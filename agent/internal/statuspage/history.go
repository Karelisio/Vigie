package statuspage

import (
	"context"
	"sort"
	"time"

	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	Days           = 90                 // barres de disponibilité
	incidentWindow = 7 * 24 * time.Hour // incidents affichés
	maxIncidents   = 20
	// Arrêt de conteneur plus court ignoré : redémarrage ou mise à jour.
	minContainerIncident = 2 * time.Minute
)

const day = 24 * time.Hour

// dayStarts renvoie les Days derniers jours (minuit UTC), du plus ancien à aujourd'hui.
func dayStarts(now time.Time) []time.Time {
	today := now.UTC().Truncate(day)
	out := make([]time.Time, Days)
	for i := range out {
		out[i] = today.Add(-time.Duration(Days-1-i) * day)
	}
	return out
}

// checkHistory : disponibilité quotidienne (90 jours d'historique) et coupures des 7 derniers jours
// (au moins DownAfter échecs consécutifs, comme les alertes).
func (m *Manager) checkHistory(ctx context.Context, id int64, now time.Time) history {
	h := history{Uptime: -1}
	buckets, _ := m.st.UptimeBuckets(ctx, id, Days*day, Days)
	byDay := map[int64]store.UptimeBucket{}
	for _, b := range buckets {
		byDay[b.TS] = b
	}
	total, ok := 0, 0
	for _, d := range dayStarts(now) {
		b := byDay[d.UnixMilli()]
		r := -1.0
		if b.Total > 0 {
			r = float64(b.OK) / float64(b.Total)
			total += b.Total
			ok += b.OK
			h.DataDays++
		}
		h.Days = append(h.Days, Day{Date: d, Ratio: r})
	}
	if total > 0 {
		h.Uptime = float64(ok) / float64(total)
	}
	runs, _ := m.st.CheckRuns(ctx, id, now.Add(-incidentWindow-day))
	h.incidents = recent(outages(runs, m.opt.DownAfter), now)
	return h
}

// outages repère les suites d'au moins downAfter échecs : de l'instant du premier échec à la réussite suivante.
func outages(runs []store.RunPoint, downAfter int) []Incident {
	var out []Incident
	fails := 0
	var start int64
	for _, r := range runs {
		if !r.OK {
			if fails == 0 {
				start = r.TS
			}
			fails++
			continue
		}
		if fails >= downAfter {
			out = append(out, Incident{Status: Offline, Start: time.UnixMilli(start), End: time.UnixMilli(r.TS)})
		}
		fails = 0
	}
	if fails >= downAfter {
		out = append(out, Incident{Status: Offline, Start: time.UnixMilli(start)})
	}
	return out
}

// containerHistory : disponibilité quotidienne d'après les mesures de l'agent (le conteneur tournait-il
// à chaque quart d'heure mesuré ? 8 jours d'historique) et incidents d'après sa chronologie Docker.
func (m *Manager) containerHistory(ctx context.Context, name string, now time.Time) history {
	h := history{Uptime: -1}
	starts := dayStarts(now)
	presence, _ := m.st.ContainerPresence(ctx, name, starts[0])
	measured, running := 0, 0
	for _, d := range starts {
		p := presence[d.UnixMilli()]
		r := -1.0
		if p.Measured > 0 {
			r = float64(p.Running) / float64(p.Measured)
			measured += p.Measured
			running += p.Running
			h.DataDays++
		}
		h.Days = append(h.Days, Day{Date: d, Ratio: r})
	}
	if measured > 0 {
		h.Uptime = float64(running) / float64(measured)
	}
	events, _ := m.st.Events(ctx, name, 500)
	var live *monitor.Container
	for _, c := range m.opt.Containers() {
		if c.Name == name {
			live = &c
			break
		}
	}
	h.incidents = recent(containerIncidents(events, live), now)
	return h
}

// containerIncidents lit la chronologie (du plus récent au plus ancien) : arrêté de « die » (ou pause)
// à « start », dégradé de « unhealthy » à « healthy ». live est l'état actuel (nil : conteneur absent).
func containerIncidents(events []store.Event, live *monitor.Container) []Incident {
	var out []Incident
	var down, degraded time.Time
	closeAt := func(from *time.Time, status string, at time.Time) {
		if !from.IsZero() && at.Sub(*from) >= minContainerIncident {
			out = append(out, Incident{Status: status, Start: *from, End: at})
		}
		*from = time.Time{}
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Source != "docker" {
			continue
		}
		at := time.UnixMilli(ev.TS)
		switch ev.Action {
		case "die", "pause":
			closeAt(&degraded, Degraded, at)
			if down.IsZero() {
				down = at
			}
		case "start", "unpause":
			closeAt(&down, Offline, at)
		case "health":
			switch ev.Detail {
			case "unhealthy":
				if down.IsZero() && degraded.IsZero() {
					degraded = at
				}
			case "healthy":
				closeAt(&degraded, Degraded, at)
			}
		}
	}
	running := live != nil && live.State == "running"
	if !down.IsZero() {
		switch {
		case !running:
			out = append(out, Incident{Status: Offline, Start: down})
		case live.StartedAt > down.UnixMilli():
			// Redémarrage manqué (agent arrêté entre-temps) : la fin est l'heure de démarrage.
			closeAt(&down, Offline, time.UnixMilli(live.StartedAt))
		}
	}
	if !degraded.IsZero() && running && live.Health == "unhealthy" {
		out = append(out, Incident{Status: Degraded, Start: degraded})
	}
	return out
}

// recent garde les incidents des 7 derniers jours (ou en cours).
func recent(list []Incident, now time.Time) []Incident {
	out := []Incident{}
	for _, inc := range list {
		if inc.End.IsZero() || now.Sub(inc.End) <= incidentWindow {
			out = append(out, inc)
		}
	}
	return out
}

// sortIncidents : en cours d'abord, puis du plus récent au plus ancien.
func sortIncidents(list []Incident) {
	sort.SliceStable(list, func(i, j int) bool {
		if a, b := list[i].End.IsZero(), list[j].End.IsZero(); a != b {
			return a
		}
		return list[i].Start.After(list[j].Start)
	})
}

// fresh corrige un incident « en cours » d'un historique mis en cache quand l'élément est de nouveau en ligne.
func fresh(list []Incident, status string, now time.Time) []Incident {
	out := make([]Incident, len(list))
	for i, inc := range list {
		if inc.End.IsZero() && (status == Online || status == Unknown) {
			inc.End = now
		}
		out[i] = inc
	}
	return out
}
