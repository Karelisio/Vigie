package alerts

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/traffic"
)

// trafficKey mémorise en base le dernier niveau signalé : « AAAA-MM:niveau ».
const trafficKey = "traffic_alert"

// quotaLevel : 0 sous le seuil d'alerte, 1 seuil atteint (warning), 2 quota atteint (critical).
func quotaLevel(pct float64, alert int) int {
	switch {
	case pct >= 100:
		return 2
	case pct >= float64(alert):
		return 1
	}
	return 0
}

// evaluateTraffic prévient quand le trafic sortant du mois atteint le seuil d'alerte, puis le quota :
// une seule fois par mois et par niveau, même après un redémarrage de l'agent.
func (e *Engine) evaluateTraffic(now time.Time) {
	set := e.set.Get()
	if set.NetQuotaTB <= 0 {
		return
	}
	r, err := traffic.Build(e.ctx, e.st, set.NetQuotaTB, now)
	if err != nil {
		e.log.Warn("quota de trafic", "erreur", err)
		return
	}
	level := quotaLevel(r.Pct, set.NetQuotaAlert)
	sent := 0
	if month, lvl, ok := strings.Cut(e.st.Get(e.ctx, trafficKey), ":"); ok && month == r.Month {
		sent, _ = strconv.Atoi(lvl)
	}
	if level <= sent {
		return
	}
	if err := e.st.Set(e.ctx, trafficKey, r.Month+":"+strconv.Itoa(level)); err != nil {
		e.log.Warn("quota de trafic", "erreur", err)
		return
	}
	used := fmt.Sprintf("%s envoyés sur %s ce mois-ci", traffic.Volume(r.Tx), traffic.Volume(r.Quota))
	n := Notification{
		Kind: "traffic", Severity: "warning", Target: "quota",
		Title: fmt.Sprintf("Trafic sortant à %.0f %% du quota", r.Pct),
		Body:  fmt.Sprintf("%s. Au rythme actuel : %s d'ici la fin du mois.", used, traffic.Volume(r.ProjectedTx)),
	}
	if level == 2 {
		n.Severity = "critical"
		n.Title = "Quota de trafic sortant atteint"
		n.Body = fmt.Sprintf("%s. Remise à zéro dans %.0f jour(s).", used, math.Ceil(r.DaysLeft))
	}
	e.dispatch(n, false, true)
}
