package updater

// Mises à jour automatiques de nuit : chaque jour à l'heure choisie dans les réglages, l'état des
// images est revérifié, puis les conteneurs retenus qui ont une nouvelle image sont mis à jour.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	nightlyKey = "auto_update_last" // jour (AAAA-MM-JJ) du dernier lancement, conservé en base
	dayLayout  = "2006-01-02"
	retryDelay = 10 * time.Minute // opération déjà en cours : nouvel essai plus tard
	maxRetries = 3
	// Au démarrage, un créneau passé depuis peu est rattrapé (agent redémarré à l'heure prévue).
	startupGrace = 30 * time.Minute
)

// Nightly lance chaque nuit les mises à jour automatiques choisies dans les réglages.
type Nightly struct {
	upd        *Manager
	st         *store.Store
	log        *slog.Logger
	settings   func() settings.Settings
	refresh    func(ctx context.Context) map[string]images.Status // interroge les registres
	containers func() []monitor.Container

	clock string    // heure réglée lors du dernier calcul de next
	day   string    // jour du créneau en cours (inchangé pendant les nouveaux essais)
	next  time.Time // prochain déclenchement (zéro : à calculer)
	tries int       // essais refusés car une opération était en cours
}

func NewNightly(upd *Manager, mon *monitor.Monitor, st *store.Store, log *slog.Logger) *Nightly {
	return &Nightly{
		upd: upd, st: st, log: log,
		settings:   mon.Settings().Get,
		refresh:    func(ctx context.Context) map[string]images.Status { return mon.CheckImages(ctx, true) },
		containers: mon.Containers,
	}
}

// Run vérifie chaque minute si le créneau est atteint : un changement d'heure dans les réglages
// est ainsi pris en compte sans redémarrage.
func (n *Nightly) Run(ctx context.Context) {
	if !n.upd.Enabled() {
		return
	}
	if s := n.settings(); s.AutoUpdate {
		n.plan(ctx, time.Now(), s.AutoUpdateTime, startupGrace)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			n.tick(ctx, now)
		}
	}
}

func (n *Nightly) tick(ctx context.Context, now time.Time) {
	s := n.settings()
	if !s.AutoUpdate {
		n.next, n.clock = time.Time{}, ""
		return
	}
	if n.next.IsZero() || s.AutoUpdateTime != n.clock {
		// Activation ou nouvelle heure : un créneau déjà passé aujourd'hui attend demain.
		n.plan(ctx, now, s.AutoUpdateTime, 0)
	}
	if n.next.IsZero() || now.Before(n.next) {
		return
	}
	n.launch(ctx, now, s)
}

// plan calcule le prochain créneau à partir du jour du dernier lancement.
func (n *Nightly) plan(ctx context.Context, now time.Time, clock string, grace time.Duration) {
	next, err := nextRun(now, clock, n.st.Get(ctx, nightlyKey), grace)
	if err != nil {
		n.log.Warn("mises à jour de la nuit : heure invalide", "heure", clock, "erreur", err)
		n.next = time.Time{}
		return
	}
	n.clock, n.next, n.day, n.tries = clock, next, next.Format(dayLayout), 0
}

// nextRun renvoie le prochain passage à l'heure clock (HH:MM, fuseau de now). Le créneau du jour
// reste dû s'il n'a pas été lancé (last : jour du dernier lancement) et n'est pas dépassé de plus de grace.
func nextRun(now time.Time, clock, last string, grace time.Duration) (time.Time, error) {
	t, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := now.Date()
	at := time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, now.Location())
	if at.Format(dayLayout) == last || now.Sub(at) > grace {
		// time.Date normalise le lendemain : 24 h ne font pas toujours un jour (changement d'heure).
		at = time.Date(y, m, d+1, t.Hour(), t.Minute(), 0, 0, now.Location())
	}
	return at, nil
}

func (n *Nightly) launch(ctx context.Context, now time.Time, s settings.Settings) {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	updates := n.refresh(rctx)
	cancel()
	members := autoMembers(s, n.containers(), updates)
	if len(members) == 0 {
		n.log.Info("mises à jour de la nuit : rien à mettre à jour")
		n.done(ctx, now)
		return
	}
	names := make([]string, len(members))
	for i, c := range members {
		names[i] = c.Name
	}
	target := strings.Join(names, ", ")
	_, err := n.upd.Group("auto", "nightly", "update", members)
	switch {
	case errors.Is(err, ErrBusy) && n.tries < maxRetries:
		n.tries++
		n.next = now.Add(retryDelay)
		n.log.Info("mises à jour de la nuit reportées : une opération est en cours", "essai", n.tries, "reprise", n.next.Format("15:04"))
		return
	case err != nil:
		n.log.Warn("mises à jour de la nuit abandonnées", "erreur", err)
		n.st.Audit(ctx, "agent", "update-auto", target, "refusé : "+err.Error(), "vigie")
	default:
		n.log.Info("mises à jour de la nuit lancées", "conteneurs", target)
		n.st.Audit(ctx, "agent", "update-auto", target, "lancé", "vigie")
	}
	n.done(ctx, now)
}

// done mémorise le jour traité (même après un redémarrage, il ne sera pas relancé) et planifie le suivant.
func (n *Nightly) done(ctx context.Context, now time.Time) {
	if err := n.st.Set(ctx, nightlyKey, n.day); err != nil {
		n.log.Warn("mises à jour de la nuit : date non enregistrée", "erreur", err)
	}
	n.plan(ctx, now, n.clock, 0)
}

// autoMembers retient les conteneurs dont une nouvelle image est publiée, parmi tous ou ceux de la
// liste ; l'agent seulement si demandé (Group le traite en dernier, via un conteneur auxiliaire).
func autoMembers(s settings.Settings, list []monitor.Container, updates map[string]images.Status) []Member {
	chosen := map[string]bool{}
	for _, name := range s.AutoUpdateList {
		chosen[name] = true
	}
	var out []Member
	for _, c := range list {
		if !updates[c.Name].UpdateAvailable {
			continue
		}
		if c.Self && !s.AutoUpdateAgent || !c.Self && !s.AutoUpdateAll && !chosen[c.Name] {
			continue
		}
		out = append(out, Member{ID: c.ID, Name: c.Name, Self: c.Self, Running: c.State == "running" || c.State == "restarting"})
	}
	return out
}
