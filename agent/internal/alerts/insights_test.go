package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func alertsOfKind(t *testing.T, st *store.Store, kind string) []store.Alert {
	t.Helper()
	list, err := st.Alerts(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Alert
	for _, a := range list {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func TestQuotaLevel(t *testing.T) {
	cases := []struct {
		pct   float64
		alert int
		want  int
	}{{50, 80, 0}, {79.9, 80, 0}, {80, 80, 1}, {99.9, 80, 1}, {100, 80, 2}, {120, 100, 2}, {99, 100, 0}}
	for _, c := range cases {
		if got := quotaLevel(c.pct, c.alert); got != c.want {
			t.Errorf("quotaLevel(%v, %d) = %d, attendu %d", c.pct, c.alert, got, c.want)
		}
	}
}

func TestTrafficAlertsOncePerMonthAndLevel(t *testing.T) {
	e, st := newEngine(t)
	ctx := context.Background()
	s := e.set.Get()
	s.NetQuotaTB, s.NetQuotaAlert = 1e-6, 80 // quota de 1 Mo
	if _, err := e.set.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	send := func(month time.Time, tx int64) {
		if err := st.AddTraffic(ctx, month.Format("2006-01"), 0, tx, month.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(n int, severity string) {
		t.Helper()
		list := alertsOfKind(t, st, "traffic")
		if len(list) != n || (n > 0 && list[0].Severity != severity) {
			t.Fatalf("alertes = %+v, attendu %d (dernière : %s)", list, n, severity)
		}
	}

	send(now, 500_000)
	e.evaluateTraffic(now)
	expect(0, "")
	send(now, 350_000) // 85 %
	e.evaluateTraffic(now)
	e.evaluateTraffic(now)
	expect(1, "warning")
	send(now, 200_000) // 105 %
	e.evaluateTraffic(now)
	e.evaluateTraffic(now)
	expect(2, "critical")

	// Agent redémarré : les niveaux déjà signalés sont mémorisés en base.
	restarted := New(e.ctx, e.cfg, e.mon, e.dk, st, e.log)
	restarted.evaluateTraffic(now)
	expect(2, "critical")

	// Nouveau mois : on repart de zéro (quota dépassé d'emblée : une seule alerte, critique).
	next := time.Date(2026, 11, 1, 0, 30, 0, 0, time.Local)
	send(next, 2_000_000)
	restarted.evaluateTraffic(next)
	restarted.evaluateTraffic(next)
	expect(3, "critical")
}

func TestWeeklyDue(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, loc) }
	// Le 1er novembre 2026 est un dimanche.
	cases := []struct{ now, want time.Time }{
		{at(11, 1, 19, 0), at(11, 1, 19, 0)},
		{at(11, 1, 18, 59), at(10, 25, 19, 0)},
		{at(11, 2, 7, 0), at(11, 1, 19, 0)},
		{at(11, 7, 23, 0), at(11, 1, 19, 0)},
		{at(11, 8, 20, 0), at(11, 8, 19, 0)},
	}
	for _, c := range cases {
		if got := weeklyDue(c.now); !got.Equal(c.want) {
			t.Errorf("weeklyDue(%v) = %v, attendu %v", c.now, got, c.want)
		}
	}
}

func TestWeeklyReportOncePerWeek(t *testing.T) {
	e, st := newEngine(t)
	ctx := context.Background()
	sunday := time.Date(2026, 11, 1, 19, 0, 0, 0, time.Local)
	e.weeklyReport(sunday.Add(-time.Minute))
	if n := len(alertsOfKind(t, st, "report")); n != 0 {
		t.Fatalf("rapport envoyé avant l'heure (%d)", n)
	}
	e.weeklyReport(sunday)
	e.weeklyReport(sunday.Add(time.Hour))
	// Redémarrage de l'agent : l'envoi est mémorisé en base.
	New(e.ctx, e.cfg, e.mon, e.dk, st, e.log).weeklyReport(sunday.Add(2 * time.Hour))
	list := alertsOfKind(t, st, "report")
	if len(list) != 1 || list[0].Title != "Rapport de la semaine" || list[0].Severity != "info" || list[0].Body == "" {
		t.Fatalf("rapports = %+v", list)
	}
	// Plus de 24 h après l'échéance : semaine sautée.
	e.weeklyReport(sunday.Add(8*24*time.Hour + time.Minute))
	e.weeklyReport(sunday.Add(7 * 24 * time.Hour))
	if n := len(alertsOfKind(t, st, "report")); n != 2 {
		t.Fatalf("%d rapports, attendu 2", n)
	}
	s := e.set.Get()
	s.WeeklyReport = false
	if _, err := e.set.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	e.weeklyReport(sunday.Add(14 * 24 * time.Hour))
	if n := len(alertsOfKind(t, st, "report")); n != 2 {
		t.Fatalf("rapport envoyé alors que désactivé (%d)", n)
	}
}
