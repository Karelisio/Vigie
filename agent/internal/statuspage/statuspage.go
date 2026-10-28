// Package statuspage publie une page d'état publique, sans authentification, à une adresse secrète :
// état des services et conteneurs choisis, sous le nom public choisi, disponibilité et incidents récents.
// Rien d'autre n'en sort : ni adresse, ni IP, ni image, ni message d'erreur, ni nom interne.
package statuspage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	KindCheck     = "check"
	KindContainer = "container"

	configKey    = "status_page"
	DefaultTitle = "État des services"
	maxItems     = 30
	historyTTL   = time.Minute
)

// Item est un élément affiché : un check (Ref = son ID) ou un conteneur (Ref = son nom), sous un nom public.
type Item struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// Config est la configuration de la page, conservée en base sous sa propre clé.
type Config struct {
	Enabled     bool   `json:"enabled"`
	Slug        string `json:"slug"` // identifiant secret de l'adresse, changé seulement par Regenerate
	Title       string `json:"title"`
	Description string `json:"description"`
	Items       []Item `json:"items"`
	ShowUptime  bool   `json:"show_uptime"`
}

// Options : état courant des checks et des conteneurs, et seuil d'échecs avant « hors ligne ».
type Options struct {
	Checks     func() []checks.Status
	Containers func() []monitor.Container
	DownAfter  int
}

type Manager struct {
	st  *store.Store
	opt Options

	mu  sync.Mutex
	cfg Config

	build   sync.Mutex // un calcul d'historique à la fois
	histMu  sync.Mutex
	hist    map[string]history // par élément (kind:ref)
	histAt  time.Time
	histFor string // empreinte des éléments du cache
}

func New(ctx context.Context, st *store.Store, opt Options) (*Manager, error) {
	if opt.DownAfter < 1 {
		opt.DownAfter = 1
	}
	if opt.Checks == nil {
		opt.Checks = func() []checks.Status { return nil }
	}
	if opt.Containers == nil {
		opt.Containers = func() []monitor.Container { return nil }
	}
	m := &Manager{st: st, opt: opt, cfg: Config{Title: DefaultTitle, Items: []Item{}, ShowUptime: true}}
	if raw := st.Get(ctx, configKey); raw != "" {
		var saved Config
		if err := json.Unmarshal([]byte(raw), &saved); err == nil {
			if saved.Items == nil {
				saved.Items = []Item{}
			}
			m.cfg = saved
		}
	}
	if len(m.cfg.Slug) < 16 {
		m.cfg.Slug = newSlug()
		if err := m.save(ctx, m.cfg); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// newSlug tire 16 octets au hasard : 22 caractères URL-safe (128 bits).
func newSlug() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) save(ctx context.Context, c Config) error {
	raw, _ := json.Marshal(c)
	return m.st.Set(ctx, configKey, string(raw))
}

// Get renvoie la configuration actuelle.
func (m *Manager) Get() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cfg
	c.Items = append([]Item{}, m.cfg.Items...)
	return c
}

// Update valide et enregistre la configuration ; l'identifiant secret n'est pas modifiable ici.
func (m *Manager) Update(ctx context.Context, in Config) (Config, error) {
	if err := validate(&in); err != nil {
		return Config{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	in.Slug = m.cfg.Slug
	if err := m.save(ctx, in); err != nil {
		return Config{}, err
	}
	m.cfg = in
	m.invalidate()
	return in, nil
}

// Regenerate remplace l'identifiant secret : l'ancienne adresse cesse aussitôt de fonctionner.
func (m *Manager) Regenerate(ctx context.Context) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.cfg
	c.Slug = newSlug()
	if err := m.save(ctx, c); err != nil {
		return Config{}, err
	}
	m.cfg = c
	return c, nil
}

// Match indique si slug ouvre la page : page activée et identifiant exact (comparaison à temps constant).
func (m *Manager) Match(slug string) bool {
	m.mu.Lock()
	enabled, want := m.cfg.Enabled, m.cfg.Slug
	m.mu.Unlock()
	a, b := sha256.Sum256([]byte(slug)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1 && enabled && want != ""
}

var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func validate(c *Config) error {
	c.Title = cleanText(c.Title)
	if c.Title == "" {
		c.Title = DefaultTitle
	}
	if utf8.RuneCountInString(c.Title) > 80 {
		return errors.New("titre trop long (80 caractères au plus)")
	}
	c.Description = cleanText(c.Description)
	if utf8.RuneCountInString(c.Description) > 300 {
		return errors.New("description trop longue (300 caractères au plus)")
	}
	if len(c.Items) > maxItems {
		return fmt.Errorf("%d services au plus sur la page", maxItems)
	}
	seen := map[string]bool{}
	items := make([]Item, 0, len(c.Items))
	for _, it := range c.Items {
		it.Kind = strings.ToLower(strings.TrimSpace(it.Kind))
		it.Ref = strings.TrimPrefix(strings.TrimSpace(it.Ref), "/")
		it.Label = cleanText(it.Label)
		switch it.Kind {
		case KindCheck:
			if id, err := strconv.ParseInt(it.Ref, 10, 64); err != nil || id <= 0 {
				return fmt.Errorf("identifiant de service invalide %q", it.Ref)
			}
		case KindContainer:
			if !containerNameRe.MatchString(it.Ref) {
				return fmt.Errorf("nom de conteneur invalide %q", it.Ref)
			}
		default:
			return fmt.Errorf("type d'élément inconnu %q (check ou container)", it.Kind)
		}
		if it.Label == "" {
			return errors.New("chaque élément affiché doit avoir un nom public")
		}
		if utf8.RuneCountInString(it.Label) > 60 {
			return fmt.Errorf("nom public trop long : %q (60 caractères au plus)", it.Label)
		}
		key := it.Kind + ":" + it.Ref
		if seen[key] {
			return fmt.Errorf("élément en double : %q", it.Label)
		}
		seen[key] = true
		items = append(items, it)
	}
	c.Items = items
	return nil
}

// cleanText retire les caractères de contrôle et les espaces superflus.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// ---- Contenu de la page ----

// États d'un élément et de la page.
const (
	Online   = "online"
	Degraded = "degraded"
	Offline  = "offline"
	Unknown  = "unknown"

	Operational = "operational"
	Disrupted   = "degraded"
	Outage      = "outage"
)

// Page est tout ce que montre la page publique.
type Page struct {
	Title       string
	Description string
	Status      string // operational, degraded, outage
	Counts      map[string]int
	Items       []PageItem
	Incidents   []Incident
	ShowUptime  bool
	UpdatedAt   time.Time
}

type PageItem struct {
	Name   string
	Status string // online, degraded, offline, unknown
	history
}

// Day est la disponibilité d'un jour (minuit UTC) ; Ratio vaut -1 sans donnée.
type Day struct {
	Date  time.Time
	Ratio float64
}

// Incident : élément hors ligne ou dégradé ; End nul s'il est en cours.
type Incident struct {
	Name   string
	Status string // offline ou degraded
	Start  time.Time
	End    time.Time
}

type history struct {
	Days      []Day
	Uptime    float64 // sur les jours avec données, -1 sans donnée
	DataDays  int
	incidents []Incident
}

// Page calcule le contenu de la page : état en direct, historique mis en cache une minute.
func (m *Manager) Page(ctx context.Context, now time.Time) Page {
	c := m.Get()
	hist := m.histories(ctx, c.Items, now)
	byID := map[int64]checks.Status{}
	for _, s := range m.opt.Checks() {
		byID[s.ID] = s
	}
	byName := map[string]monitor.Container{}
	for _, ct := range m.opt.Containers() {
		byName[ct.Name] = ct
	}
	p := Page{
		Title: c.Title, Description: c.Description, ShowUptime: c.ShowUptime, UpdatedAt: now,
		Items: []PageItem{}, Incidents: []Incident{}, Counts: map[string]int{},
	}
	for _, it := range c.Items {
		var status string
		switch it.Kind {
		case KindCheck:
			id, _ := strconv.ParseInt(it.Ref, 10, 64)
			s, ok := byID[id]
			if !ok {
				continue // check supprimé : retiré de la page
			}
			status = m.checkStatus(s)
		case KindContainer:
			ct, ok := byName[it.Ref]
			status = containerStatus(ct, ok)
		}
		h := hist[it.Kind+":"+it.Ref]
		p.Items = append(p.Items, PageItem{Name: it.Label, Status: status, history: h})
		p.Counts[status]++
		for _, inc := range fresh(h.incidents, status, now) {
			inc.Name = it.Label
			p.Incidents = append(p.Incidents, inc)
		}
	}
	p.Status = overall(p.Counts)
	sortIncidents(p.Incidents)
	if len(p.Incidents) > maxIncidents {
		p.Incidents = p.Incidents[:maxIncidents]
	}
	return p
}

func (m *Manager) checkStatus(s checks.Status) string {
	switch {
	case !s.Enabled || s.Last == nil:
		return Unknown
	case s.Last.OK:
		return Online
	case s.ConsecutiveFailures >= m.opt.DownAfter:
		return Offline
	}
	return Degraded
}

func containerStatus(c monitor.Container, found bool) string {
	switch {
	case !found || c.State != "running":
		return Offline
	case c.Health == "unhealthy" || c.Health == "starting":
		return Degraded
	}
	return Online
}

// overall : panne si plus de la moitié des éléments sont hors ligne, perturbations si l'un d'eux
// n'est pas en ligne ; les éléments à l'état inconnu ne comptent pas.
func overall(counts map[string]int) string {
	known := counts[Online] + counts[Degraded] + counts[Offline]
	switch {
	case known > 0 && counts[Offline]*2 > known:
		return Outage
	case counts[Offline]+counts[Degraded] > 0:
		return Disrupted
	}
	return Operational
}

func (m *Manager) invalidate() {
	m.histMu.Lock()
	m.hist = nil
	m.histMu.Unlock()
}

func itemsKey(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Kind + ":" + it.Ref + "\n")
	}
	return b.String()
}

// histories renvoie l'historique des éléments, recalculé au plus une fois par minute.
func (m *Manager) histories(ctx context.Context, items []Item, now time.Time) map[string]history {
	key := itemsKey(items)
	m.histMu.Lock()
	if m.hist != nil && m.histFor == key && now.Sub(m.histAt) < historyTTL {
		h := m.hist
		m.histMu.Unlock()
		return h
	}
	m.histMu.Unlock()

	m.build.Lock()
	defer m.build.Unlock()
	m.histMu.Lock()
	if m.hist != nil && m.histFor == key && now.Sub(m.histAt) < historyTTL {
		h := m.hist
		m.histMu.Unlock()
		return h
	}
	m.histMu.Unlock()

	// Indépendant de la requête : le résultat sert aux visiteurs suivants.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	out := map[string]history{}
	for _, it := range items {
		switch it.Kind {
		case KindCheck:
			id, _ := strconv.ParseInt(it.Ref, 10, 64)
			out[it.Kind+":"+it.Ref] = m.checkHistory(ctx, id, now)
		case KindContainer:
			out[it.Kind+":"+it.Ref] = m.containerHistory(ctx, it.Ref, now)
		}
	}
	m.histMu.Lock()
	m.hist, m.histFor, m.histAt = out, key, now
	m.histMu.Unlock()
	return out
}
