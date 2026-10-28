package alerts

import (
	"context"
	"testing"

	"github.com/karelisio/vigie/agent/internal/autoheal"
)

func TestNotifyAutoHeal(t *testing.T) {
	e, st := newEngine(t)
	// Retour à la normale sans abandon signalé : rien.
	e.NotifyAutoHeal(autoheal.Notice{Container: "comet", Severity: "info", Resolved: true, Title: "comet fonctionne de nouveau"})
	e.NotifyAutoHeal(autoheal.Notice{Container: "comet", ContainerID: "abc", Severity: "info", Title: "Réparation automatique de comet", Body: "redémarré"})
	e.NotifyAutoHeal(autoheal.Notice{Container: "comet", Severity: "critical", Title: "Réparation automatique abandonnée : comet"})
	e.NotifyAutoHeal(autoheal.Notice{Container: "comet", Severity: "info", Resolved: true, Title: "comet fonctionne de nouveau"})
	list, _ := st.Alerts(context.Background(), 10)
	if len(list) != 3 {
		t.Fatalf("3 alertes attendues, obtenu %+v", list)
	}
	if a := list[2]; a.Kind != "autoheal" || a.Severity != "info" || a.Target != "comet" || a.Body != "redémarré" || a.Resolved {
		t.Errorf("réparation %+v", a)
	}
	if a := list[1]; a.Severity != "critical" || a.Resolved {
		t.Errorf("abandon %+v", a)
	}
	if a := list[0]; !a.Resolved || a.Title != "comet fonctionne de nouveau" {
		t.Errorf("retour à la normale %+v", a)
	}

	// Conteneur en sourdine : aucune notification.
	if _, err := e.set.SetMuted(context.Background(), "redis", true); err != nil {
		t.Fatal(err)
	}
	e.NotifyAutoHeal(autoheal.Notice{Container: "redis", Severity: "info", Title: "Réparation automatique de redis"})
	if list, _ := st.Alerts(context.Background(), 10); len(list) != 3 {
		t.Errorf("notification d'un conteneur en sourdine : %+v", list[0])
	}
}
