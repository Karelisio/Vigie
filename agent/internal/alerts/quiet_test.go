package alerts

import (
	"context"
	"testing"
	"time"
)

// Le bilan des mises à jour de la nuit, même réussi, arrive dans le résumé de fin des heures calmes.
func TestJobResultKeptForQuietSummary(t *testing.T) {
	e, _ := newEngine(t)
	s := e.set.Get()
	now := time.Now()
	s.QuietEnabled, s.QuietStart, s.QuietEnd = true, now.Add(-time.Hour).Format("15:04"), now.Add(time.Hour).Format("15:04")
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.NotifyJob("Mises à jour de la nuit : 2 mis à jour", "Mis à jour : comet, redis.", true)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.quiet) != 1 || e.quiet[0] != "Mises à jour de la nuit : 2 mis à jour" {
		t.Fatalf("résumé des heures calmes = %v", e.quiet)
	}
}
