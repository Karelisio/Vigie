package alerts

import (
	"time"

	"github.com/karelisio/vigie/agent/internal/report"
)

// reportKey mémorise en base l'échéance (AAAA-MM-JJ) du dernier rapport hebdomadaire envoyé.
const reportKey = "weekly_report"

// weeklyDue renvoie la dernière échéance du rapport : dimanche 19:00, heure locale.
func weeklyDue(now time.Time) time.Time {
	due := time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday()), 19, 0, 0, 0, now.Location())
	if due.After(now) {
		due = due.AddDate(0, 0, -7)
	}
	return due
}

// weeklyReport envoie le rapport de la semaine le dimanche à 19:00 (ou, agent arrêté ou heures calmes,
// dès que possible dans les 24 h), une seule fois par semaine, même après un redémarrage de l'agent.
func (e *Engine) weeklyReport(now time.Time) {
	set := e.set.Get()
	due := weeklyDue(now)
	if !set.WeeklyReport || now.Sub(due) > 24*time.Hour || set.InQuietHours(now) {
		return
	}
	key := due.Format("2006-01-02")
	if e.st.Get(e.ctx, reportKey) == key {
		return
	}
	r, err := report.Build(e.ctx, e.st, report.FromMonitor(e.mon), now, 7)
	if err == nil {
		err = e.st.Set(e.ctx, reportKey, key)
	}
	if err != nil {
		e.log.Warn("rapport hebdomadaire", "erreur", err)
		return
	}
	e.dispatch(Notification{
		Kind: "report", Severity: "info", Target: "semaine",
		Title: "Rapport de la semaine",
		Body:  r.Text(),
	}, false, true)
}
