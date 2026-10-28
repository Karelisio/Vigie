package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/channels"
	"github.com/karelisio/vigie/agent/internal/debrid"
	"github.com/karelisio/vigie/agent/internal/store"
)

func premium(id string, left time.Duration) debrid.Status {
	return debrid.Status{
		ID: id, Provider: debrid.RealDebrid, Label: "Perso", OK: true, Premium: left > 0,
		Expires: time.Now().Add(left).UnixMilli(), CheckedAt: time.Now().UnixMilli(),
	}
}

func alertTitles(t *testing.T, st *store.Store) []string {
	t.Helper()
	list, err := st.Alerts(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i].Severity+" "+list[i].Title)
	}
	return out
}

func TestDebridAlertsOncePerLevel(t *testing.T) {
	e, st := newEngine(t)
	end := time.Now().Add(30 * 24 * time.Hour)
	a := premium("a", time.Until(end))
	at := func(before time.Duration) time.Time { return end.Add(-before) }

	e.evaluateDebrid([]debrid.Status{a}, at(8*24*time.Hour)) // plus de 7 jours : rien
	e.evaluateDebrid([]debrid.Status{a}, at(5*24*time.Hour+time.Hour))
	e.evaluateDebrid([]debrid.Status{a}, at(4*24*time.Hour)) // même seuil, même échéance : rien de plus
	e.evaluateDebrid([]debrid.Status{a}, at(20*time.Hour))
	// Vérification impossible : ni alerte ni oubli des seuils.
	e.evaluateDebrid([]debrid.Status{{ID: "a", Provider: debrid.RealDebrid, Error: "délai dépassé"}}, at(10*time.Hour))
	e.evaluateDebrid([]debrid.Status{a}, at(10*time.Hour))
	a.Premium = false
	e.evaluateDebrid([]debrid.Status{a}, at(-time.Hour))

	// Redémarrage de l'agent : les seuils déjà signalés sont mémorisés en base.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := New(ctx, e.cfg, e.mon, e.dk, st, e.log)
	again.evaluateDebrid([]debrid.Status{a}, at(-2*time.Hour))

	// Renouvelé : nouvelle échéance lointaine, puis de nouveau à moins de 7 jours.
	renewed := premium("a", 40*24*time.Hour)
	again.OnDebrid([]debrid.Status{renewed})
	renewed.Expires = time.Now().Add(6*24*time.Hour + time.Hour).UnixMilli()
	again.OnDebrid([]debrid.Status{renewed})

	want := []string{
		"warning Premium Real-Debrid (Perso) : 5 jours restants",
		"critical Premium Real-Debrid (Perso) : dernier jour",
		"critical Premium Real-Debrid (Perso) expiré",
		"warning Premium Real-Debrid (Perso) : 6 jours restants",
	}
	got := alertTitles(t, st)
	if len(got) != len(want) {
		t.Fatalf("alertes : %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alerte %d : %q, attendu %q", i, got[i], want[i])
		}
	}
}

func TestDebridExpiredAndRefused(t *testing.T) {
	e, st := newEngine(t)
	expired := premium("a", -time.Hour)
	bad := debrid.Status{ID: "b", Provider: debrid.TorBox, Label: "TorBox", Refused: true, Error: "clé API refusée"}
	e.OnDebrid([]debrid.Status{expired, bad})
	e.OnDebrid([]debrid.Status{expired, bad})
	// Clé corrigée puis de nouveau refusée : nouvelle alerte.
	fixed := premium("b", 60*24*time.Hour)
	e.OnDebrid([]debrid.Status{expired, fixed})
	e.OnDebrid([]debrid.Status{expired, bad})
	got := alertTitles(t, st)
	if len(got) != 3 || got[0] != "critical Premium Real-Debrid (Perso) expiré" ||
		got[1] != "critical Clé TorBox refusée" || got[2] != "critical Clé TorBox refusée" {
		t.Fatalf("alertes : %q", got)
	}
	// Compte supprimé : sa mémoire disparaît.
	e.OnDebrid(nil)
	var sent map[string]string
	_ = json.Unmarshal([]byte(st.Get(context.Background(), debridKey)), &sent)
	if len(sent) != 0 {
		t.Fatalf("mémoire non vidée : %v", sent)
	}
}

// Les canaux d'alerte reçoivent ce que reçoivent les notifications push, même sans Firebase :
// rien pendant la maintenance, puis le bilan.
func TestChannelsFollowPush(t *testing.T) {
	got := make(chan map[string]any, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- body
	}))
	defer srv.Close()
	e, _ := newEngine(t)
	cfg := channels.Config{Channels: []channels.Channel{{Type: channels.TypeNtfy, Enabled: true, Server: srv.URL, Topic: "vigie"}}}
	if _, err := e.Channels.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	next := func() string {
		select {
		case b := <-got:
			return b["title"].(string)
		case <-time.After(3 * time.Second):
			return ""
		}
	}

	e.OnEvent(healthEvent("unhealthy"))
	if title := next(); title != "web en mauvaise santé" {
		t.Fatalf("alerte relayée : %q", title)
	}
	s := e.set.Get()
	s.MaintenanceUntil = time.Now().Add(time.Hour).UnixMilli()
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.OnDebrid([]debrid.Status{premium("a", -time.Hour)})
	s.MaintenanceUntil = 0
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.flushMaintenance(time.Now())
	if title := next(); title != "Fin de maintenance : 1 alerte(s)" {
		t.Fatalf("pendant la maintenance, seul le bilan doit partir : %q", title)
	}
}

func TestDebridServiceDown(t *testing.T) {
	e, st := newEngine(t)
	start := time.Now().Add(-12 * time.Minute)
	svc := debrid.Service{Provider: debrid.RealDebrid, Name: "Real-Debrid", State: debrid.Down, Failures: 1, Detail: "délai dépassé",
		Incidents: []debrid.Incident{{Start: start.UnixMilli(), Cause: "délai dépassé"}}}
	e.OnDebridServices([]debrid.Service{svc}) // panne isolée : rien
	svc.Failures = 2
	e.OnDebridServices([]debrid.Service{svc})
	e.OnDebridServices([]debrid.Service{svc}) // déjà signalée
	svc.State, svc.Failures = debrid.Up, 0
	svc.Incidents[0].End = start.Add(12 * time.Minute).UnixMilli()
	e.OnDebridServices([]debrid.Service{svc})
	e.OnDebridServices([]debrid.Service{svc})
	got := alertTitles(t, st)
	if len(got) != 2 || got[0] != "critical Real-Debrid ne répond plus" || got[1] != "info Real-Debrid répond de nouveau" {
		t.Fatalf("alertes : %q", got)
	}
	list, _ := st.Alerts(context.Background(), 5)
	if list[0].Body != "Panne de 12 min." || list[1].Body != "délai dépassé. Les addons qui l'utilisent ne trouvent plus de liens." {
		t.Fatalf("textes : %q / %q", list[0].Body, list[1].Body)
	}

	// Réglage coupé : aucune alerte.
	s := e.set.Get()
	s.DebridDownAlert = false
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	svc.State, svc.Failures = debrid.Down, 3
	e.OnDebridServices([]debrid.Service{svc})
	if n := len(alertTitles(t, st)); n != 2 {
		t.Fatalf("alerte malgré le réglage : %d", n)
	}
}
