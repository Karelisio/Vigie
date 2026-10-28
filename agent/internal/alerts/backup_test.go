package alerts

import (
	"context"
	"testing"
)

func TestNotifyBackup(t *testing.T) {
	e, st := newEngine(t)
	// Réussite, appli fermée : simple notification de fin d'opération.
	e.NotifyBackup(true, "2 éléments sauvegardés, 3 Mo envoyés", "")
	// Échec : alerte critique, puis retour à la normale à la réussite suivante.
	e.NotifyBackup(false, "", "1 élément sur 2 en échec — sonarr_config : stockage : 403 AccessDenied")
	e.NotifyBackup(true, "2 éléments sauvegardés, 3 Mo envoyés", "")
	list, _ := st.Alerts(context.Background(), 10)
	if len(list) != 3 {
		t.Fatalf("3 alertes attendues : %+v", list)
	}
	done, failed, back := list[2], list[1], list[0]
	if done.Kind != "job" || done.Title != "Sauvegarde terminée" || !done.Resolved {
		t.Errorf("réussite : %+v", done)
	}
	if failed.Kind != "backup" || failed.Severity != "critical" || failed.Title != "Échec de la sauvegarde" || failed.Resolved {
		t.Errorf("échec : %+v", failed)
	}
	if back.Kind != "backup" || !back.Resolved || back.Title != "Sauvegarde de nouveau réussie" {
		t.Errorf("retour à la normale : %+v", back)
	}
}
