package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

var paris = time.FixedZone("CET", 3600)

func at(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, paris) }

func TestMeterAccumulates(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	m := NewMeter(st)
	steps := []struct {
		ts     time.Time
		rx, tx uint64
	}{
		{at(2026, 10, 31, 23, 56), 1000, 5000}, // premier relevé : rien à ajouter
		{at(2026, 10, 31, 23, 57), 1500, 7000}, // +500 / +2000
		{at(2026, 10, 31, 23, 58), 0, 0},       // compteurs illisibles : ignoré
		{at(2026, 10, 31, 23, 59), 200, 300},   // compteurs remis à zéro : +200 / +300
		{at(2026, 11, 1, 0, 0), 1200, 1300},    // minute de minuit (heure locale) : nouveau mois, +1000 / +1000
	}
	for _, s := range steps {
		if err := m.Sample(ctx, s.ts, s.rx, s.tx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.TrafficMonths(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	want := []store.TrafficMonth{
		{Month: "2026-10", Rx: 700, Tx: 2300, Since: at(2026, 10, 31, 23, 57).UnixMilli()},
		{Month: "2026-11", Rx: 1000, Tx: 1000, Since: at(2026, 11, 1, 0, 0).UnixMilli()},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("mois = %+v, attendu %+v", got, want)
	}

	// 13 mois conservés, mois en cours compris : octobre 2026 disparaît en novembre 2027.
	if err := m.Sample(ctx, at(2027, 11, 15, 12, 0), 1400, 1500); err != nil {
		t.Fatal(err)
	}
	got, _ = st.TrafficMonths(ctx, 12)
	if len(got) != 2 || got[0].Month != "2026-11" || got[1].Month != "2027-11" || got[1].Rx != 200 {
		t.Fatalf("après un an : %+v", got)
	}
}

func TestProject(t *testing.T) {
	end := at(2026, 11, 1, 0, 0)
	// Mesure commencée le 11 : 2 To en 10 jours, 11 jours restants.
	if got := project(2e12, at(2026, 10, 11, 0, 0), at(2026, 10, 21, 0, 0), end); got != 4.2e12 {
		t.Errorf("projection = %d, attendu 4,2 To", got)
	}
	// Six minutes de mesure : rythme calculé sur une heure au moins.
	if got := project(1e9, at(2026, 10, 31, 14, 0), at(2026, 10, 31, 14, 6), at(2026, 10, 31, 23, 6)); got != 10e9 {
		t.Errorf("projection = %d, attendu 10 Go", got)
	}
}

func TestBuild(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	_ = st.AddTraffic(ctx, "2026-09", 5e11, 1e12, at(2026, 9, 1, 0, 0).UnixMilli())
	_ = st.AddTraffic(ctx, "2026-10", 1e11, 2e12, at(2026, 10, 11, 0, 0).UnixMilli())
	r, err := Build(ctx, st, 10, at(2026, 10, 21, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if r.Month != "2026-10" || r.Tx != 2e12 || r.Rx != 1e11 || r.Quota != 10e12 || r.Pct != 20 ||
		r.ProjectedTx != 4.2e12 || r.DaysLeft != 11 || len(r.History) != 2 || r.History[0].Month != "2026-09" {
		t.Fatalf("rapport = %+v", r)
	}
	r, _ = Build(ctx, st, 0, at(2026, 11, 2, 12, 0))
	if r.Quota != 0 || r.Pct != 0 || r.Tx != 0 || r.ProjectedTx != 0 || r.DaysLeft != 28.5 {
		t.Fatalf("mois sans trafic ni quota = %+v", r)
	}
}

func TestVolume(t *testing.T) {
	cases := map[int64]string{
		0: "0 o", 999: "999 o", 1000: "1 ko", 1_500_000: "1,5 Mo", 12_340_000: "12,3 Mo",
		850e9: "850 Go", 1.64e12: "1,64 To", 10e12: "10 To",
	}
	for b, want := range cases {
		if got := Volume(b); got != want {
			t.Errorf("Volume(%d) = %q, attendu %q", b, got, want)
		}
	}
}
