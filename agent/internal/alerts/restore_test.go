package alerts

import (
	"context"
	"testing"
)

func TestNotifyRestore(t *testing.T) {
	e, st := newEngine(t)
	e.NotifyRestore("sonarr_config", true, "sonarr_config restauré : copie du 28/10/2026 à 03:30", "")
	e.NotifyRestore("sonarr_config", false, "", "copie de sécurité impossible : stockage : 403 AccessDenied — rien n'a été modifié")
	// Un échec sur un autre élément ne résout pas le premier.
	e.NotifyRestore("/srv/radarr", true, "/srv/radarr restauré", "")
	e.NotifyRestore("sonarr_config", true, "sonarr_config restauré", "")
	list, _ := st.Alerts(context.Background(), 10)
	if len(list) != 4 {
		t.Fatalf("4 alertes attendues : %+v", list)
	}
	back, other, failed, done := list[0], list[1], list[2], list[3]
	if done.Kind != "job" || done.Title != "Restauration de sonarr_config terminée" || !done.Resolved {
		t.Errorf("réussite : %+v", done)
	}
	if failed.Kind != "restore" || failed.Severity != "critical" || failed.Title != "Échec de la restauration de sonarr_config" || failed.Resolved ||
		failed.Body == "" {
		t.Errorf("échec : %+v", failed)
	}
	if other.Kind != "job" || other.Title != "Restauration de /srv/radarr terminée" {
		t.Errorf("autre élément : %+v", other)
	}
	if back.Kind != "restore" || !back.Resolved || back.Title != "Restauration de sonarr_config réussie" {
		t.Errorf("retour à la normale : %+v", back)
	}
}
