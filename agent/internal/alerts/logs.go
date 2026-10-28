package alerts

import (
	"fmt"

	"github.com/karelisio/vigie/agent/internal/logwatch"
)

// NotifyLog signale une ligne de log correspondant à une alerte définie par l'utilisateur.
// Le délai entre deux alertes d'une même règle et la sourdine sont appliqués par logwatch.
func (e *Engine) NotifyLog(h logwatch.Hit) {
	body := h.Line
	switch {
	case h.Others == 1:
		body += "\n+1 autre occurrence"
	case h.Others > 1:
		body += fmt.Sprintf("\n+%d autres occurrences", h.Others)
	}
	e.dispatch(Notification{
		Kind: "log", Severity: "warning", Target: h.Container, ContainerID: h.ContainerID,
		Title: fmt.Sprintf("%s : « %s »", h.Container, h.Pattern),
		Body:  body,
	}, false, true)
}
