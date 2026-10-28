package updater

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Paris, même sans base de fuseaux sur la machine

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

func TestNextRun(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, paris) }
	for _, c := range []struct {
		name  string
		now   time.Time
		clock string
		last  string
		grace time.Duration
		want  time.Time
	}{
		{"avant l'heure", at(10, 3, 0), "04:00", "", 0, at(10, 4, 0)},
		{"pile à l'heure", at(10, 4, 0), "04:00", "2026-10-09", 0, at(10, 4, 0)},
		{"heure passée", at(10, 4, 1), "04:00", "", 0, at(11, 4, 0)},
		{"déjà lancé aujourd'hui", at(10, 3, 0), "04:00", "2026-10-10", 0, at(11, 4, 0)},
		{"rattrapage au démarrage", at(10, 4, 20), "04:00", "2026-10-09", 30 * time.Minute, at(10, 4, 0)},
		{"rattrapage trop tardif", at(10, 4, 40), "04:00", "", 30 * time.Minute, at(11, 4, 0)},
		{"rattrapage déjà fait", at(10, 4, 20), "04:00", "2026-10-10", 30 * time.Minute, at(11, 4, 0)},
		{"fin de mois", at(31, 23, 50), "23:30", "2026-10-31", 0, time.Date(2026, 11, 1, 23, 30, 0, 0, paris)},
		// Passage à l'heure d'hiver le 25 : le lendemain de 04:00 est toujours à 04:00 (25 h plus tard).
		{"changement d'heure", at(24, 5, 0), "04:00", "2026-10-24", 0, at(25, 4, 0)},
	} {
		got, err := nextRun(c.now, c.clock, c.last, c.grace)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s : %v (%v), attendu %v", c.name, got, err, c.want)
		}
	}
	if next, _ := nextRun(at(24, 5, 0), "04:00", "", 0); next.Sub(at(24, 4, 0)) != 25*time.Hour {
		t.Errorf("le jour du changement d'heure dure 25 h : %v", next.Sub(at(24, 4, 0)))
	}
	if _, err := nextRun(at(10, 3, 0), "25:00", "", 0); err == nil {
		t.Error("heure invalide acceptée")
	}
}

func TestAutoMembers(t *testing.T) {
	list := []monitor.Container{
		{ID: "1", Name: "comet", State: "running"},
		{ID: "2", Name: "redis", State: "running"},
		{ID: "3", Name: "prowlarr", State: "running"},
		{ID: "4", Name: "sonarr", State: "exited"},
		{ID: "5", Name: "vigie-agent", State: "running", Self: true},
	}
	updates := map[string]images.Status{
		"comet": {UpdateAvailable: true}, "prowlarr": {UpdateAvailable: true},
		"sonarr": {UpdateAvailable: true}, "vigie-agent": {UpdateAvailable: true},
		"redis": {Error: "registre : code 500"},
	}
	names := func(ms []Member) string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Name)
		}
		return strings.Join(out, ",")
	}

	s := settings.Settings{AutoUpdateList: []string{"comet", "redis", "sonarr", "vigie-agent"}}
	got := autoMembers(s, list, updates)
	if names(got) != "comet,sonarr" || got[1].Running {
		t.Errorf("liste : %+v", got)
	}
	s.AutoUpdateAgent = true
	if got := autoMembers(s, list, updates); names(got) != "comet,sonarr,vigie-agent" || !got[2].Self {
		t.Errorf("avec l'agent : %+v", got)
	}
	s = settings.Settings{AutoUpdateAll: true}
	if got := names(autoMembers(s, list, updates)); got != "comet,prowlarr,sonarr" {
		t.Errorf("tous : %s", got)
	}
	if got := autoMembers(s, list, nil); len(got) != 0 {
		t.Errorf("vérification des images impossible : %+v", got)
	}
}

// nightly prépare un planificateur sur un faux démon Docker où comet a une nouvelle image.
func nightly(t *testing.T, s *settings.Settings, available *bool) (*Nightly, *Manager, *store.Store) {
	f, srv := newFake(t)
	old := setupComet(f)
	m := newManager(t, srv.URL, false)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	n := &Nightly{
		upd: m, st: st, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		settings: func() settings.Settings { return *s },
		refresh: func(context.Context) map[string]images.Status {
			return map[string]images.Status{"comet": {UpdateAvailable: *available}}
		},
		containers: func() []monitor.Container { return []monitor.Container{{ID: old.ID, Name: "comet", State: "running"}} },
	}
	return n, m, st
}

func TestNightlyRunsOncePerDay(t *testing.T) {
	s := &settings.Settings{AutoUpdate: true, AutoUpdateTime: "04:00", AutoUpdateList: []string{"comet"}}
	available := true
	n, m, st := nightly(t, s, &available)
	ctx := t.Context()
	day := func(d, h, min int) time.Time { return time.Date(2026, 10, d, h, min, 0, 0, time.UTC) }

	n.tick(ctx, day(10, 3, 59))
	if len(m.Jobs()) != 0 {
		t.Fatal("lancé avant l'heure")
	}
	n.tick(ctx, day(10, 4, 0))
	j := waitJob(t, m)
	if j.Kind != "auto" || j.Target != "nightly" || !j.OK || len(j.Results) != 1 || !j.Results[0].Updated {
		t.Fatalf("job = %+v", j)
	}
	if got := st.Get(ctx, nightlyKey); got != "2026-10-10" {
		t.Fatalf("date mémorisée = %q", got)
	}
	n.tick(ctx, day(10, 4, 1))
	if len(m.Jobs()) != 1 {
		t.Fatal("relancé le même jour")
	}
	// Après un redémarrage de l'agent (fin de la mise à jour de l'agent lui-même), rien n'est relancé.
	again, m2, _ := nightly(t, s, &available)
	again.st = st
	again.plan(ctx, day(10, 4, 5), s.AutoUpdateTime, startupGrace)
	again.tick(ctx, day(10, 4, 6))
	if len(m2.Jobs()) != 0 || !again.next.Equal(day(11, 4, 0)) {
		t.Fatalf("relancé après redémarrage, prochain = %v", again.next)
	}

	// Nouvelle heure dans les réglages, sans redémarrage : prise en compte dès le lendemain.
	s.AutoUpdateTime = "02:30"
	n.tick(ctx, day(11, 1, 0))
	if !n.next.Equal(day(11, 2, 30)) {
		t.Fatalf("prochain = %v", n.next)
	}
	// Rien à mettre à jour : pas d'opération, mais le jour est consommé.
	available = false
	n.tick(ctx, day(11, 2, 30))
	if len(m.Jobs()) != 1 || st.Get(ctx, nightlyKey) != "2026-10-11" {
		t.Fatalf("jobs = %d, date = %q", len(m.Jobs()), st.Get(ctx, nightlyKey))
	}
	// Désactivé : plus rien n'est prévu.
	s.AutoUpdate = false
	n.tick(ctx, day(12, 2, 30))
	if !n.next.IsZero() {
		t.Fatalf("prochain = %v", n.next)
	}
}

func TestNightlyRetriesWhenBusy(t *testing.T) {
	s := &settings.Settings{AutoUpdate: true, AutoUpdateTime: "23:50", AutoUpdateAll: true}
	available := true
	n, m, st := nightly(t, s, &available)
	ctx := t.Context()
	m.current = &Job{} // une autre opération est en cours

	start := time.Date(2026, 10, 10, 23, 50, 0, 0, time.UTC)
	for i := 0; i <= maxRetries; i++ {
		n.tick(ctx, start.Add(time.Duration(i)*retryDelay))
		if i < maxRetries && (n.tries != i+1 || !n.next.Equal(start.Add(time.Duration(i+1)*retryDelay))) {
			t.Fatalf("essai %d : tries = %d, prochain = %v", i, n.tries, n.next)
		}
	}
	// Abandon après trois nouveaux essais, au nom du jour prévu malgré le passage de minuit.
	if got := st.Get(ctx, nightlyKey); got != "2026-10-10" {
		t.Fatalf("date mémorisée = %q", got)
	}
	if want := time.Date(2026, 10, 11, 23, 50, 0, 0, time.UTC); !n.next.Equal(want) {
		t.Fatalf("prochain = %v", n.next)
	}
	if log, _ := st.AuditLog(ctx, 5); len(log) != 1 || log[0].Action != "update-auto" || !strings.HasPrefix(log[0].Result, "refusé") {
		t.Fatalf("journal = %+v", log)
	}
}

func TestNightlyTitleAndBody(t *testing.T) {
	j := Job{Kind: "auto", Results: []Result{
		{Name: "comet", OK: true, Updated: true}, {Name: "redis", OK: true, Updated: true},
		{Name: "prowlarr", OK: true}, {Name: "sonarr", Message: "healthcheck en échec"},
	}}
	if got := j.Title(); got != "Mises à jour de la nuit : 2 mis à jour, 1 déjà à jour, 1 échec" {
		t.Errorf("titre = %q", got)
	}
	if got := j.Body(); got != "Mis à jour : comet, redis. Échec : sonarr." {
		t.Errorf("corps = %q", got)
	}
	ok := Job{Kind: "auto", OK: true, Results: []Result{{Name: "comet", OK: true, Updated: true}}}
	if ok.Title() != "Mises à jour de la nuit : 1 mis à jour" {
		t.Errorf("titre = %q", ok.Title())
	}
	if cut := (Job{Kind: "auto", Message: "Échec : context canceled"}); cut.Title() != "Mises à jour de la nuit : échec" || cut.Body() != cut.Message {
		t.Errorf("interrompu : %q / %q", cut.Title(), cut.Body())
	}
}
