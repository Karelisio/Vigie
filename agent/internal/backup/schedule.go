package backup

// Sauvegarde quotidienne : chaque jour à l'heure choisie (fuseau TZ de l'agent), une fois par jour
// au plus, même après un redémarrage ; si une autre opération est en cours, nouveaux essais.

import (
	"context"
	"errors"
	"time"

	"github.com/karelisio/vigie/agent/internal/updater"
)

const (
	dayLayout  = "2006-01-02"
	retryDelay = 10 * time.Minute
	maxRetries = 3
	// Au démarrage, un créneau passé depuis peu est rattrapé (agent redémarré à l'heure prévue).
	startupGrace = 30 * time.Minute
)

// schedule est l'état du planificateur (utilisé par la seule goroutine Run).
type schedule struct {
	clock string    // heure réglée lors du calcul de next
	day   string    // jour du créneau en cours
	next  time.Time // prochain déclenchement (zéro : à calculer)
	tries int       // essais refusés car une opération était en cours
}

// nextSlot renvoie le prochain passage à l'heure clock (HH:MM, fuseau de now). Le créneau du jour
// reste dû s'il n'a pas été lancé (last : jour du dernier lancement) et n'est pas dépassé de plus de grace.
func nextSlot(now time.Time, clock, last string, grace time.Duration) (time.Time, error) {
	t, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := now.Date()
	at := time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, now.Location())
	if at.Format(dayLayout) == last || now.Sub(at) > grace {
		// time.Date normalise le lendemain (24 h ne font pas toujours un jour).
		at = time.Date(y, m, d+1, t.Hour(), t.Minute(), 0, 0, now.Location())
	}
	return at, nil
}

// NextRun renvoie la prochaine sauvegarde planifiée (ms), 0 si la planification est coupée.
func (s *Service) NextRun(ctx context.Context) int64 {
	cfg, _ := s.load(ctx)
	if !cfg.Schedule {
		return 0
	}
	at, err := nextSlot(time.Now(), cfg.Time, s.st.Get(ctx, lastKey), 0)
	if err != nil {
		return 0
	}
	return at.UnixMilli()
}

// Run vérifie chaque minute si le créneau est atteint : un changement d'heure est pris en compte
// sans redémarrage.
func (s *Service) Run(ctx context.Context) {
	s.tick(ctx, time.Now(), startupGrace)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.tick(ctx, now, 0)
		}
	}
}

func (s *Service) tick(ctx context.Context, now time.Time, grace time.Duration) {
	cfg, _ := s.load(ctx)
	sch := &s.sched
	if !cfg.Schedule {
		*sch = schedule{}
		return
	}
	if sch.next.IsZero() || cfg.Time != sch.clock {
		// Activation ou nouvelle heure : un créneau déjà passé aujourd'hui attend demain.
		s.plan(ctx, now, cfg.Time, grace)
	}
	if sch.next.IsZero() || now.Before(sch.next) {
		return
	}
	_, err := s.Start("schedule")
	switch {
	case errors.Is(err, updater.ErrBusy) && sch.tries < maxRetries:
		sch.tries++
		sch.next = now.Add(retryDelay)
		s.log.Info("sauvegarde reportée : une opération est en cours", "essai", sch.tries, "reprise", sch.next.Format("15:04"))
		return
	case err != nil:
		s.log.Warn("sauvegarde planifiée abandonnée", "erreur", err)
		s.st.Audit(ctx, "agent", "backup", "sauvegarde", "refusé : "+err.Error(), "vigie")
	default:
		s.log.Info("sauvegarde planifiée lancée")
		s.st.Audit(ctx, "agent", "backup", "sauvegarde", "lancé", "vigie")
	}
	// Jour traité, même en cas d'échec : il ne sera pas relancé, même après un redémarrage.
	if err := s.st.Set(ctx, lastKey, sch.day); err != nil {
		s.log.Warn("sauvegarde planifiée : date non enregistrée", "erreur", err)
	}
	s.plan(ctx, now, cfg.Time, 0)
}

func (s *Service) plan(ctx context.Context, now time.Time, clock string, grace time.Duration) {
	next, err := nextSlot(now, clock, s.st.Get(ctx, lastKey), grace)
	if err != nil {
		s.log.Warn("sauvegarde planifiée : heure invalide", "heure", clock, "erreur", err)
		s.sched = schedule{}
		return
	}
	s.sched = schedule{clock: clock, day: next.Format(dayLayout), next: next}
}
