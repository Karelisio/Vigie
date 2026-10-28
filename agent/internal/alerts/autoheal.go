package alerts

import "github.com/karelisio/vigie/agent/internal/autoheal"

// NotifyAutoHeal transmet une notification de la réparation automatique. Les réparations sont de
// simples informations ; l'abandon est une alerte critique, close au retour à la normale du conteneur.
func (e *Engine) NotifyAutoHeal(n autoheal.Notice) {
	key := "autoheal:" + n.Container
	a := Notification{
		Kind: "autoheal", Severity: n.Severity, Target: n.Container, ContainerID: n.ContainerID,
		Title: n.Title, Body: n.Body,
	}
	if n.Resolved {
		e.mu.Lock()
		active := e.active[key]
		e.mu.Unlock()
		if active {
			e.resolve(key, a)
		}
		return
	}
	if e.set.Get().IsMuted(n.Container) {
		return
	}
	if n.Severity == "critical" {
		e.fire(key, a)
		return
	}
	e.dispatch(a, false, true)
}
