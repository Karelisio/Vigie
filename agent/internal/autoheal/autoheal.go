// Package autoheal répare automatiquement les conteneurs choisis dans les réglages : redémarrage
// d'un conteneur dont le healthcheck échoue depuis le délai choisi, relance d'un conteneur arrêté
// anormalement, dans la limite d'un nombre de réparations par heure et par conteneur.
package autoheal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	tick        = 10 * time.Second // examen périodique des conteneurs
	stopGrace   = 20 * time.Second // arrêt confirmé : même délai que l'alerte « est tombé »
	actionGrace = 2 * time.Minute  // action récente (appli, mise à jour, réparation) : pas d'intervention
	limitWindow = time.Hour        // fenêtre glissante de la limite de réparations
	keepFor     = 24 * time.Hour   // historique renvoyé par l'API…
	keepMax     = 50               // …limité aux dernières réparations
	historyKey  = "autoheal_history"
)

// Docker agit sur les conteneurs (le client Docker, ou un faux dans les tests).
type Docker interface {
	Inspect(ctx context.Context, id string) (*docker.Inspect, error)
	Start(ctx context.Context, id string) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
}

// Monitor fournit l'état des conteneurs et le suivi des actions (le monitor, ou un faux dans les tests).
type Monitor interface {
	Containers() []monitor.Container
	NoteAction(name, action string)
	RecentAction(name string, within time.Duration) bool
	IsStoppedByUser(name string) bool
}

// Notice est une notification à transmettre au moteur d'alertes.
type Notice struct {
	Container   string
	ContainerID string
	Severity    string // info (réparation), warning (réparation impossible), critical (abandon)
	Resolved    bool   // retour à la normale après un abandon
	Title       string
	Body        string
}

// Repair est une réparation effectuée (GET /api/v1/autoheal).
type Repair struct {
	TS        int64  `json:"ts"`
	Container string `json:"container"`
	Reason    string `json:"reason"` // unhealthy, down ou oom
	Action    string `json:"action"` // restart ou start
	OK        bool   `json:"ok"`     // conteneur revenu : en marche, et en bonne santé s'il a un healthcheck
	Error     string `json:"error,omitempty"`
}

// job est une réparation décidée, pas encore effectuée.
type job struct {
	c      monitor.Container
	reason string
	action string
	since  time.Time // début de la mauvaise santé (unhealthy)
}

// Healer surveille les conteneurs concernés et les répare.
type Healer struct {
	dk       Docker
	mon      Monitor
	st       *store.Store
	settings func() settings.Settings
	notify   func(Notice)
	log      *slog.Logger
	now      func() time.Time

	// Vérification après réparation : intervalle, durée maximale, et durée de marche
	// sans redémarrage qui suffit pour un conteneur sans healthcheck.
	verifyEvery, verifyFor, stableFor time.Duration

	mu        sync.Mutex
	unhealthy map[string]time.Time   // conteneur → début de la mauvaise santé
	repairs   map[string][]time.Time // conteneur → réparations de la dernière heure
	busy      map[string]bool        // réparation en cours, jusqu'à la fin de la vérification
	abandoned map[string]bool        // abandon signalé, jusqu'au retour à la normale
	history   []Repair               // de la plus récente à la plus ancienne

	saveMu sync.Mutex // ordonne les enregistrements de l'historique
}

func New(dk Docker, mon Monitor, st *store.Store, set func() settings.Settings, notify func(Notice), log *slog.Logger) *Healer {
	h := &Healer{
		dk: dk, mon: mon, st: st, settings: set, notify: notify, log: log,
		now:         time.Now,
		verifyEvery: 5 * time.Second,
		verifyFor:   3 * time.Minute,
		stableFor:   30 * time.Second,
		unhealthy:   map[string]time.Time{},
		repairs:     map[string][]time.Time{},
		busy:        map[string]bool{},
		abandoned:   map[string]bool{},
	}
	// L'historique survit aux redémarrages de l'agent, et avec lui la limite horaire.
	_ = json.Unmarshal([]byte(st.Get(context.Background(), historyKey)), &h.history)
	for _, r := range h.history {
		if t := time.UnixMilli(r.TS); h.now().Sub(t) < limitWindow {
			h.repairs[r.Container] = append(h.repairs[r.Container], t)
		}
	}
	return h
}

// Run examine les conteneurs périodiquement jusqu'à l'annulation du contexte.
func (h *Healer) Run(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.scan(ctx)
		}
	}
}

// OnEvent date le passage en mauvaise santé : Docker ne signale que les changements d'état de santé.
func (h *Healer) OnEvent(ev docker.Event) {
	name := ev.Name()
	if name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case strings.HasPrefix(ev.Action, "health_status"):
		if strings.TrimSpace(strings.TrimPrefix(ev.Action, "health_status:")) == "unhealthy" {
			h.unhealthy[name] = h.eventTime(ev)
		} else {
			delete(h.unhealthy, name)
		}
	case ev.Action == "start", ev.Action == "die", ev.Action == "destroy":
		delete(h.unhealthy, name)
	}
}

func (h *Healer) eventTime(ev docker.Event) time.Time {
	switch {
	case ev.TimeNano > 0:
		return time.Unix(0, ev.TimeNano)
	case ev.Time > 0:
		return time.Unix(ev.Time, 0)
	}
	return h.now()
}

// concerned indique si la réparation automatique s'applique au conteneur (jamais l'agent ni un conteneur ignoré).
func concerned(s settings.Settings, c monitor.Container) bool {
	return s.AutoHeal && !c.Self && !c.Ignored && (s.AutoHealAll || slices.Contains(s.AutoHealList, c.Name))
}

// normal indique qu'un conteneur fonctionne : en marche, et en bonne santé s'il a un healthcheck.
func normal(c monitor.Container) bool {
	return c.State == "running" && (c.Health == "" || c.Health == "healthy")
}

// scan examine les conteneurs concernés et lance les réparations dues.
func (h *Healer) scan(ctx context.Context) {
	s := h.settings()
	list := h.mon.Containers()
	now := h.now()
	delay := time.Duration(s.AutoHealDelay) * time.Minute
	var due []job
	var notices []Notice

	h.mu.Lock()
	present := map[string]bool{}
	for _, c := range list {
		present[c.Name] = true
		sick := c.State == "running" && c.Health == "unhealthy"
		since, timed := h.unhealthy[c.Name]
		switch {
		case sick && !timed:
			// Événement manqué ou agent redémarré : le délai part de maintenant.
			since = now
			h.unhealthy[c.Name] = now
		case !sick && timed && now.Sub(since) >= tick:
			// La liste peut retarder de quelques secondes sur l'événement : seul un état ancien est effacé.
			delete(h.unhealthy, c.Name)
		}
		if !concerned(s, c) {
			delete(h.abandoned, c.Name)
			continue
		}
		if h.busy[c.Name] {
			continue
		}
		var j job
		switch {
		case sick && now.Sub(since) >= delay:
			j = job{c: c, reason: "unhealthy", action: "restart", since: since}
		case (c.Problem == "down" || c.Problem == "oom") && now.Sub(time.UnixMilli(c.FinishedAt)) >= stopGrace:
			j = job{c: c, reason: c.Problem, action: "start"}
		default:
			if h.abandoned[c.Name] && normal(c) {
				delete(h.abandoned, c.Name)
				notices = append(notices, Notice{
					Container: c.Name, ContainerID: c.ID, Severity: "info", Resolved: true,
					Title: c.Name + " fonctionne de nouveau",
					Body:  "La réparation automatique reprend.",
				})
			}
			continue
		}
		// Maintenance : l'utilisateur intervient sur le serveur, on ne touche à rien.
		if h.abandoned[c.Name] || s.InMaintenance(now) || h.mon.IsStoppedByUser(c.Name) || h.mon.RecentAction(c.Name, actionGrace) {
			continue
		}
		if h.count(c.Name, now) >= s.AutoHealMax {
			h.abandoned[c.Name] = true
			notices = append(notices, Notice{
				Container: c.Name, ContainerID: c.ID, Severity: "critical",
				Title: "Réparation automatique abandonnée : " + c.Name,
				Body: fmt.Sprintf("%d réparation(s) dans l'heure sans effet durable. "+
					"Pas de nouvel essai avant son retour à la normale : regarde ses logs.", s.AutoHealMax),
			})
			continue
		}
		h.busy[c.Name] = true
		due = append(due, j)
	}
	for name := range h.unhealthy {
		if !present[name] {
			delete(h.unhealthy, name)
		}
	}
	for name := range h.abandoned {
		if !present[name] {
			delete(h.abandoned, name)
		}
	}
	h.mu.Unlock()

	for _, n := range notices {
		h.notify(n)
	}
	for _, j := range due {
		go h.repair(ctx, j)
	}
}

// count renvoie le nombre de réparations du conteneur dans l'heure écoulée (appelé sous verrou).
func (h *Healer) count(name string, now time.Time) int {
	var kept []time.Time
	for _, t := range h.repairs[name] {
		if now.Sub(t) < limitWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(h.repairs, name)
	} else {
		h.repairs[name] = kept
	}
	return len(kept)
}

// repair effectue la réparation, la consigne (journal, chronologie, notification), puis vérifie
// que le conteneur est revenu. Sinon, il sera de nouveau traité au prochain examen, dans la limite.
func (h *Healer) repair(ctx context.Context, j job) {
	name, id := j.c.Name, j.c.ID
	defer func() {
		h.mu.Lock()
		delete(h.busy, name)
		h.mu.Unlock()
	}()
	// L'état a pu changer depuis la collecte : Docker a le dernier mot.
	in, err := h.dk.Inspect(ctx, id)
	if err != nil || !stillBroken(j.action, in) {
		return
	}
	at := h.now()
	h.mu.Lock()
	h.repairs[name] = append(h.repairs[name], at)
	h.mu.Unlock()

	h.mon.NoteAction(name, j.action)
	actx, cancel := context.WithTimeout(ctx, 45*time.Second)
	if j.action == "restart" {
		err = h.dk.Restart(actx, id, 15*time.Second)
	} else {
		err = h.dk.Start(actx, id)
	}
	cancel()
	r := Repair{TS: at.UnixMilli(), Container: name, Reason: j.reason, Action: j.action}
	if err != nil {
		h.log.Warn("réparation automatique impossible", "conteneur", name, "action", j.action, "erreur", err)
		h.st.Audit(ctx, "agent", "autoheal", name, "échec : "+err.Error(), "vigie")
		h.notify(Notice{
			Container: name, ContainerID: id, Severity: "warning",
			Title: "Réparation automatique de " + name + " impossible",
			Body:  err.Error(),
		})
		r.Error = err.Error()
		h.record(r)
		return
	}
	h.log.Info("réparation automatique", "conteneur", name, "action", j.action, "raison", j.reason)
	h.st.Audit(ctx, "agent", "autoheal", name, "ok", "vigie")
	h.st.AddEvent(ctx, name, "autoheal", j.reason, r.TS)
	verb := "relancé"
	if j.action == "restart" {
		verb = "redémarré"
	}
	h.notify(Notice{
		Container: name, ContainerID: id, Severity: "info",
		Title: "Réparation automatique de " + name,
		Body:  fmt.Sprintf("%s %s : %s automatiquement.", name, j.why(at), verb),
	})

	if err := h.verify(ctx, id); err != nil {
		h.log.Warn("réparation automatique sans effet", "conteneur", name, "erreur", err)
		r.Error = err.Error()
	} else {
		r.OK = true
	}
	h.record(r)
}

// stillBroken confirme auprès de Docker que la réparation est toujours nécessaire.
func stillBroken(action string, in *docker.Inspect) bool {
	if action == "restart" {
		return in.State.Running && in.State.Health != nil && in.State.Health.Status == "unhealthy"
	}
	return !in.State.Running && !in.State.Restarting
}

// why décrit le problème réparé, pour la notification.
func (j job) why(at time.Time) string {
	switch j.reason {
	case "unhealthy":
		return "était en mauvaise santé depuis " + minutes(at.Sub(j.since))
	case "oom":
		return "avait été tué par manque de mémoire"
	}
	return fmt.Sprintf("s'était arrêté (code %d)", j.c.ExitCode)
}

func minutes(d time.Duration) string {
	m := max(1, int(d.Round(time.Minute).Minutes()))
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %02d", m/60, m%60)
}

// verify attend le retour du conteneur : en bonne santé s'il a un healthcheck, sinon en marche
// sans redémarrage pendant stableFor.
func (h *Healer) verify(ctx context.Context, id string) error {
	deadline := time.Now().Add(h.verifyFor)
	var started string // StartedAt observé : il change si le conteneur redémarre entre-temps
	var since time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.verifyEvery):
		}
		in, err := h.dk.Inspect(ctx, id)
		if err != nil {
			return err
		}
		st := in.State
		switch {
		case st.Restarting:
			// Relancé par sa politique de redémarrage : on laisse une chance jusqu'à l'échéance.
		case !st.Running && st.OOMKilled:
			return errors.New("de nouveau tué par manque de mémoire")
		case !st.Running:
			return fmt.Errorf("de nouveau arrêté (code %d)", st.ExitCode)
		case st.Health == nil:
			if st.StartedAt != started || since.IsZero() {
				started, since = st.StartedAt, time.Now()
			}
			if time.Since(since) >= h.stableFor {
				return nil
			}
		case st.Health.Status == "healthy":
			return nil
		case st.Health.Status == "unhealthy":
			return errors.New("toujours en mauvaise santé")
		}
		if time.Now().After(deadline) {
			if st.Health != nil && !st.Restarting {
				return errors.New("healthcheck toujours en attente")
			}
			return errors.New("redémarre en boucle")
		}
	}
}

// record ajoute une réparation à l'historique et l'enregistre en base.
func (h *Healer) record(r Repair) {
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	h.mu.Lock()
	cut := h.now().Add(-keepFor).UnixMilli()
	list := []Repair{r}
	for _, old := range h.history {
		if old.TS >= cut {
			list = append(list, old)
		}
	}
	// Les vérifications n'ont pas toutes la même durée : l'ordre suit la date de la réparation.
	sort.SliceStable(list, func(a, b int) bool { return list[a].TS > list[b].TS })
	h.history = list[:min(len(list), keepMax)]
	b, _ := json.Marshal(h.history)
	h.mu.Unlock()
	if err := h.st.Set(context.Background(), historyKey, string(b)); err != nil {
		h.log.Warn("historique des réparations", "erreur", err)
	}
}

// History renvoie les réparations des dernières 24 heures, de la plus récente à la plus ancienne.
func (h *Healer) History() []Repair {
	cut := h.now().Add(-keepFor).UnixMilli()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Repair{}
	for _, r := range h.history {
		if r.TS >= cut {
			out = append(out, r)
		}
	}
	return out
}
