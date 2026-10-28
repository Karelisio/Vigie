package alerts

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/debrid"
)

// debridKey mémorise en base les alertes debrid envoyées : ID du compte → « échéance:niveau ».
const debridKey = "debrid_alerts"

// debridLevel : 0 rien à signaler, 1 premium qui expire dans 7 jours (warning), 2 dans 1 jour (critical),
// 3 premium expiré ou clé refusée (critical).
func debridLevel(s debrid.Status, now time.Time) int {
	switch {
	case s.Refused || (s.OK && !s.Premium):
		return 3
	case !s.OK || s.Expires == 0:
		return 0
	}
	switch left := time.UnixMilli(s.Expires).Sub(now); {
	case left <= 24*time.Hour:
		return 2
	case left <= 7*24*time.Hour:
		return 1
	}
	return 0
}

// OnDebrid prévient quand le premium d'un compte debrid arrive à échéance (7 jours, puis 1 jour), a expiré,
// ou que sa clé est refusée : une seule fois par niveau et par échéance, même après un redémarrage de l'agent.
// Une vérification impossible (réseau, service indisponible) ne change rien.
func (e *Engine) OnDebrid(list []debrid.Status) { e.evaluateDebrid(list, time.Now()) }

func (e *Engine) evaluateDebrid(list []debrid.Status, now time.Time) {
	sent := map[string]string{}
	_ = json.Unmarshal([]byte(e.st.Get(e.ctx, debridKey)), &sent)
	next := map[string]string{}
	var notes []Notification
	for _, s := range list {
		if !s.OK && !s.Refused {
			if v, ok := sent[s.ID]; ok {
				next[s.ID] = v
			}
			continue
		}
		level := debridLevel(s, now)
		if level == 0 {
			continue // échéance lointaine ou renouvelée : les seuils sont réarmés
		}
		due := strconv.FormatInt(s.Expires, 10)
		if s.Refused {
			due = "clé"
		}
		if prevDue, prevLevel, ok := strings.Cut(sent[s.ID], ":"); ok && prevDue == due {
			if n, _ := strconv.Atoi(prevLevel); n >= level {
				next[s.ID] = sent[s.ID]
				continue
			}
		}
		next[s.ID] = due + ":" + strconv.Itoa(level)
		notes = append(notes, debridNotification(s, level, now))
	}
	raw, _ := json.Marshal(next)
	if err := e.st.Set(e.ctx, debridKey, string(raw)); err != nil {
		e.log.Warn("alertes debrid", "erreur", err)
		return
	}
	for _, n := range notes {
		e.dispatch(n, false, true)
	}
}

func debridNotification(s debrid.Status, level int, now time.Time) Notification {
	name := s.Name()
	n := Notification{Kind: "debrid", Severity: "critical", Target: name}
	end := time.UnixMilli(s.Expires)
	switch {
	case s.Refused:
		n.Title = "Clé " + name + " refusée"
		n.Body = "Vérification impossible : " + s.Error + ". Mets la clé à jour dans Vigie."
	case level == 3:
		n.Title = "Premium " + name + " expiré"
		n.Body = "Les addons qui l'utilisent ne trouvent plus de liens."
		if s.Expires > 0 {
			n.Body = "Terminé le " + end.Format("02/01/2006") + ". " + n.Body
		}
	case level == 2:
		n.Title = "Premium " + name + " : dernier jour"
		n.Body = "Il expire le " + end.Format("02/01/2006 à 15:04") + ". Pense à le renouveler."
	default:
		n.Severity = "warning"
		n.Title = fmt.Sprintf("Premium %s : %d jours restants", name, int(end.Sub(now).Hours()/24))
		if end.Sub(now) < 48*time.Hour {
			n.Title = "Premium " + name + " : 1 jour restant"
		}
		n.Body = "Il expire le " + end.Format("02/01/2006") + ". Pense à le renouveler."
	}
	return n
}

// OnDebridServices prévient quand un service debrid ne répond plus (panne confirmée par deux vérifications
// d'affilée), puis quand il répond de nouveau. Réglage : DebridDownAlert.
func (e *Engine) OnDebridServices(list []debrid.Service) {
	for _, s := range list {
		key := "debrid-service:" + s.Provider
		e.mu.Lock()
		active := e.active[key]
		e.mu.Unlock()
		switch {
		case s.Confirmed() && !active && e.set.Get().DebridDownAlert:
			e.fire(key, Notification{
				Kind: "debrid", Severity: "critical", Target: s.Name,
				Title: s.Name + " ne répond plus",
				Body:  s.Detail + ". Les addons qui l'utilisent ne trouvent plus de liens.",
			})
		case s.State != debrid.Down && active:
			n := Notification{Kind: "debrid", Severity: "info", Target: s.Name, Title: s.Name + " répond de nouveau"}
			if k := len(s.Incidents); k > 0 && s.Incidents[k-1].End > 0 {
				n.Body = "Panne de " + humanDuration(time.UnixMilli(s.Incidents[k-1].End).Sub(time.UnixMilli(s.Incidents[k-1].Start))) + "."
			}
			e.resolve(key, n)
		}
	}
}
