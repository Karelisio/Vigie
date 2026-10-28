package alerts

import "strings"

// Conseil final du message de surconsommation, remplacé quand Vigie peut revenir en arrière lui-même.
const dockhandHint = "reviens à l'image précédente dans Dockhand"

// NotifyRegression signale un conteneur qui consomme nettement plus depuis sa mise à jour. Si sa
// version précédente est disponible, la notification propose d'y revenir.
func (e *Engine) NotifyRegression(name, title, body string) {
	if e.cfg.Ignore[name] || e.set.Get().IsMuted(name) {
		return
	}
	e.dispatch(regressionNotice(name, title, body, e.Rollback != nil && e.Rollback(name)), false, true)
}

// regressionNotice prépare l'alerte ; rollback : bouton « Revenir à la version précédente » (push).
func regressionNotice(name, title, body string, rollback bool) Notification {
	n := Notification{Kind: "regression", Severity: "warning", Target: name, Title: title, Body: body}
	if rollback {
		n.Action = "rollback"
		if strings.Contains(body, dockhandHint) {
			n.Body = strings.Replace(body, dockhandHint, "reviens à la version précédente depuis Vigie", 1)
		} else {
			n.Body += " Tu peux revenir à la version précédente depuis Vigie."
		}
	}
	return n
}
