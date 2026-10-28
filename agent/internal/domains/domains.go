// Package domains suit la date d'expiration des noms de domaine : ceux des services surveillés
// (checks HTTP et Stremio, adresse publique de l'agent) et ceux ajoutés à la main, via RDAP.
package domains

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	SourceAuto   = "auto"   // détecté depuis un service surveillé
	SourceManual = "manual" // ajouté à la main dans les réglages

	stateKey = "domains_state"

	refreshOK    = 24 * time.Hour // une vérification par jour
	refreshError = 6 * time.Hour  // nouvel essai après une erreur
	minRecheck   = time.Minute    // « Vérifier maintenant » : pas plus d'une requête par minute et par domaine
	checkTimeout = 2 * time.Minute
	workers      = 4
	maxDomains   = 60
)

// Status est l'état d'un domaine suivi. Les dates sont en ms Unix, 0 si inconnues.
type Status struct {
	Domain      string   `json:"domain"`
	Source      string   `json:"source"`   // auto ou manual
	Services    []string `json:"services"` // services qui l'utilisent (source auto)
	Expires     int64    `json:"expires"`
	DaysLeft    *int     `json:"days_left"` // jours entiers restants (négatif une fois expiré), null si inconnu
	Registered  int64    `json:"registered"`
	LastChanged int64    `json:"last_changed"`
	Registrar   string   `json:"registrar"`
	Status      []string `json:"status"` // statuts RDAP (« client transfer prohibited »…)
	CheckedAt   int64    `json:"checked_at"`
	Error       string   `json:"error,omitempty"`
	// Note explique une date d'expiration inconnue alors que la vérification a réussi.
	Note string `json:"note,omitempty"`
}

// result est ce qui est conservé en base pour un domaine après une vérification.
type result struct {
	Expires     int64    `json:"expires,omitempty"`
	Registered  int64    `json:"registered,omitempty"`
	LastChanged int64    `json:"last_changed,omitempty"`
	Registrar   string   `json:"registrar,omitempty"`
	Status      []string `json:"status,omitempty"`
	CheckedAt   int64    `json:"checked_at"`
	Error       string   `json:"error,omitempty"`
}

// Options : sources des domaines suivis et adresses RDAP (remplacées dans les tests).
type Options struct {
	PublicURL string          // VIGIE_PUBLIC_URL
	Manual    func() []string // domaines ajoutés à la main (réglages)
	Version   string
	// BootstrapURL : fichier d'amorçage IANA ; FallbackURL : service de redirection pour un TLD absent.
	BootstrapURL string
	FallbackURL  string
}

type Manager struct {
	st   *store.Store
	log  *slog.Logger
	opt  Options
	http *http.Client

	// OnCheck reçoit l'état de tous les domaines après chaque passage (alertes d'expiration).
	OnCheck func([]Status)

	run     sync.Mutex // un passage à la fois
	mu      sync.Mutex
	results map[string]result
}

func New(ctx context.Context, st *store.Store, log *slog.Logger, opt Options) *Manager {
	if opt.BootstrapURL == "" {
		opt.BootstrapURL = "https://data.iana.org/rdap/dns.json"
	}
	if opt.FallbackURL == "" {
		opt.FallbackURL = "https://rdap.org/domain/"
	}
	if opt.Manual == nil {
		opt.Manual = func() []string { return nil }
	}
	m := &Manager{
		st: st, log: log, opt: opt,
		http: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errTooManyRedirects
				}
				return nil
			},
		},
		results: map[string]result{},
	}
	if raw := st.Get(ctx, stateKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m.results); err != nil || m.results == nil {
			m.results = map[string]result{}
		}
	}
	return m
}

// Run vérifie les domaines peu après le démarrage puis chaque heure : seuls ceux dont le résultat
// a plus d'un jour (6 h après une erreur) sont interrogés, mais les alertes sont réévaluées à chaque fois.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.refresh(ctx, false)
		t.Reset(time.Hour)
	}
}

// Check vérifie maintenant tous les domaines (sauf ceux vérifiés il y a moins d'une minute) et renvoie leur état.
func (m *Manager) Check(ctx context.Context) []Status {
	return m.refresh(ctx, true)
}

// State renvoie l'état des domaines suivis, sans rien interroger.
func (m *Manager) State(ctx context.Context) []Status {
	return m.state(m.tracked(ctx), time.Now())
}

func (m *Manager) refresh(ctx context.Context, force bool) []Status {
	m.run.Lock()
	defer m.run.Unlock()
	list := m.tracked(ctx)
	now := time.Now()
	m.mu.Lock()
	var due []string
	for _, t := range list {
		if t.invalid != "" {
			continue
		}
		r, ok := m.results[t.domain]
		age := now.Sub(time.UnixMilli(r.CheckedAt))
		switch {
		case !ok:
			due = append(due, t.domain)
		case force && age >= minRecheck:
			due = append(due, t.domain)
		case r.Error != "" && age >= refreshError, age >= refreshOK:
			due = append(due, t.domain)
		}
	}
	m.mu.Unlock()

	if len(due) > 0 {
		ctx, cancel := context.WithTimeout(ctx, checkTimeout)
		boot := m.bootstrap(ctx)
		fresh := make([]result, len(due))
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for i, d := range due {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				fresh[i] = m.lookup(ctx, d, boot)
			}()
		}
		wg.Wait()
		cancel()
		m.mu.Lock()
		for i, d := range due {
			if fresh[i].Error != "" {
				// Une erreur ne fait pas oublier la dernière réponse valable.
				prev := m.results[d]
				prev.Error, prev.CheckedAt = fresh[i].Error, fresh[i].CheckedAt
				fresh[i] = prev
				m.log.Warn("expiration du domaine", "domaine", d, "erreur", fresh[i].Error)
			}
			m.results[d] = fresh[i]
		}
		m.mu.Unlock()
	}

	// Domaines qui ne sont plus suivis : oubliés.
	keep := map[string]bool{}
	for _, t := range list {
		keep[t.domain] = true
	}
	m.mu.Lock()
	for d := range m.results {
		if !keep[d] {
			delete(m.results, d)
		}
	}
	raw, _ := json.Marshal(m.results)
	m.mu.Unlock()
	if err := m.st.Set(context.WithoutCancel(ctx), stateKey, string(raw)); err != nil {
		m.log.Warn("domaines", "erreur", err)
	}

	out := m.state(list, time.Now())
	if m.OnCheck != nil {
		m.OnCheck(out)
	}
	return out
}

func (m *Manager) state(list []tracked, now time.Time) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(list))
	for _, t := range list {
		s := Status{Domain: t.domain, Source: t.source, Services: t.services, Status: []string{}, Error: t.invalid}
		if t.invalid == "" {
			r := m.results[t.domain]
			s.Expires, s.Registered, s.LastChanged = r.Expires, r.Registered, r.LastChanged
			s.Registrar, s.CheckedAt, s.Error = r.Registrar, r.CheckedAt, r.Error
			if r.Status != nil {
				s.Status = r.Status
			}
			if r.Expires > 0 {
				d := DaysLeft(r.Expires, now)
				s.DaysLeft = &d
			} else if r.CheckedAt > 0 && r.Error == "" {
				s.Note = "le registre ne publie pas la date d'expiration (RDAP) : vérifie-la chez ton bureau d'enregistrement"
			}
		}
		out = append(out, s)
	}
	return out
}

// DaysLeft renvoie le nombre de jours entiers avant expires (ms Unix) : 0 le dernier jour, négatif une fois expiré.
func DaysLeft(expires int64, now time.Time) int {
	return int(math.Floor(time.UnixMilli(expires).Sub(now).Hours() / 24))
}

// ---- Domaines suivis ----

type tracked struct {
	domain   string
	source   string
	services []string
	invalid  string // domaine manuel impossible à vérifier
}

// tracked calcule la liste des domaines suivis : ceux des checks et de l'adresse publique (auto),
// puis ceux des réglages (manual, prioritaires : c'est la source affichée).
func (m *Manager) tracked(ctx context.Context) []tracked {
	byName := map[string]*tracked{}
	var order []string
	add := func(domain, source, service, invalid string) {
		t, ok := byName[domain]
		if !ok {
			if len(order) >= maxDomains {
				return
			}
			t = &tracked{domain: domain, source: source, services: []string{}, invalid: invalid}
			byName[domain] = t
			order = append(order, domain)
		}
		if source == SourceManual {
			t.source = SourceManual
		}
		if service != "" && !contains(t.services, service) {
			t.services = append(t.services, service)
		}
	}
	if list, err := m.st.Checks(ctx); err == nil {
		for _, c := range list {
			if d, ok := hostDomain(c.URL); ok {
				add(d, SourceAuto, c.Name, "")
			}
		}
	}
	if d, ok := hostDomain(m.opt.PublicURL); ok {
		add(d, SourceAuto, "Vigie", "")
	}
	for _, raw := range m.opt.Manual() {
		if d, ok := Registrable(raw); ok {
			add(d, SourceManual, "", "")
		} else {
			add(strings.ToLower(strings.TrimSpace(raw)), SourceManual, "", "domaine impossible à vérifier : suffixe privé ou inconnu")
		}
	}
	sort.Strings(order)
	out := make([]tracked, 0, len(order))
	for _, d := range order {
		t := byName[d]
		sort.Strings(t.services)
		out = append(out, *t)
	}
	return out
}

func hostDomain(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	return Registrable(u.Hostname())
}

// TLD et noms réservés, jamais enregistrables (réseau local, tests, Tor).
var reserved = map[string]bool{
	"arpa": true, "onion": true, "test": true, "example": true, "invalid": true, "localhost": true,
	"local": true, "internal": true, "lan": true, "home": true, "corp": true, "localdomain": true,
}

// Registrable renvoie le domaine enregistrable d'un nom d'hôte (« aiostreams.example.co.uk » → « example.co.uk »),
// d'après la liste des suffixes publics. Refuse les adresses IP, localhost, les noms internes et les sous-domaines
// d'un suffixe privé (duckdns.org, github.io…) : leur expiration ne dépend pas de toi.
func Registrable(host string) (string, bool) {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "" || len(h) > 253 || net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return "", false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return "", false
		}
	}
	if reserved[h[strings.LastIndexByte(h, '.')+1:]] {
		return "", false
	}
	suffix, icann := publicsuffix.PublicSuffix(h)
	if !icann || suffix == h {
		return "", false
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return "", false
	}
	return d, true
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
