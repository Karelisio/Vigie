package alerts

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/cleanup"
	"github.com/karelisio/vigie/agent/internal/storage"
)

const (
	// diskForecastKey mémorise l'alerte « disque bientôt plein » : en cours, et date du dernier envoi.
	diskForecastKey = "disk_forecast_alert"
	// volumeGrowthKey mémorise, par volume, la date de la dernière alerte de croissance.
	volumeGrowthKey = "volume_growth_alerts"
	// Rappel de l'alerte de prévision tant que le disque reste sous le préavis.
	diskForecastRemind = 3 * 24 * time.Hour
	// Une alerte de croissance par jour et par volume : les volumes sont mesurés toutes les 6 h, la marge
	// évite de reporter l'alerte au calcul suivant quand l'un d'eux a été un peu plus rapide.
	volumeGrowthRepeat = 23 * time.Hour
)

type diskForecastState struct {
	Active bool  `json:"active"`
	Sent   int64 `json:"sent"`
}

// OnDiskForecast prévient quand, au rythme actuel, le disque sera plein dans moins de disk_forecast_days
// jours : une fois, puis un rappel tous les 3 jours au plus ; résolue quand la prévision repasse nettement
// au-dessus (une fois et demie le préavis) ou que la croissance s'arrête.
func (e *Engine) OnDiskForecast(d storage.Disk, vols []storage.Volume) {
	e.evaluateDiskForecast(d, vols, time.Now())
}

func (e *Engine) evaluateDiskForecast(d storage.Disk, vols []storage.Volume, now time.Time) {
	limit := float64(e.set.Get().DiskForecastDays)
	var state diskForecastState
	_ = json.Unmarshal([]byte(e.st.Get(e.ctx, diskForecastKey)), &state)
	save := func(s diskForecastState) bool {
		b, _ := json.Marshal(s)
		if err := e.st.Set(e.ctx, diskForecastKey, string(b)); err != nil {
			e.log.Warn("prévision du disque", "erreur", err)
			return false
		}
		return true
	}
	switch {
	case limit > 0 && d.Status == storage.StatusOK && d.DaysLeft != nil && *d.DaysLeft < limit:
		if state.Active && now.Sub(time.UnixMilli(state.Sent)) < diskForecastRemind {
			return
		}
		if save(diskForecastState{Active: true, Sent: now.UnixMilli()}) {
			e.dispatch(diskForecastNotification(d, vols), false, true)
		}
	case !state.Active, d.Status == storage.StatusInsufficient:
		// Rien en cours, ou plus assez de mesures pour conclure : l'état ne change pas.
	case limit <= 0:
		save(diskForecastState{}) // alerte désactivée : rien à signaler
	case d.DaysLeft == nil || *d.DaysLeft >= math.Max(limit*1.5, limit+2):
		if save(diskForecastState{}) {
			body := "La croissance du disque s'est arrêtée."
			if d.DaysLeft != nil {
				body = fmt.Sprintf("Au rythme actuel, il reste %s avant d'être plein.", daysText(*d.DaysLeft))
			}
			e.dispatch(Notification{
				Kind: "storage", Severity: "info", Target: "disque",
				Title: "Disque : prévision rassurante", Body: body,
			}, true, true)
		}
	}
}

// daysText : « moins d'un jour », « ~1 jour », « ~12 jours », « plus d'un an ».
func daysText(days float64) string {
	switch n := int(math.Round(days)); {
	case days < 1:
		return "moins d'un jour"
	case n == 1:
		return "~1 jour"
	case n > 365:
		return "plus d'un an"
	default:
		return fmt.Sprintf("~%d jours", n)
	}
}

func perDay(b int64) string { return "+" + cleanup.HumanBytes(b) + "/jour" }

func diskForecastNotification(d storage.Disk, vols []storage.Volume) Notification {
	days := *d.DaysLeft
	n := Notification{Kind: "storage", Severity: "warning", Target: "disque"}
	if days <= 3 {
		n.Severity = "critical"
	}
	n.Title = "Disque plein dans " + daysText(days)
	if days < 1 {
		n.Title = "Disque plein d'ici demain"
	}
	rate := int64(0)
	if d.PerDay != nil {
		rate = *d.PerDay
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Au rythme actuel (%s), le disque sera plein dans %s.", perDay(rate), daysText(days))
	// Piste : le volume qui grossit le plus, s'il compte pour une bonne part de la croissance.
	var top *storage.Volume
	for i := range vols {
		if g := vols[i].GrowthDay; g != nil && *g > 0 && (top == nil || *g > *top.GrowthDay) {
			top = &vols[i]
		}
	}
	if top != nil && float64(*top.GrowthDay) >= 0.25*float64(rate) {
		fmt.Fprintf(&b, " Le volume %s grossit le plus (%s).", top.Name, perDay(*top.GrowthDay))
	}
	b.WriteString(" Libère de la place : nettoyage du disque, plus gros volumes (Réglages › Stockage).")
	n.Body = b.String()
	return n
}

// OnVolumes prévient quand un volume grossit de plus de volume_growth_gb Go par jour (mesuré sur environ
// 24 h), une fois par jour au plus et par volume, même après un redémarrage de l'agent.
func (e *Engine) OnVolumes(vols []storage.Volume) { e.evaluateVolumes(vols, time.Now()) }

func (e *Engine) evaluateVolumes(vols []storage.Volume, now time.Time) {
	limitGB := e.set.Get().VolumeGrowthGB
	if limitGB <= 0 {
		return
	}
	limit := int64(limitGB * (1 << 30))
	sent := map[string]int64{}
	_ = json.Unmarshal([]byte(e.st.Get(e.ctx, volumeGrowthKey)), &sent)
	for name, ts := range sent {
		if now.Sub(time.UnixMilli(ts)) > 7*24*time.Hour {
			delete(sent, name)
		}
	}
	var notes []Notification
	for _, v := range vols {
		if v.GrowthDay == nil || *v.GrowthDay < limit {
			continue
		}
		if ts, ok := sent[v.Name]; ok && now.Sub(time.UnixMilli(ts)) < volumeGrowthRepeat {
			continue
		}
		sent[v.Name] = now.UnixMilli()
		body := fmt.Sprintf("%s en 24 h, %s au total.", "+"+cleanup.HumanBytes(*v.GrowthDay), cleanup.HumanBytes(v.Size))
		if len(v.Containers) > 0 {
			body += " Utilisé par " + strings.Join(v.Containers, ", ") + "."
		}
		notes = append(notes, Notification{
			Kind: "storage", Severity: "warning", Target: v.Name,
			Title: "Le volume " + v.Name + " grossit vite",
			Body:  body,
		})
	}
	if len(notes) == 0 {
		return
	}
	raw, _ := json.Marshal(sent)
	if err := e.st.Set(e.ctx, volumeGrowthKey, string(raw)); err != nil {
		e.log.Warn("croissance des volumes", "erreur", err)
		return
	}
	for _, n := range notes {
		e.dispatch(n, false, true)
	}
}
