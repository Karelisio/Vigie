package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/storage"
	"github.com/karelisio/vigie/agent/internal/store"
)

func forecast(days float64, perDayGB float64) storage.Disk {
	per := int64(perDayGB * (1 << 30))
	d := storage.Disk{Total: 47 << 30, Status: storage.StatusOK, PerDay: &per, DaysLeft: &days}
	return d
}

func lastAlert(t *testing.T, st *store.Store) store.Alert {
	t.Helper()
	list, err := st.Alerts(context.Background(), 1)
	if err != nil || len(list) == 0 {
		t.Fatalf("aucune alerte (%v)", err)
	}
	return list[0]
}

func TestDiskForecastAlert(t *testing.T) {
	e, st := newEngine(t)
	now := time.Now()
	day := 24 * time.Hour
	g := int64(1 << 30)
	vols := []storage.Volume{{Name: "immich_pgdata", GrowthDay: &g}, {Name: "calme"}}

	e.evaluateDiskForecast(forecast(20, 1.2), vols, now) // au-delà du préavis de 14 jours : rien
	e.evaluateDiskForecast(forecast(9.4, 1.2), vols, now)
	a := lastAlert(t, st)
	if a.Severity != "warning" || a.Title != "Disque plein dans ~9 jours" || a.Kind != "storage" {
		t.Fatalf("alerte : %+v", a)
	}
	for _, part := range []string{"Au rythme actuel (+1,2 Go/jour), le disque sera plein dans ~9 jours.", "immich_pgdata grossit le plus (+1 Go/jour)", "nettoyage du disque"} {
		if !strings.Contains(a.Body, part) {
			t.Errorf("texte sans %q : %s", part, a.Body)
		}
	}

	// Toujours sous le préavis : pas de nouvelle alerte avant 3 jours, même après un redémarrage.
	e.evaluateDiskForecast(forecast(8, 1.2), vols, now.Add(day))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := New(ctx, e.cfg, e.mon, e.dk, st, e.log)
	again.evaluateDiskForecast(forecast(7, 1.2), vols, now.Add(2*day))
	// Plus assez de mesures : rien ne change.
	again.evaluateDiskForecast(storage.Disk{Status: storage.StatusInsufficient}, nil, now.Add(2*day+time.Hour))
	// Rappel au bout de 3 jours ; critique à 3 jours ou moins.
	again.evaluateDiskForecast(forecast(2.6, 3), nil, now.Add(3*day+time.Minute))
	if a := lastAlert(t, st); a.Severity != "critical" || a.Title != "Disque plein dans ~3 jours" || strings.Contains(a.Body, "grossit le plus") {
		t.Fatalf("rappel : %+v", a)
	}
	// Prévision un peu au-dessus du préavis : pas encore résolue (hystérésis) ; nettement au-dessus : résolue.
	again.evaluateDiskForecast(forecast(16, 1), nil, now.Add(4*day))
	again.evaluateDiskForecast(forecast(30, 0.5), nil, now.Add(5*day))
	if a := lastAlert(t, st); !a.Resolved || a.Title != "Disque : prévision rassurante" || a.Body != "Au rythme actuel, il reste ~30 jours avant d'être plein." {
		t.Fatalf("résolution : %+v", a)
	}
	// Croissance arrêtée puis reprise : nouvelle alerte immédiate.
	again.evaluateDiskForecast(storage.Disk{Status: storage.StatusStable}, nil, now.Add(6*day))
	again.evaluateDiskForecast(forecast(0.4, 20), nil, now.Add(6*day+time.Hour))
	if a := lastAlert(t, st); a.Title != "Disque plein d'ici demain" || !strings.Contains(a.Body, "plein dans moins d'un jour") {
		t.Fatalf("reprise : %+v", a)
	}
	again.evaluateDiskForecast(storage.Disk{Status: storage.StatusStable}, nil, now.Add(7*day))

	want := []string{
		"warning Disque plein dans ~9 jours",
		"critical Disque plein dans ~3 jours",
		"info Disque : prévision rassurante",
		"critical Disque plein d'ici demain",
		"info Disque : prévision rassurante",
	}
	got := alertTitles(t, st)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("alertes :\n%q\nattendu :\n%q", got, want)
	}
}

func TestDiskForecastAlertDisabled(t *testing.T) {
	e, st := newEngine(t)
	s := e.set.Get()
	s.DiskForecastDays = 0
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.evaluateDiskForecast(forecast(1, 10), nil, time.Now())
	if got := alertTitles(t, st); len(got) != 0 {
		t.Fatalf("alerte désactivée : %q", got)
	}
}

func TestVolumeGrowthAlert(t *testing.T) {
	e, st := newEngine(t)
	now := time.Now()
	fast, slow := int64(6<<30), int64(1<<30)
	vols := []storage.Volume{
		{Name: "logs_data", Size: 40 << 30, GrowthDay: &fast, Containers: []string{"loki"}},
		{Name: "calme_db", Size: 2 << 30, GrowthDay: &slow},
		{Name: "nouveau", Size: 9 << 30}, // pas encore de recul
	}
	e.evaluateVolumes(vols, now)
	e.evaluateVolumes(vols, now.Add(6*time.Hour)) // même jour : rien
	// Redémarrage de l'agent : l'envoi est mémorisé en base.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := New(ctx, e.cfg, e.mon, e.dk, st, e.log)
	again.evaluateVolumes(vols, now.Add(12*time.Hour))
	again.evaluateVolumes(vols, now.Add(24*time.Hour)) // le lendemain : de nouveau

	got := alertTitles(t, st)
	if len(got) != 2 || got[0] != "warning Le volume logs_data grossit vite" || got[1] != got[0] {
		t.Fatalf("alertes : %q", got)
	}
	if a := lastAlert(t, st); a.Body != "+6 Go en 24 h, 40 Go au total. Utilisé par loki." || a.Target != "logs_data" {
		t.Fatalf("alerte : %+v", a)
	}

	// Seuil relevé à 10 Go par jour : plus d'alerte ; 0 : désactivée.
	for _, limit := range []float64{10, 0} {
		s := again.set.Get()
		s.VolumeGrowthGB = limit
		if _, err := again.set.Update(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		again.evaluateVolumes(vols, now.Add(72*time.Hour))
	}
	if n := len(alertTitles(t, st)); n != 2 {
		t.Fatalf("%d alertes après changement du seuil", n)
	}
}
