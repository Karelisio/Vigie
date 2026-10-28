// Package logwatch suit les logs des conteneurs visés par les alertes sur les logs (réglage log_rules)
// et signale les lignes qui correspondent, avec un délai minimum entre deux alertes d'une même règle.
package logwatch

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
)

const (
	// Cooldown est le délai minimum entre deux alertes d'une même règle ;
	// les occurrences intermédiaires sont comptées et annoncées avec l'alerte suivante.
	Cooldown = 15 * time.Minute

	maxMatchLen = 4 << 10          // seuls les 4 premiers Kio d'une ligne sont analysés
	lineRate    = 500              // lignes analysées par seconde et par conteneur…
	lineBurst   = 5000             // …avec une rafale tolérée ; au-delà, les lignes sont ignorées
	bodyMax     = 300              // caractères de la ligne recopiés dans l'alerte
	checkEvery  = 15 * time.Second // contrôle périodique des suiveurs
)

// Source lit les logs des conteneurs (le client Docker, ou un faux dans les tests).
type Source interface {
	Inspect(ctx context.Context, id string) (*docker.Inspect, error)
	Logs(ctx context.Context, id string, tail int, follow bool, tty bool, fn func(docker.LogLine)) error
}

// Hit est une alerte à envoyer.
type Hit struct {
	RuleID      string
	Container   string
	ContainerID string
	Pattern     string
	Line        string // ligne qui a déclenché l'alerte, tronquée
	Others      int    // occurrences survenues pendant le délai qui a suivi l'alerte précédente
}

// Status décrit une règle et le suivi de son conteneur (GET /api/v1/logwatch).
type Status struct {
	RuleID     string `json:"rule_id"`
	Container  string `json:"container"`
	Active     bool   `json:"active"` // flux de logs en cours de lecture
	Matches24h int    `json:"matches_24h"`
	LastMatch  int64  `json:"last_match"` // ms Unix, 0 si jamais
	LastLine   string `json:"last_line"`
}

type rule struct {
	settings.LogRule
	re    *regexp.Regexp // mode regex
	lower string         // mode texte : motif en minuscules

	// Protégés par Watcher.mu.
	hours     [24]hourCount
	lastMatch int64
	lastLine  string
	lastAlert time.Time
	pending   int
}

type hourCount struct {
	hour int64
	n    int
}

type follower struct {
	id     string
	cancel context.CancelFunc
}

type Watcher struct {
	src        Source
	containers func() []monitor.Container
	settings   func() settings.Settings
	notify     func(Hit)
	log        *slog.Logger
	now        func() time.Time
	wake       chan struct{}

	mu      sync.Mutex
	set     settings.Settings  // réglages au dernier alignement (sourdine)
	rules   map[string]*rule   // ID → règle
	byName  map[string][]*rule // conteneur → ses règles (tranche remplacée, jamais modifiée)
	tails   map[string]*follower
	started map[string]string // conteneur → ID annoncé par un événement « start » récent
	lastErr map[string]string // dernière erreur de lecture signalée, par conteneur
}

func New(src Source, containers func() []monitor.Container, set func() settings.Settings, notify func(Hit), log *slog.Logger) *Watcher {
	return &Watcher{
		src: src, containers: containers, settings: set, notify: notify, log: log,
		now:     time.Now,
		wake:    make(chan struct{}, 1),
		rules:   map[string]*rule{},
		byName:  map[string][]*rule{},
		tails:   map[string]*follower{},
		started: map[string]string{},
		lastErr: map[string]string{},
	}
}

// Run aligne les suiveurs sur les règles jusqu'à l'annulation du contexte :
// au démarrage, à chaque changement signalé (Reload, OnEvent) et périodiquement.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		w.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.wake:
		}
	}
}

// Reload demande un alignement immédiat (réglages modifiés).
func (w *Watcher) Reload() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// OnEvent relance sans attendre le suivi d'un conteneur qui (re)démarre, y compris sous
// un nouvel ID après une mise à jour : la liste du monitor n'est pas encore à jour à ce moment.
func (w *Watcher) OnEvent(ev docker.Event) {
	if ev.Action != "start" && ev.Action != "rename" {
		return
	}
	w.mu.Lock()
	_, watched := w.byName[ev.Name()]
	if watched && ev.Action == "start" {
		w.started[ev.Name()] = ev.Actor.ID
	}
	following := len(w.tails) > 0
	w.mu.Unlock()
	if watched || (ev.Action == "rename" && following) {
		w.Reload()
	}
}

// reconcile démarre un suiveur par conteneur concerné et en marche, et arrête les autres.
func (w *Watcher) reconcile(ctx context.Context) {
	s := w.settings()
	list := w.containers()
	running := map[string]string{} // nom → ID
	names := map[string]string{}   // ID → nom
	for _, c := range list {
		names[c.ID] = c.Name
		if c.State == "running" {
			running[c.Name] = c.ID
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.setRules(s)
	for name, f := range w.tails {
		// Règles supprimées, ou conteneur renommé : ce suivi n'a plus lieu d'être.
		if n, known := names[f.id]; w.byName[name] == nil || (known && n != name) {
			f.cancel()
			delete(w.tails, name)
		}
	}
	for name := range w.byName {
		f := w.tails[name]
		if id, ok := w.started[name]; ok {
			if f == nil || f.id != id {
				w.start(ctx, name, id)
			}
		} else if id, ok := running[name]; ok && f == nil {
			w.start(ctx, name, id)
		}
	}
	clear(w.started)
}

// setRules compile les règles, en gardant les statistiques de celles qui n'ont pas changé.
func (w *Watcher) setRules(s settings.Settings) {
	rules := map[string]*rule{}
	byName := map[string][]*rule{}
	for _, lr := range s.LogRules {
		r := w.rules[lr.ID]
		if r == nil || r.LogRule != lr {
			r = &rule{LogRule: lr}
			if lr.Regex {
				re, err := regexp.Compile(lr.Pattern)
				if err != nil {
					continue // déjà refusée par la validation des réglages
				}
				r.re = re
			} else {
				r.lower = strings.ToLower(lr.Pattern)
			}
		}
		rules[lr.ID] = r
		byName[lr.Container] = append(byName[lr.Container], r)
	}
	w.set, w.rules, w.byName = s, rules, byName
}

func (w *Watcher) start(ctx context.Context, name, id string) {
	if f := w.tails[name]; f != nil {
		f.cancel()
	}
	fctx, cancel := context.WithCancel(ctx)
	f := &follower{id: id, cancel: cancel}
	w.tails[name] = f
	go w.follow(fctx, name, f)
}

// follow lit les nouvelles lignes du conteneur jusqu'à son arrêt ou celui du suivi.
func (w *Watcher) follow(ctx context.Context, name string, f *follower) {
	tty := false
	if in, err := w.src.Inspect(ctx, f.id); err == nil {
		tty = in.Config.Tty
	}
	lim := limiter{tokens: lineBurst, last: w.now()}
	err := w.src.Logs(ctx, f.id, 0, true, tty, func(l docker.LogLine) {
		now := w.now()
		if !lim.take(now) {
			if now.Sub(lim.reported) >= 10*time.Minute {
				w.log.Warn("logs trop abondants : lignes non analysées", "conteneur", name, "ignorées", lim.skipped)
				lim.reported, lim.skipped = now, 0
			}
			return
		}
		w.scan(name, f.id, l)
	})
	f.cancel()

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tails[name] == f {
		delete(w.tails, name)
	}
	// Une erreur persistante (pilote de logs illisible…) n'est signalée qu'une fois.
	if err != nil && ctx.Err() == nil {
		if w.lastErr[name] != err.Error() {
			w.log.Warn("suivi des logs interrompu", "conteneur", name, "erreur", err)
		}
		w.lastErr[name] = err.Error()
	} else if err == nil {
		delete(w.lastErr, name)
	}
}

// scan compare une ligne aux règles du conteneur.
func (w *Watcher) scan(name, id string, l docker.LogLine) {
	w.mu.Lock()
	rules := w.byName[name]
	w.mu.Unlock()
	text := l.Line
	if len(text) > maxMatchLen {
		text = text[:maxMatchLen]
	}
	lowered, done := "", false
	for _, r := range rules {
		var ok bool
		if r.re != nil {
			ok = r.re.MatchString(text)
		} else {
			if !done {
				lowered, done = strings.ToLower(text), true
			}
			ok = strings.Contains(lowered, r.lower)
		}
		if ok {
			w.hit(r, name, id, l)
		}
	}
}

// hit comptabilise une correspondance et déclenche l'alerte si le délai de la règle est écoulé.
func (w *Watcher) hit(r *rule, name, id string, l docker.LogLine) {
	now := w.now()
	w.mu.Lock()
	r.count(now)
	r.lastMatch = l.TS
	if r.lastMatch == 0 {
		r.lastMatch = now.UnixMilli()
	}
	r.lastLine = clip(l.Line, bodyMax)
	if w.set.IsMuted(name) {
		w.mu.Unlock()
		return
	}
	if !r.lastAlert.IsZero() && now.Sub(r.lastAlert) < Cooldown {
		r.pending++
		w.mu.Unlock()
		return
	}
	h := Hit{RuleID: r.ID, Container: name, ContainerID: id, Pattern: r.Pattern, Line: r.lastLine, Others: r.pending}
	r.lastAlert, r.pending = now, 0
	w.mu.Unlock()
	// Envoi hors du flux de lecture : l'enregistrement de l'alerte ne doit pas le ralentir.
	go w.notify(h)
}

// Status renvoie l'état de chaque règle, dans l'ordre des réglages.
func (w *Watcher) Status() []Status {
	s := w.settings()
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Status, 0, len(s.LogRules))
	for _, lr := range s.LogRules {
		st := Status{RuleID: lr.ID, Container: lr.Container, Active: w.tails[lr.Container] != nil}
		if r := w.rules[lr.ID]; r != nil && r.LogRule == lr {
			st.Matches24h, st.LastMatch, st.LastLine = r.total(now), r.lastMatch, r.lastLine
		}
		out = append(out, st)
	}
	return out
}

func (r *rule) count(now time.Time) {
	h := now.Unix() / 3600
	b := &r.hours[h%24]
	if b.hour != h {
		*b = hourCount{hour: h}
	}
	b.n++
}

// total renvoie le nombre de correspondances des dernières 24 heures (à l'heure près).
func (r *rule) total(now time.Time) int {
	h := now.Unix() / 3600
	n := 0
	for _, b := range r.hours {
		if h-b.hour < 24 {
			n += b.n
		}
	}
	return n
}

// limiter borne le nombre de lignes analysées (seau à jetons) : au-delà, les lignes sont
// ignorées plutôt que de ralentir la lecture et de prendre du retard sur le flux.
type limiter struct {
	tokens   float64
	last     time.Time
	skipped  int
	reported time.Time
}

func (l *limiter) take(now time.Time) bool {
	l.tokens = min(lineBurst, l.tokens+now.Sub(l.last).Seconds()*lineRate)
	l.last = now
	if l.tokens < 1 {
		l.skipped++
		return false
	}
	l.tokens--
	return true
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
