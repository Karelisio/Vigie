package alerts

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/domains"
)

// domainKey mémorise en base les alertes d'expiration envoyées : domaine → « expiration:niveau ».
const domainKey = "domain_alerts"

// domainLevel : 0 rien à signaler, 1 expiration dans le préavis choisi (warning), 2 dans 7 jours (critical),
// 3 expiré (critical).
func domainLevel(expires int64, warnDays int, now time.Time) int {
	if expires == 0 {
		return 0
	}
	switch left := time.UnixMilli(expires).Sub(now); {
	case left <= 0:
		return 3
	case left <= 7*24*time.Hour:
		return 2
	case left <= time.Duration(warnDays)*24*time.Hour:
		return 1
	}
	return 0
}

// OnDomains prévient quand un nom de domaine approche de son expiration (préavis des réglages, 7 jours)
// ou a expiré : une seule fois par seuil et par date d'expiration, même après un redémarrage de l'agent.
// Un renouvellement (date d'expiration repoussée) clôt l'alerte.
func (e *Engine) OnDomains(list []domains.Status) { e.evaluateDomains(list, time.Now()) }

func (e *Engine) evaluateDomains(list []domains.Status, now time.Time) {
	sent := map[string]string{}
	_ = json.Unmarshal([]byte(e.st.Get(e.ctx, domainKey)), &sent)
	next := map[string]string{}
	warn := e.set.Get().DomainDays
	type note struct {
		n        Notification
		resolved bool
	}
	var notes []note
	for _, s := range list {
		prev, had := sent[s.Domain]
		dueRaw, levelRaw, _ := strings.Cut(prev, ":")
		prevDue, _ := strconv.ParseInt(dueRaw, 10, 64)
		prevLevel, _ := strconv.Atoi(levelRaw)
		if s.Expires == 0 {
			// Date inconnue (jamais vérifié, registre muet ou en erreur) : rien ne change.
			if had {
				next[s.Domain] = prev
			}
			continue
		}
		if had && s.Expires > prevDue {
			notes = append(notes, note{renewedNotification(s), true})
			had = false
		}
		level := domainLevel(s.Expires, warn, now)
		if level == 0 {
			continue // échéance lointaine : les seuils sont réarmés
		}
		if had && prevDue == s.Expires && prevLevel >= level {
			next[s.Domain] = prev
			continue
		}
		next[s.Domain] = strconv.FormatInt(s.Expires, 10) + ":" + strconv.Itoa(level)
		notes = append(notes, note{domainNotification(s, level, now), false})
	}
	raw, _ := json.Marshal(next)
	if err := e.st.Set(e.ctx, domainKey, string(raw)); err != nil {
		e.log.Warn("alertes des domaines", "erreur", err)
		return
	}
	for _, n := range notes {
		e.dispatch(n.n, n.resolved, true)
	}
}

func domainNotification(s domains.Status, level int, now time.Time) Notification {
	n := Notification{Kind: "domain", Severity: "critical", Target: s.Domain}
	end := time.UnixMilli(s.Expires)
	renew := "Pense à le renouveler."
	if s.Registrar != "" {
		renew = "Renouvelle-le chez " + s.Registrar
		if !strings.HasSuffix(renew, ".") {
			renew += "."
		}
	}
	switch days := domains.DaysLeft(s.Expires, now); {
	case level == 3:
		n.Title = "Domaine " + s.Domain + " expiré"
		n.Body = "Expiré le " + end.Format("02/01/2006") + " : les services qui l'utilisent vont cesser de répondre. " +
			"Renouvelle-le vite, tant que le bureau d'enregistrement le permet."
	case days <= 0:
		n.Title = "Domaine " + s.Domain + " : dernier jour"
		n.Body = "Il expire le " + end.Format("02/01/2006 à 15:04") + ". " + renew
	default:
		if level == 1 {
			n.Severity = "warning"
		}
		n.Title = fmt.Sprintf("Domaine %s : %d jours restants", s.Domain, days)
		if days == 1 {
			n.Title = "Domaine " + s.Domain + " : 1 jour restant"
		}
		n.Body = "Il expire le " + end.Format("02/01/2006") + ". " + renew
	}
	return n
}

func renewedNotification(s domains.Status) Notification {
	return Notification{
		Kind: "domain", Severity: "info", Target: s.Domain,
		Title: "Domaine " + s.Domain + " renouvelé",
		Body:  "Il expire maintenant le " + time.UnixMilli(s.Expires).Format("02/01/2006") + ".",
	}
}
