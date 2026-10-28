package backup

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Paris, même sans base de fuseaux sur la machine
)

func TestNextSlot(t *testing.T) {
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
		{"avant l'heure", at(10, 3, 0), "03:30", "", 0, at(10, 3, 30)},
		{"pile à l'heure", at(10, 3, 30), "03:30", "2026-10-09", 0, at(10, 3, 30)},
		{"heure passée", at(10, 3, 31), "03:30", "", 0, at(11, 3, 30)},
		{"déjà faite aujourd'hui", at(10, 3, 0), "03:30", "2026-10-10", 0, at(11, 3, 30)},
		{"rattrapage au démarrage", at(10, 3, 50), "03:30", "2026-10-09", 30 * time.Minute, at(10, 3, 30)},
		{"rattrapage trop tardif", at(10, 4, 10), "03:30", "", 30 * time.Minute, at(11, 3, 30)},
		// Passage à l'heure d'hiver le 25 : le lendemain de 03:30 est toujours à 03:30.
		{"changement d'heure", at(24, 5, 0), "03:30", "2026-10-24", 0, at(25, 3, 30)},
	} {
		got, err := nextSlot(c.now, c.clock, c.last, c.grace)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s : %v (%v), attendu %v", c.name, got, err, c.want)
		}
	}
	if _, err := nextSlot(at(10, 3, 0), "3h30", "", 0); err == nil {
		t.Error("heure invalide acceptée")
	}
}

func TestScheduledRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.configure(t, Selection{ID: vigieID})
	c.Schedule, c.Time = true, "03:30"
	if _, err := e.svc.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	day := func(h, m int) time.Time { return time.Date(2026, 10, 28, h, m, 0, 0, time.UTC) }

	e.svc.tick(ctx, day(3, 0), startupGrace)
	if len(e.upd.Jobs()) != 0 {
		t.Fatal("sauvegarde lancée avant l'heure")
	}

	// Une autre opération est en cours : nouvel essai 10 minutes plus tard.
	release := make(chan struct{})
	if _, err := e.upd.Run("cleanup", "cleanup", "disque", func(ctx context.Context, _ func(float64, string)) (string, error) {
		<-release
		return "ok", nil
	}); err != nil {
		t.Fatal(err)
	}
	e.svc.tick(ctx, day(3, 30), 0)
	if e.svc.sched.tries != 1 || !e.svc.sched.next.Equal(day(3, 40)) || e.st.Get(ctx, lastKey) != "" {
		t.Fatalf("report attendu : %+v", e.svc.sched)
	}
	close(release)
	waitJob(t, e.upd)

	e.svc.tick(ctx, day(3, 40), 0)
	j := waitJob(t, e.upd)
	if j.Kind != "backup" || !j.OK {
		t.Fatalf("sauvegarde planifiée : %+v", j)
	}
	if h := e.svc.History(ctx); len(h) != 1 || h[0].Trigger != "schedule" {
		t.Fatalf("historique : %+v", h)
	}
	if e.st.Get(ctx, lastKey) != "2026-10-28" || !e.svc.sched.next.Equal(time.Date(2026, 10, 29, 3, 30, 0, 0, time.UTC)) {
		t.Fatalf("prochaine sauvegarde : %+v", e.svc.sched)
	}

	// Une seule fois par jour, même après un redémarrage de l'agent.
	e.svc.sched = schedule{}
	e.svc.tick(ctx, day(3, 45), startupGrace)
	if n := len(e.upd.Jobs()); n != 2 {
		t.Fatalf("relancée le même jour : %d opérations", n)
	}
	if e.svc.NextRun(ctx) == 0 {
		t.Error("prochaine sauvegarde inconnue")
	}
}
