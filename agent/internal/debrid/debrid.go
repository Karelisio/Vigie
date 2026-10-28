// Package debrid vérifie les comptes debrid (Real-Debrid, AllDebrid, TorBox, Premiumize) :
// clé acceptée, premium actif et date d'expiration, et suit l'état de chaque service (en ligne,
// ralenti, en panne). Les clés restent sur l'agent.
package debrid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/secret"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	RealDebrid = "realdebrid"
	AllDebrid  = "alldebrid"
	TorBox     = "torbox"
	Premiumize = "premiumize"

	accountsKey  = "debrid_accounts"
	servicesKey  = "debrid_services"
	interval     = 5 * time.Minute
	confirmAfter = time.Minute // nouvel essai rapproché après un premier échec
	checkTimeout = 15 * time.Second
	slowAfter    = 5 * time.Second // au-delà, le service est considéré comme ralenti
	maxAccounts  = 10
	historySpan  = 24 * time.Hour
	incidentSpan = 30 * 24 * time.Hour
	maxIncidents = 20
)

// États d'un service debrid.
const (
	Up       = "up"
	Degraded = "degraded"
	Down     = "down"
)

// providers : identifiant → nom affiché.
var providers = map[string]string{
	RealDebrid: "Real-Debrid",
	AllDebrid:  "AllDebrid",
	TorBox:     "TorBox",
	Premiumize: "Premiumize",
}

// Account est un compte enregistré. Reçu de l'appli : sans ID connu, c'est un nouveau compte ;
// avec une clé vide ou masquée, la clé actuelle est conservée.
type Account struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Key      string `json:"key"`
}

// Status est l'état d'un compte : sa configuration (clé masquée) et le résultat de la dernière vérification.
type Status struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Key       string `json:"key"`
	OK        bool   `json:"ok"` // clé acceptée et réponse comprise
	Premium   bool   `json:"premium"`
	Expires   int64  `json:"expires"`   // fin du premium (ms Unix), 0 si inconnue
	DaysLeft  int    `json:"days_left"` // jours entiers de premium restants
	Username  string `json:"username,omitempty"`
	Error     string `json:"error,omitempty"`
	Refused   bool   `json:"refused"`    // clé refusée par le fournisseur
	CheckedAt int64  `json:"checked_at"` // 0 : pas encore vérifié

	service string        // état du service vu par cette vérification
	latency time.Duration // durée de la requête
}

// Name renvoie le nom du compte : le fournisseur, suivi du libellé s'il en diffère.
func (s Status) Name() string {
	name := providers[s.Provider]
	if s.Label != "" && s.Label != name {
		name += " (" + s.Label + ")"
	}
	return name
}

// Point est le résultat d'une vérification d'un service.
type Point struct {
	TS        int64  `json:"ts"`
	State     string `json:"s"`
	LatencyMS int64  `json:"ms"`
}

// Incident est une panne d'un service (End à 0 : en cours).
type Incident struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Cause string `json:"cause"`
}

// Service est l'état d'un fournisseur, déduit des vérifications de ses comptes.
type Service struct {
	Provider  string     `json:"provider"`
	Name      string     `json:"name"`
	State     string     `json:"state"`            // up, degraded ou down
	Detail    string     `json:"detail,omitempty"` // cause d'une panne ou d'un ralentissement
	LatencyMS int64      `json:"latency_ms"`
	Since     int64      `json:"since"` // début de l'état actuel (ms Unix)
	CheckedAt int64      `json:"checked_at"`
	Failures  int        `json:"failures"`   // vérifications en panne d'affilée
	Uptime    float64    `json:"uptime_24h"` // % de vérifications sans panne sur 24 h, -1 sans données
	History   []Point    `json:"history"`    // 24 h
	Incidents []Incident `json:"incidents"`  // 30 jours, la plus récente en dernier
}

// Confirmed indique une panne vérifiée deux fois d'affilée (une panne isolée ne déclenche pas d'alerte).
func (s Service) Confirmed() bool { return s.State == Down && s.Failures >= 2 }

// State est l'état de tous les comptes et des services correspondants.
type State struct {
	Accounts []Status  `json:"accounts"`
	Services []Service `json:"services"`
}

type Manager struct {
	st    *store.Store
	log   *slog.Logger
	http  *http.Client
	bases map[string]string // URL de base de l'API par fournisseur (remplacées dans les tests)

	// OnCheck reçoit l'état des comptes après chaque vérification (alertes d'expiration).
	OnCheck func([]Status)
	// OnService reçoit l'état des services après chaque vérification (alertes de panne).
	OnService func([]Service)

	run      sync.Mutex // une vérification ou une modification à la fois
	mu       sync.Mutex
	accounts []Account
	results  map[string]Status   // par ID de compte
	services map[string]*Service // par fournisseur
	now      func() time.Time
}

func New(ctx context.Context, st *store.Store, log *slog.Logger) *Manager {
	m := &Manager{
		st: st, log: log,
		http: &http.Client{},
		bases: map[string]string{
			RealDebrid: "https://api.real-debrid.com/rest/1.0",
			AllDebrid:  "https://api.alldebrid.com/v4",
			TorBox:     "https://api.torbox.app/v1/api",
			Premiumize: "https://www.premiumize.me/api",
		},
		accounts: []Account{},
		results:  map[string]Status{},
		services: map[string]*Service{},
		now:      time.Now,
	}
	if raw := st.Get(ctx, accountsKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m.accounts); err != nil {
			log.Warn("comptes debrid illisibles", "erreur", err)
			m.accounts = []Account{}
		}
	}
	if raw := st.Get(ctx, servicesKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m.services); err != nil || m.services == nil {
			m.services = map[string]*Service{}
		}
	}
	return m
}

// Run vérifie les comptes peu après le démarrage, puis toutes les 5 minutes ; une minute plus tard
// après un premier échec, pour confirmer vite une panne.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next := interval
		for _, s := range m.Check(ctx).Services {
			if s.State == Down && s.Failures == 1 {
				next = confirmAfter
			}
		}
		t.Reset(next)
	}
}

// State renvoie l'état des comptes, clés masquées.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := State{Accounts: make([]Status, 0, len(m.accounts))}
	for _, a := range m.accounts {
		s := m.results[a.ID]
		s.ID, s.Provider, s.Label, s.Key = a.ID, a.Provider, a.Label, secret.Mask(a.Key)
		if left := time.UnixMilli(s.Expires).Sub(now); s.Premium && left > 0 {
			s.DaysLeft = int(left.Hours() / 24)
		}
		out.Accounts = append(out.Accounts, s)
	}
	out.Services = make([]Service, 0, len(m.services))
	for _, p := range []string{RealDebrid, AllDebrid, TorBox, Premiumize} {
		if svc, ok := m.services[p]; ok {
			out.Services = append(out.Services, svc.snapshot(now))
		}
	}
	return out
}

// snapshot copie l'état du service, avec son nom et sa disponibilité sur 24 h.
func (s *Service) snapshot(now time.Time) Service {
	cp := *s
	cp.Name = providers[s.Provider]
	cp.History = append([]Point{}, s.History...)
	cp.Incidents = append([]Incident{}, s.Incidents...)
	cp.Uptime = -1
	n, ok := 0, 0
	for _, p := range s.History {
		if now.Sub(time.UnixMilli(p.TS)) <= historySpan {
			n++
			if p.State != Down {
				ok++
			}
		}
	}
	if n > 0 {
		cp.Uptime = float64(ok) * 100 / float64(n)
	}
	return cp
}

// Update remplace la liste des comptes puis les vérifie.
func (m *Manager) Update(ctx context.Context, in []Account) (State, error) {
	m.run.Lock()
	err := m.replace(ctx, in)
	m.run.Unlock()
	if err != nil {
		return State{}, err
	}
	return m.Check(ctx), nil
}

func (m *Manager) replace(ctx context.Context, in []Account) error {
	if len(in) > maxAccounts {
		return fmt.Errorf("%d comptes au plus", maxAccounts)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := map[string]Account{}
	for _, a := range m.accounts {
		old[a.ID] = a
	}
	next := make([]Account, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		a.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
		name, ok := providers[a.Provider]
		if !ok {
			return fmt.Errorf("fournisseur inconnu %q (realdebrid, alldebrid, torbox, premiumize)", a.Provider)
		}
		if a.Label = strings.TrimSpace(a.Label); a.Label == "" {
			a.Label = name
		}
		if utf8.RuneCountInString(a.Label) > 40 {
			return errors.New("nom de compte trop long (40 caractères au plus)")
		}
		prev, known := old[a.ID]
		if !known || seen[a.ID] || prev.Provider != a.Provider {
			// Nouveau compte (ou fournisseur changé) : une clé est obligatoire.
			a.ID, prev = newID(), Account{}
		}
		seen[a.ID] = true
		a.Key = secret.Merge(a.Key, prev.Key)
		switch {
		case a.Key == "":
			return fmt.Errorf("clé API manquante pour %s", a.Label)
		case len(a.Key) < 8 || len(a.Key) > 200 || strings.ContainsAny(a.Key, " \t\r\n"):
			return fmt.Errorf("clé API invalide pour %s", a.Label)
		}
		next = append(next, a)
	}
	raw, _ := json.Marshal(next)
	if err := m.st.Set(ctx, accountsKey, string(raw)); err != nil {
		return err
	}
	// Résultats des comptes supprimés ou dont la clé a changé : à refaire.
	results := map[string]Status{}
	for _, a := range next {
		if r, ok := m.results[a.ID]; ok && old[a.ID].Key == a.Key {
			results[a.ID] = r
		}
	}
	m.accounts, m.results = next, results
	return nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Check vérifie tous les comptes en parallèle et renvoie le nouvel état.
func (m *Manager) Check(ctx context.Context) State {
	m.run.Lock()
	defer m.run.Unlock()
	m.mu.Lock()
	accounts := append([]Account(nil), m.accounts...)
	m.mu.Unlock()
	results := make([]Status, len(accounts))
	var wg sync.WaitGroup
	for i, a := range accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = m.check(ctx, a)
		}()
	}
	wg.Wait()
	m.mu.Lock()
	for _, r := range results {
		m.results[r.ID] = r
	}
	changed := m.observe(accounts, results)
	raw, _ := json.Marshal(m.services)
	m.mu.Unlock()
	if err := m.st.Set(context.WithoutCancel(ctx), servicesKey, string(raw)); err != nil {
		m.log.Warn("état des services debrid", "erreur", err)
	}
	for _, s := range changed {
		m.log.Info("service debrid", "service", providers[s.Provider], "état", s.State, "détail", s.Detail)
	}
	st := m.State()
	if m.OnCheck != nil {
		m.OnCheck(st.Accounts)
	}
	if m.OnService != nil {
		m.OnService(st.Services)
	}
	return st
}

// observe met à jour l'état de chaque fournisseur à partir des vérifications de ses comptes : en ligne
// si l'un d'eux a répondu (même pour refuser une clé), en panne si aucun n'a pu le joindre.
// Renvoie les services dont l'état a changé. Appelé sous m.mu.
func (m *Manager) observe(accounts []Account, results []Status) []Service {
	now := m.now()
	probes := map[string][]Status{}
	for i, a := range accounts {
		probes[a.Provider] = append(probes[a.Provider], results[i])
	}
	for p := range m.services {
		if _, ok := probes[p]; !ok {
			delete(m.services, p) // plus aucun compte chez ce fournisseur
		}
	}
	var changed []Service
	for p, list := range probes {
		state, detail, latency := summarize(list)
		svc, ok := m.services[p]
		if !ok {
			svc = &Service{Provider: p, Since: now.UnixMilli()}
			m.services[p] = svc
		}
		if svc.State != state {
			if state == Down {
				svc.Incidents = append(svc.Incidents, Incident{Start: now.UnixMilli(), Cause: detail})
			} else if svc.State == Down && len(svc.Incidents) > 0 {
				svc.Incidents[len(svc.Incidents)-1].End = now.UnixMilli()
			}
			if svc.State != "" {
				svc.Since = now.UnixMilli()
			}
			svc.State = state
			changed = append(changed, *svc)
		}
		if state == Down {
			svc.Failures++
		} else {
			svc.Failures = 0
		}
		svc.Detail, svc.LatencyMS, svc.CheckedAt = detail, latency.Milliseconds(), now.UnixMilli()
		svc.History = append(svc.History, Point{TS: now.UnixMilli(), State: state, LatencyMS: latency.Milliseconds()})
		for len(svc.History) > 0 && now.Sub(time.UnixMilli(svc.History[0].TS)) > historySpan {
			svc.History = svc.History[1:]
		}
		kept := svc.Incidents[:0]
		for _, in := range svc.Incidents {
			if in.End == 0 || now.Sub(time.UnixMilli(in.End)) <= incidentSpan {
				kept = append(kept, in)
			}
		}
		svc.Incidents = kept[max(0, len(kept)-maxIncidents):]
	}
	return changed
}

// summarize déduit l'état d'un fournisseur des vérifications de ses comptes.
func summarize(list []Status) (state, detail string, latency time.Duration) {
	var best, degraded, down *Status
	for i := range list {
		s := &list[i]
		switch s.service {
		case Up:
			if best == nil || s.latency < best.latency {
				best = s
			}
		case Degraded:
			degraded = cmpLatency(degraded, s)
		default:
			down = cmpLatency(down, s)
		}
	}
	switch {
	case best != nil && best.latency > slowAfter:
		return Degraded, fmt.Sprintf("réponse lente (%.1f s)", best.latency.Seconds()), best.latency
	case best != nil:
		return Up, "", best.latency
	case degraded != nil:
		return Degraded, degraded.Error, degraded.latency
	}
	return Down, down.Error, down.latency
}

func cmpLatency(cur, s *Status) *Status {
	if cur == nil || s.latency < cur.latency {
		return s
	}
	return cur
}

// info est ce qu'un fournisseur dit du compte.
type info struct {
	username string
	premium  bool
	expires  time.Time // zéro si inconnue
}

// checkError est une erreur de vérification lisible, sans la clé ; refused : clé refusée par le fournisseur,
// down : service injoignable ou en erreur (réseau, délai dépassé, code 5xx).
type checkError struct {
	msg     string
	refused bool
	down    bool
}

func (e *checkError) Error() string { return e.msg }

func refused(msg string) error { return &checkError{msg: msg, refused: true} }

func (m *Manager) check(ctx context.Context, a Account) Status {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var in info
	var err error
	start := time.Now()
	switch a.Provider {
	case RealDebrid:
		in, err = m.realDebrid(ctx, a.Key)
	case AllDebrid:
		in, err = m.allDebrid(ctx, a.Key)
	case TorBox:
		in, err = m.torBox(ctx, a.Key)
	default:
		in, err = m.premiumize(ctx, a.Key)
	}
	now := time.Now()
	s := Status{ID: a.ID, CheckedAt: now.UnixMilli(), service: Up, latency: now.Sub(start)}
	if err != nil {
		var ce *checkError
		isCheck := errors.As(err, &ce)
		s.Refused = isCheck && ce.refused
		s.Error = err.Error()
		switch {
		case !isCheck || ce.down:
			s.service = Down
		case !ce.refused:
			s.service = Degraded // limite de requêtes, réponse inattendue
		}
		m.log.Debug("compte debrid", "compte", a.Label, "erreur", s.Error)
		return s
	}
	s.OK, s.Username = true, in.username
	if !in.expires.IsZero() {
		s.Expires = in.expires.UnixMilli()
	}
	// Une échéance passée l'emporte sur l'indicateur du fournisseur.
	s.Premium = in.premium && (in.expires.IsZero() || in.expires.After(now))
	return s
}
