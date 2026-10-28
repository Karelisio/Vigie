// Package ports liste les ports en écoute sur l'hôte (sockets de son espace réseau, ports publiés par
// Docker), les rattache aux conteneurs, repère ceux qui contournent Pangolin et signale l'apparition
// d'un nouveau port ouvert sur Internet.
package ports

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

// debounce regroupe les événements Docker rapprochés et laisse au proxy de Docker le temps d'ouvrir ses sockets.
var debounce = 5 * time.Second

const (
	Interval   = 5 * time.Minute  // analyse périodique
	firstDelay = 20 * time.Second // première analyse après le démarrage (Docker et les conteneurs se lancent)
	// settle : une socket d'un processus de l'hôte doit rester ouverte ce temps avant d'être signalée
	// (sockets UDP éphémères, serveurs lancés un instant).
	settle = 4 * time.Minute
	// closeGrace : un port signalé n'est considéré fermé qu'après cette absence (redémarrage d'un conteneur).
	closeGrace  = 10 * time.Minute
	scanTimeout = 30 * time.Second
	maxState    = 5000
	stateKey    = "ports_state"
)

// Docker fournit les conteneurs et réseaux (le client Docker, ou un faux dans les tests).
type Docker interface {
	RunningPorts(ctx context.Context) ([]docker.PortContainer, error)
	ExposedPorts(ctx context.Context, id string) ([]string, error)
	Networks(ctx context.Context) ([]docker.Network, error)
}

// Report est la réponse de GET /api/v1/ports.
type Report struct {
	TS       int64    `json:"ts"` // date de l'analyse (ms), 0 avant la première
	Ports    []Port   `json:"ports"`
	HostIP   string   `json:"host_ip,omitempty"` // IP de l'interface principale de l'hôte (IP privée de la VNIC chez Oracle)
	Pangolin []string `json:"pangolin"`          // conteneurs de la pile Pangolin repérés
	Docker   bool     `json:"docker"`            // conteneurs lus : rattachement aux conteneurs possible
	Error    string   `json:"error,omitempty"`
}

// Change signale un nouveau port public (Open) ou la fermeture d'un port signalé.
type Change struct {
	Port Port
	Open bool
}

type alerted struct {
	Container string `json:"container,omitempty"`
	Process   string `json:"process,omitempty"`
}

// state est mémorisé en base : il survit aux redémarrages de l'agent.
type state struct {
	Baseline bool             `json:"baseline"` // premier passage fait : les ports alors ouverts forment la base
	Seen     map[string]int64 `json:"seen"`     // ligne (proto/port/portée) → première détection (ms)
	Gone     map[string]int64 `json:"gone"`     // ligne → première analyse qui ne l'a plus vue (ms) ; l'absence est
	// mesurée par l'agent lui-même : après un arrêt du serveur, les ports qui redémarrent ne sont pas « fermés ».
	Known   map[string]bool    `json:"known"`   // port public (port/proto) déjà évalué : base, autorisé, Pangolin ou signalé
	Alerted map[string]alerted `json:"alerted"` // port public signalé, alerte en cours
}

type Scanner struct {
	proc   string
	dk     Docker
	st     *store.Store
	set    func() settings.Settings
	notify func(Change)
	log    *slog.Logger
	now    func() time.Time

	kick    chan struct{}
	scanMu  sync.Mutex // une analyse à la fois
	mu      sync.Mutex
	last    Report
	exposed map[string][]string // ports déclarés des conteneurs en réseau hôte, par ID
}

func New(proc string, dk Docker, st *store.Store, set func() settings.Settings, notify func(Change), log *slog.Logger) *Scanner {
	return &Scanner{
		proc: proc, dk: dk, st: st, set: set, notify: notify, log: log, now: time.Now,
		kick: make(chan struct{}, 1), exposed: map[string][]string{},
		last: Report{Ports: []Port{}, Pangolin: []string{}},
	}
}

// Kick demande une nouvelle analyse (événement Docker, réglages modifiés).
func (s *Scanner) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// OnEvent relance l'analyse quand un conteneur démarre, s'arrête ou disparaît.
func (s *Scanner) OnEvent(ev docker.Event) {
	switch ev.Action {
	case "start", "die", "stop", "destroy", "restart", "unpause", "pause":
		s.Kick()
	}
}

// Run analyse les ports toutes les 5 minutes et après les événements Docker utiles.
func (s *Scanner) Run(ctx context.Context) {
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			// Laisse le temps au proxy de Docker d'ouvrir ou fermer ses sockets, et regroupe les événements.
			select {
			case <-ctx.Done():
				return
			case <-time.After(debounce):
			}
			select {
			case <-s.kick:
			default:
			}
		case <-timer.C:
		}
		s.Scan(ctx)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(Interval)
	}
}

// Report renvoie la dernière analyse.
func (s *Scanner) Report() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Scan analyse les ports maintenant, évalue les alertes et renvoie le résultat.
func (s *Scanner) Scan(ctx context.Context) Report {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	now := s.now()
	set := s.set()
	r := Report{TS: now.UnixMilli(), Ports: []Port{}, Pangolin: []string{}}

	dir := netDir(s.proc)
	socks, err := readSockets(dir)
	if err != nil {
		r.Error = "Lecture des sockets de l'hôte impossible (" + err.Error() + "). Vérifie le montage « /proc:/host/proc:ro »."
		s.log.Warn("ports ouverts", "erreur", err)
	}
	ctrs, nets, derr := s.containers(ctx)
	if derr != nil {
		if r.Error == "" {
			r.Error = "Docker injoignable : rattachement aux conteneurs impossible pour l'instant."
		}
		s.log.Warn("ports ouverts : conteneurs", "erreur", derr)
	}
	r.Docker = derr == nil
	hn := readHostNet(dir, nets)
	if hn.primary.IsValid() {
		r.HostIP = hn.primary.String()
	}
	for _, c := range ctrs {
		if c.Pangolin {
			r.Pangolin = append(r.Pangolin, c.Name)
		}
	}
	sort.Strings(r.Pangolin)

	allow := map[string]bool{}
	for _, k := range set.PortAllow {
		allow[k] = true
	}
	r.Ports = build(socks, ctrs, hn, allow)

	st := s.loadState(ctx)
	var changes []Change
	// Sans sockets ni conteneurs fiables, pas d'évaluation : les dates de détection restent valables.
	if err == nil && derr == nil {
		changes = evaluate(&st, r.Ports, set, now)
		s.saveState(ctx, st)
	}
	for i := range r.Ports {
		p := &r.Ports[i]
		if t := st.Seen[p.stateKey()]; t > 0 {
			p.Since = t
		} else {
			p.Since = r.TS
		}
		_, p.Alerted = st.Alerted[p.Key()]
		if p.Scope != "public" {
			p.Alerted = false
		}
	}
	s.mu.Lock()
	s.last = r
	s.mu.Unlock()
	for _, c := range changes {
		if s.notify != nil {
			s.notify(c)
		}
	}
	return r
}

// containers lit les conteneurs en marche (ports publiés, réseau hôte) et les réseaux Docker.
func (s *Scanner) containers(ctx context.Context) ([]ctrInfo, []docker.Network, error) {
	list, err := s.dk.RunningPorts(ctx)
	if err != nil {
		return nil, nil, err
	}
	nets, err := s.dk.Networks(ctx)
	if err != nil {
		nets = nil // facultatif : les bridges sont aussi repérés par leur nom d'interface
	}
	out := make([]ctrInfo, 0, len(list))
	alive := map[string]bool{}
	for _, c := range list {
		info := ctrInfo{
			ID: c.ID, Name: c.Name(), Published: c.Ports,
			Pangolin:    isPangolin(c.Name(), c.Image),
			HostNetwork: c.HostConfig.NetworkMode == "host",
		}
		if info.HostNetwork {
			alive[c.ID] = true
			s.mu.Lock()
			exp, ok := s.exposed[c.ID]
			s.mu.Unlock()
			if !ok {
				if exp, err = s.dk.ExposedPorts(ctx, c.ID); err == nil {
					s.mu.Lock()
					s.exposed[c.ID] = exp
					s.mu.Unlock()
				}
			}
			info.Exposed = map[string]bool{}
			for _, p := range exp {
				info.Exposed[strings.ToLower(p)] = true
			}
		}
		out = append(out, info)
	}
	s.mu.Lock()
	for id := range s.exposed {
		if !alive[id] {
			delete(s.exposed, id)
		}
	}
	s.mu.Unlock()
	return out, nets, nil
}

// evaluate met à jour l'état et renvoie les alertes à envoyer : nouveau port public non autorisé et hors
// Pangolin (pas au premier passage, qui fixe la base), fermeture d'un port signalé.
func evaluate(st *state, list []Port, set settings.Settings, now time.Time) []Change {
	ms := now.UnixMilli()
	present := map[string]bool{}
	for _, p := range list {
		k := p.stateKey()
		present[k] = true
		if st.Seen[k] == 0 {
			st.Seen[k] = ms
		}
		delete(st.Gone, k)
	}
	// Absence constatée par l'agent : elle commence à la première analyse qui ne voit plus la ligne.
	gone := func(line string) bool {
		if present[line] {
			return false
		}
		if st.Gone[line] == 0 {
			st.Gone[line] = ms
		}
		return now.Sub(time.UnixMilli(st.Gone[line])) >= closeGrace
	}
	var changes []Change
	if !st.Baseline {
		st.Baseline = true
		for _, p := range list {
			if p.Scope == "public" {
				st.Known[p.Key()] = true
			}
		}
	} else {
		for _, p := range list {
			key := p.Key()
			if p.Scope != "public" {
				continue
			}
			if p.Allowed {
				delete(st.Alerted, key) // autorisé après coup : l'alerte en cours est oubliée sans bruit
			}
			if st.Known[key] {
				continue
			}
			if p.Allowed || p.Pangolin {
				st.Known[key] = true
				continue
			}
			if p.Container == "" && now.Sub(time.UnixMilli(st.Seen[p.stateKey()])) < settle {
				continue // à confirmer au prochain passage
			}
			st.Known[key] = true
			if set.PortAlert {
				st.Alerted[key] = alerted{Container: p.Container, Process: p.Process}
				changes = append(changes, Change{Port: p, Open: true})
			}
		}
	}
	// Ports publics disparus depuis un moment : fermés (alerte résolue).
	for key := range st.Known {
		port, proto, _ := strings.Cut(key, "/")
		if !gone(proto + "/" + port + "/public") {
			continue
		}
		delete(st.Known, key)
		if a, ok := st.Alerted[key]; ok {
			delete(st.Alerted, key)
			p := Port{Proto: proto, Scope: "public", Container: a.Container, Process: a.Process}
			p.Port, _ = parsePort(port)
			changes = append(changes, Change{Port: p, Open: false})
		}
	}
	// Lignes absentes depuis longtemps : oubliées (la date de détection repart de zéro).
	for k := range st.Seen {
		if gone(k) {
			delete(st.Seen, k)
		}
	}
	for k, t := range st.Gone {
		if now.Sub(time.UnixMilli(t)) >= closeGrace {
			delete(st.Gone, k) // déjà traitée ci-dessus
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Port.Port < changes[j].Port.Port })
	return changes
}

func parsePort(s string) (int, bool) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0 && n <= 65535
}

func newState() state {
	return state{Seen: map[string]int64{}, Gone: map[string]int64{}, Known: map[string]bool{}, Alerted: map[string]alerted{}}
}

func (s *Scanner) loadState(ctx context.Context) state {
	st := newState()
	if raw := s.st.Get(ctx, stateKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	if st.Seen == nil {
		st.Seen = map[string]int64{}
	}
	if st.Gone == nil {
		st.Gone = map[string]int64{}
	}
	if st.Known == nil {
		st.Known = map[string]bool{}
	}
	if st.Alerted == nil {
		st.Alerted = map[string]alerted{}
	}
	return st
}

func (s *Scanner) saveState(ctx context.Context, st state) {
	if len(st.Seen) > maxState || len(st.Known) > maxState {
		// Garde-fou : un état démesuré est repris de zéro (sans alerte en rafale).
		st = newState()
		st.Baseline = false
	}
	raw, _ := json.Marshal(st)
	if err := s.st.Set(ctx, stateKey, string(raw)); err != nil {
		s.log.Warn("ports ouverts : enregistrement", "erreur", err)
	}
}
