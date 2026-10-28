package storage

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

const (
	gb    = float64(1 << 30)
	total = 47 * gb // disque de démarrage Oracle Always Free
	step  = 15 * time.Minute
)

// series fabrique des mesures toutes les 15 min sur span, en arrondissant comme l'historique
// (pourcentage au dixième, moyenné sur la tranche : bruit d'environ 0,05 %).
func series(span time.Duration, used func(h float64) float64) []Sample {
	rng := rand.New(rand.NewPCG(1, 2))
	end := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var out []Sample
	for t := end.Add(-span); !t.After(end); t = t.Add(step) {
		h := t.Sub(end.Add(-span)).Hours()
		pct := math.Round((used(h)/total*100+rng.NormFloat64()*0.05)*10) / 10
		out = append(out, Sample{TS: t.UnixMilli(), Used: pct / 100 * total})
	}
	return out
}

func near(t *testing.T, label string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s : %.3f, attendu %.3f ± %.3f", label, got, want, tol)
	}
}

func TestForecastGrowth(t *testing.T) {
	// 20 Go occupés, +1,5 Go par jour pendant 7 jours.
	tr := Estimate(series(7*24*time.Hour, func(h float64) float64 { return 20*gb + 1.5*gb*h/24 }), total)
	if tr.Status != StatusOK {
		t.Fatalf("statut %q, attendu ok", tr.Status)
	}
	near(t, "Go/jour", tr.PerDay/gb, 1.5, 0.1)
	near(t, "recul (h)", tr.Hours, 168, 0.01)
	// 47 - 30,5 = 16,5 Go libres : plein dans 11 jours.
	near(t, "jours", DaysLeft(total-30.5*gb, tr.PerDay), 11, 1)
}

func TestForecastPlateau(t *testing.T) {
	tr := Estimate(series(7*24*time.Hour, func(float64) float64 { return 30 * gb }), total)
	if tr.Status != StatusStable {
		t.Fatalf("statut %q (%.3f Go/jour), attendu stable", tr.Status, tr.PerDay/gb)
	}
	// Légère baisse (rotation des logs) : jamais de prévision.
	tr = Estimate(series(5*24*time.Hour, func(h float64) float64 { return 30*gb - 0.2*gb*h/24 }), total)
	if tr.Status != StatusStable || tr.PerDay >= 0 {
		t.Fatalf("baisse : %q %.3f Go/jour", tr.Status, tr.PerDay/gb)
	}
}

func TestForecastIgnoresCleanup(t *testing.T) {
	// +1 Go par jour, nettoyage de 8 Go au 4e jour (images inutilisées supprimées) : la tendance reste +1 Go/jour.
	used := func(h float64) float64 {
		v := 25*gb + gb*h/24
		if h >= 96.3 {
			v -= 8 * gb
		}
		return v
	}
	tr := Estimate(series(7*24*time.Hour, used), total)
	if tr.Status != StatusOK || tr.Cleanups != 1 {
		t.Fatalf("statut %q, %d nettoyage(s)", tr.Status, tr.Cleanups)
	}
	near(t, "Go/jour", tr.PerDay/gb, 1, 0.1)

	// Sans le nettoyage écarté, la pente brute serait faussée : vérification du contraire.
	raw := theilSen(hourly(series(7*24*time.Hour, used)))
	if math.Abs(raw*24/gb-1) < 0.2 {
		t.Fatalf("la pente brute devrait être faussée (%.2f Go/jour)", raw*24/gb)
	}
}

func TestForecastTemporaryFileIsNotCleanup(t *testing.T) {
	// Plateau avec un fichier temporaire de 6 Go pendant 1 h (sauvegarde), 12 h ou 36 h :
	// la baisse défait une hausse brutale, ce n'est ni un nettoyage ni de la croissance.
	for _, hours := range []float64{1, 12, 36} {
		tr := Estimate(series(7*24*time.Hour, func(h float64) float64 {
			if h >= 80 && h < 80+hours {
				return 36 * gb
			}
			return 30 * gb
		}), total)
		if tr.Status != StatusStable || tr.Cleanups != 0 {
			t.Fatalf("%v h : statut %q, %d nettoyage(s), %.2f Go/jour", hours, tr.Status, tr.Cleanups, tr.PerDay/gb)
		}
	}
}

func TestForecastRecurringPurges(t *testing.T) {
	// +1 Go par jour, purgé chaque nuit : c'est le rythme normal, pas de prévision.
	tr := Estimate(series(7*24*time.Hour, func(h float64) float64 {
		return 28*gb + gb*math.Mod(h, 24)/24
	}), total)
	if tr.Cleanups != 0 || tr.Status == StatusOK {
		t.Fatalf("statut %q, %d nettoyage(s), %.2f Go/jour", tr.Status, tr.Cleanups, tr.PerDay/gb)
	}
}

func TestForecastNeedsHistory(t *testing.T) {
	grow := func(h float64) float64 { return 20*gb + 3*gb*h/24 }
	if tr := Estimate(series(40*time.Hour, grow), total); tr.Status != StatusInsufficient || tr.PerDay != 0 {
		t.Fatalf("40 h : %+v", tr)
	}
	if tr := Estimate(nil, total); tr.Status != StatusInsufficient {
		t.Fatalf("aucune mesure : %+v", tr)
	}
	// 3 jours mais seulement quelques mesures (agent arrêté) : pas de conclusion.
	s := series(72*time.Hour, grow)
	sparse := []Sample{s[0], s[100], s[len(s)-1]}
	if tr := Estimate(sparse, total); tr.Status != StatusInsufficient {
		t.Fatalf("mesures éparses : %+v", tr)
	}
	// 49 h suffisent.
	if tr := Estimate(series(49*time.Hour, grow), total); tr.Status != StatusOK {
		t.Fatalf("49 h : %+v", tr)
	}
}

func TestPointsHourly(t *testing.T) {
	s := series(3*time.Hour, func(float64) float64 { return 10 * gb })
	pts := Points(s)
	if len(pts) != 4 { // 9 h, 10 h, 11 h, 12 h
		t.Fatalf("%d points : %+v", len(pts), pts)
	}
	for i := 1; i < len(pts); i++ {
		if pts[i].TS-pts[i-1].TS != time.Hour.Milliseconds() {
			t.Fatalf("points non horaires : %+v", pts)
		}
	}
}
