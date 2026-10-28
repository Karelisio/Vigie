// Package debrid vérifie les comptes debrid (Real-Debrid, AllDebrid, TorBox, Premiumize) :
// clé acceptée, premium actif et date d'expiration. Les clés restent sur l'agent.
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
	interval     = 6 * time.Hour
	checkTimeout = 15 * time.Second
	maxAccounts  = 10
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
}

// Name renvoie le nom du compte : le fournisseur, suivi du libellé s'il en diffère.
func (s Status) Name() string {
	name := providers[s.Provider]
	if s.Label != "" && s.Label != name {
		name += " (" + s.Label + ")"
	}
	return name
}

// State est l'état de tous les comptes.
type State struct {
	Accounts []Status `json:"accounts"`
}

type Manager struct {
	st    *store.Store
	log   *slog.Logger
	http  *http.Client
	bases map[string]string // URL de base de l'API par fournisseur (remplacées dans les tests)

	// OnCheck reçoit l'état des comptes après chaque vérification (alertes d'expiration).
	OnCheck func([]Status)

	run      sync.Mutex // une vérification ou une modification à la fois
	mu       sync.Mutex
	accounts []Account
	results  map[string]Status // par ID de compte
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
	}
	if raw := st.Get(ctx, accountsKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m.accounts); err != nil {
			log.Warn("comptes debrid illisibles", "erreur", err)
			m.accounts = []Account{}
		}
	}
	return m
}

// Run vérifie les comptes peu après le démarrage, puis toutes les 6 heures.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.Check(ctx)
		t.Reset(interval)
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
	return out
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
	m.mu.Unlock()
	st := m.State()
	if m.OnCheck != nil {
		m.OnCheck(st.Accounts)
	}
	return st
}

// info est ce qu'un fournisseur dit du compte.
type info struct {
	username string
	premium  bool
	expires  time.Time // zéro si inconnue
}

// checkError est une erreur de vérification lisible, sans la clé ; refused : clé refusée par le fournisseur.
type checkError struct {
	msg     string
	refused bool
}

func (e *checkError) Error() string { return e.msg }

func refused(msg string) error { return &checkError{msg: msg, refused: true} }

func (m *Manager) check(ctx context.Context, a Account) Status {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var in info
	var err error
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
	s := Status{ID: a.ID, CheckedAt: now.UnixMilli()}
	if err != nil {
		var ce *checkError
		s.Refused = errors.As(err, &ce) && ce.refused
		s.Error = err.Error()
		m.log.Info("compte debrid", "compte", a.Label, "erreur", s.Error)
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
