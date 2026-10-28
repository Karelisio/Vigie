// Package settings gère les réglages modifiables depuis l'appli (seuils, heures calmes, sourdine…).
// Les valeurs par défaut viennent des variables d'environnement ; les modifications sont conservées en base.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	seen := map[string]bool{}
	muted := []string{}
	for _, m := range s.Muted {
		m = strings.TrimSpace(strings.TrimPrefix(m, "/"))
		if m != "" && !seen[m] && len(muted) < 200 {
			seen[m] = true
			muted = append(muted, m)
		}
	}
	sort.Strings(muted)
	s.Muted = muted
	return nil
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
