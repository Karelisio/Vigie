// Package settings gère les réglages modifiables depuis l'appli (seuils, heures calmes, sourdine…).
// Les valeurs par défaut viennent des variables d'environnement ; les modifications sont conservées en base.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/store"
)

type Settings struct {
	CPU          float64  `json:"cpu"`
	RAM          float64  `json:"ram"`
	Disk         float64  `json:"disk"`
	DurationMin  int      `json:"duration_min"`
	QuietEnabled bool     `json:"quiet_enabled"`
	QuietStart   string   `json:"quiet_start"` // HH:MM, heure locale (TZ)
	QuietEnd     string   `json:"quiet_end"`
	Muted        []string `json:"muted"` // conteneurs sans notification
	ImageUpdates bool     `json:"image_updates"`
	OracleWatch  bool     `json:"oracle_watch"`
	CertDays     int      `json:"cert_days"`
	PruneImages  bool     `json:"prune_images"` // supprimer l'ancienne image après une mise à jour

	// Mises à jour automatiques de nuit.
	AutoUpdate      bool     `json:"auto_update"`
	AutoUpdateTime  string   `json:"auto_update_time"`  // HH:MM, heure locale (TZ)
	AutoUpdateAll   bool     `json:"auto_update_all"`   // tous les conteneurs (sinon AutoUpdateList)
	AutoUpdateList  []string `json:"auto_update_list"`  // noms des conteneurs concernés
	AutoUpdateAgent bool     `json:"auto_update_agent"` // inclure l'agent lui-même

	// Quota de trafic sortant mensuel (Oracle Always Free : 10 To).
	NetQuotaTB    float64 `json:"net_quota_tb"`    // 0 = pas de suivi de quota
	NetQuotaAlert int     `json:"net_quota_alert"` // seuil d'alerte en % du quota

	// Mode maintenance : aucune notification jusqu'à cette date (ms Unix, 0 = inactif).
	MaintenanceUntil int64 `json:"maintenance_until"`

	// Alertes sur les logs des conteneurs.
	LogRules []LogRule `json:"log_rules"`

	// Rapport hebdomadaire (dimanche soir).
	WeeklyReport bool `json:"weekly_report"`
}

// LogRule déclenche une alerte quand une ligne de log correspond au motif.
type LogRule struct {
	ID        string `json:"id"`
	Container string `json:"container"` // nom du conteneur
	Pattern   string `json:"pattern"`
	Regex     bool   `json:"regex"` // sinon : texte, sans tenir compte de la casse
}

// InMaintenance indique si les notifications sont suspendues.
func (s Settings) InMaintenance(t time.Time) bool {
	return s.MaintenanceUntil > 0 && t.UnixMilli() < s.MaintenanceUntil
}

func (s Settings) AlertDuration() time.Duration { return time.Duration(s.DurationMin) * time.Minute }

func (s Settings) IsMuted(name string) bool {
	for _, m := range s.Muted {
		if m == name {
			return true
		}
	}
	return false
}

func parseClock(v string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("heure invalide %q (format HH:MM)", v)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// InQuietHours indique si t tombe dans la plage calme (qui peut passer minuit).
func (s Settings) InQuietHours(t time.Time) bool {
	if !s.QuietEnabled {
		return false
	}
	start, err1 := parseClock(s.QuietStart)
	end, err2 := parseClock(s.QuietEnd)
	if err1 != nil || err2 != nil || start == end {
		return false
	}
	now := t.Hour()*60 + t.Minute()
	if start < end {
		return now >= start && now < end
	}
	return now >= start || now < end
}

func (s *Settings) validate() error {
	for _, v := range []struct {
		name string
		val  float64
	}{{"CPU", s.CPU}, {"RAM", s.RAM}, {"disque", s.Disk}} {
		if v.val < 30 || v.val > 99 {
			return fmt.Errorf("seuil %s hors limites (30–99 %%)", v.name)
		}
	}
	if s.DurationMin < 1 || s.DurationMin > 120 {
		return errors.New("durée de dépassement hors limites (1–120 min)")
	}
	if s.CertDays < 1 || s.CertDays > 90 {
		return errors.New("préavis certificat hors limites (1–90 jours)")
	}
	if s.QuietEnabled {
		if _, err := parseClock(s.QuietStart); err != nil {
			return err
		}
		if _, err := parseClock(s.QuietEnd); err != nil {
			return err
		}
	}
	s.Muted = cleanNames(s.Muted)
	if s.AutoUpdateTime == "" {
		s.AutoUpdateTime = "04:00"
	}
	if _, err := parseClock(s.AutoUpdateTime); err != nil {
		return err
	}
	s.AutoUpdateList = cleanNames(s.AutoUpdateList)
	if s.NetQuotaTB < 0 || s.NetQuotaTB > 1000 {
		return errors.New("quota de trafic hors limites (0–1000 To)")
	}
	if s.NetQuotaAlert == 0 {
		s.NetQuotaAlert = 80
	}
	if s.NetQuotaAlert < 50 || s.NetQuotaAlert > 100 {
		return errors.New("seuil d'alerte du quota hors limites (50–100 %)")
	}
	if s.MaintenanceUntil < 0 {
		s.MaintenanceUntil = 0
	}
	if len(s.LogRules) > 20 {
		return errors.New("20 alertes sur les logs au maximum")
	}
	rules := []LogRule{}
	for i, r := range s.LogRules {
		r.Container = strings.TrimSpace(strings.TrimPrefix(r.Container, "/"))
		r.Pattern = strings.TrimSpace(r.Pattern)
		if r.Container == "" || r.Pattern == "" {
			return errors.New("alerte sur les logs : conteneur et texte obligatoires")
		}
		if len(r.Pattern) > 200 {
			return errors.New("alerte sur les logs : texte trop long (200 caractères au plus)")
		}
		if r.Regex {
			if _, err := regexp.Compile(r.Pattern); err != nil {
				return fmt.Errorf("expression régulière invalide %q : %v", r.Pattern, err)
			}
		}
		if r.ID == "" {
			r.ID = fmt.Sprintf("r%d-%d", time.Now().UnixNano()%1_000_000_000, i)
		}
		rules = append(rules, r)
	}
	s.LogRules = rules
	return nil
}

// cleanNames retire les doublons et les « / » de tête, puis trie.
func cleanNames(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range in {
		if m = strings.TrimSpace(strings.TrimPrefix(m, "/")); m != "" && !seen[m] && len(out) < 200 {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

type Manager struct {
	st *store.Store
	mu sync.RWMutex
	s  Settings
	// OnChange est appelé après chaque modification.
	OnChange func(Settings)
}

const key = "settings"

func Load(ctx context.Context, st *store.Store, cfg *config.Config) *Manager {
	s := Settings{
		CPU: cfg.AlertCPU, RAM: cfg.AlertRAM, Disk: cfg.AlertDisk,
		DurationMin:  max(1, int(cfg.AlertDuration.Minutes())),
		QuietStart:   "23:00",
		QuietEnd:     "07:00",
		Muted:        []string{},
		ImageUpdates: true,
		OracleWatch:  true,
		CertDays:     14,
		PruneImages:  true,

		AutoUpdateTime: "04:00",
		AutoUpdateList: []string{},
		NetQuotaTB:     10,
		NetQuotaAlert:  80,
		LogRules:       []LogRule{},
		WeeklyReport:   true,
	}
	if raw := st.Get(ctx, key); raw != "" {
		saved := s
		if err := json.Unmarshal([]byte(raw), &saved); err == nil && saved.validate() == nil {
			s = saved
		}
	}
	return &Manager{st: st, s: s}
}

func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := m.s
	cp.Muted = append([]string{}, m.s.Muted...)
	cp.AutoUpdateList = append([]string{}, m.s.AutoUpdateList...)
	cp.LogRules = append([]LogRule{}, m.s.LogRules...)
	return cp
}

func (m *Manager) Update(ctx context.Context, s Settings) (Settings, error) {
	if s.Muted == nil {
		s.Muted = []string{}
	}
	if err := s.validate(); err != nil {
		return Settings{}, err
	}
	b, _ := json.Marshal(s)
	if err := m.st.Set(ctx, key, string(b)); err != nil {
		return Settings{}, err
	}
	m.mu.Lock()
	m.s = s
	m.mu.Unlock()
	if m.OnChange != nil {
		m.OnChange(s)
	}
	return s, nil
}

// SetMuted active ou coupe les notifications d'un conteneur.
func (m *Manager) SetMuted(ctx context.Context, name string, muted bool) (Settings, error) {
	s := m.Get()
	list := []string{}
	for _, n := range s.Muted {
		if n != name {
			list = append(list, n)
		}
	}
	if muted {
		list = append(list, name)
	}
	s.Muted = list
	return m.Update(ctx, s)
}
