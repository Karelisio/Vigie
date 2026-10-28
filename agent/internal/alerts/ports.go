package alerts

import (
	"github.com/karelisio/vigie/agent/internal/ports"
)

// NotifyPort signale un nouveau port ouvert sur Internet (non autorisé, hors Pangolin), une seule fois
// tant qu'il reste ouvert, puis sa fermeture. Le scanner ne l'appelle que si le réglage port_alert est actif.
func (e *Engine) NotifyPort(c ports.Change) {
	p := c.Port
	label := p.Key() + " (" + p.Owner() + ")"
	if !c.Open {
		e.dispatch(Notification{
			Kind: "port", Severity: "info", Target: p.Key(),
			Title: "Port fermé : " + label,
			Body:  "Il n'est plus ouvert sur Internet.",
		}, true, true)
		return
	}
	var body string
	switch {
	case p.Container != "" && p.HostNetwork:
		body = p.Container + " écoute sur toutes les interfaces de l'hôte (réseau hôte) : joignable sans passer par Pangolin."
	case p.Container != "":
		body = p.Container + " publie ce port sur l'hôte : joignable sans passer par Pangolin."
	default:
		body = "Un processus de l'hôte écoute sur " + p.Address + "."
	}
	body += " Si le pare-feu d'Oracle le laisse passer, il est accessible depuis Internet. Autorise-le dans Vigie s'il est voulu."
	e.dispatch(Notification{
		Kind: "port", Severity: "warning", Target: p.Key(),
		Title: "Nouveau port ouvert sur Internet : " + label,
		Body:  body,
	}, false, true)
}
