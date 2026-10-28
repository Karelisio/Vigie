package alerts

import (
	"context"
	"strings"
	"testing"
)

func TestRegressionOffersRollback(t *testing.T) {
	body := "RAM : 200 Mo → 900 Mo (moyennes avant et depuis la mise à jour). Si ça persiste, vérifie ses logs ou " + dockhandHint + "."
	if n := regressionNotice("comet", "comet consomme plus", body, false); n.Action != "" || n.Body != body {
		t.Errorf("sans version précédente : %+v", n)
	}
	n := regressionNotice("comet", "comet consomme plus", body, true)
	if n.Action != "rollback" || n.Kind != "regression" || strings.Contains(n.Body, "Dockhand") ||
		!strings.Contains(n.Body, "reviens à la version précédente depuis Vigie") {
		t.Errorf("avec version précédente : %+v", n)
	}
	if n := regressionNotice("comet", "t", "Autre texte.", true); !strings.HasSuffix(n.Body, "depuis Vigie.") {
		t.Errorf("conseil ajouté : %q", n.Body)
	}

	e, st := newEngine(t)
	asked := ""
	e.Rollback = func(name string) bool { asked = name; return true }
	e.NotifyRegression("comet", "comet consomme plus depuis sa mise à jour", body)
	list, _ := st.Alerts(context.Background(), 5)
	if asked != "comet" || len(list) != 1 || !strings.Contains(list[0].Body, "depuis Vigie") {
		t.Fatalf("alerte = %+v (demande pour %q)", list, asked)
	}
}
