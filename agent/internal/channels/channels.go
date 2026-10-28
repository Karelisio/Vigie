// Package channels relaie les alertes vers des canaux secondaires : Telegram, Discord et ntfy.
// La configuration (secrets compris) reste sur l'agent ; l'API ne la renvoie que masquée.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/secret"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	TypeTelegram = "telegram"
	TypeDiscord  = "discord"
	TypeNtfy     = "ntfy"

	LevelAll      = "all"      // toutes les alertes
	LevelCritical = "critical" // critiques seulement

	storeKey    = "alert_channels"
	sendTimeout = 10 * time.Second
	attempts    = 3  // envoi initial + nouvelles tentatives
	queueSize   = 32 // messages en attente par canal
)

// types énumère les canaux pris en charge.
var types = []string{TypeTelegram, TypeDiscord, TypeNtfy}

// Channel est un canal d'alerte (un seul par type). Les champs utiles dépendent du type.
type Channel struct {
	Type     string `json:"type"`
	Enabled  bool   `json:"enabled"`
	MinLevel string `json:"min_level"`

	// Telegram
	BotToken string `json:"bot_token,omitempty"` // secret
	ChatID   string `json:"chat_id,omitempty"`
	// Discord
	WebhookURL string `json:"webhook_url,omitempty"` // secret (l'URL contient le jeton)
	// ntfy
	Server string `json:"server,omitempty"` // https://ntfy.sh par défaut
	Topic  string `json:"topic,omitempty"`
	Token  string `json:"token,omitempty"` // jeton d'accès facultatif, secret
}

// Config est la configuration complète des canaux.
type Config struct {
	Channels []Channel `json:"channels"`
}

// Message est une alerte à relayer.
type Message struct {
	Severity string // critical, warning, info
	Resolved bool
	Title    string
	Body     string
	TS       time.Time
}

// Result est le résultat d'un envoi de test vers un canal.
type Result struct {
	Type  string `json:"type"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type job struct {
	ch  Channel
	msg Message
}

type Manager struct {
	st     *store.Store
	log    *slog.Logger
	ctx    context.Context
	http   *http.Client
	server string // nom du serveur, rappelé dans chaque message
	queues map[string]chan job

	telegramAPI string        // https://api.telegram.org (remplacée dans les tests)
	backoff     time.Duration // attente avant la première nouvelle tentative

	update sync.Mutex // une modification à la fois
	mu     sync.Mutex
	cfg    Config
}

// New charge la configuration et démarre un envoyeur par type de canal :
// un canal lent ou en panne ne retarde pas les autres.
func New(ctx context.Context, st *store.Store, log *slog.Logger, server string) *Manager {
	m := &Manager{
		st: st, log: log, ctx: ctx, server: server,
		http:        &http.Client{},
		queues:      map[string]chan job{},
		telegramAPI: "https://api.telegram.org",
		backoff:     3 * time.Second,
		cfg:         Config{Channels: []Channel{}},
	}
	if raw := st.Get(ctx, storeKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &m.cfg); err != nil {
			log.Warn("configuration des canaux d'alerte illisible", "erreur", err)
			m.cfg = Config{Channels: []Channel{}}
		}
	}
	for _, t := range types {
		q := make(chan job, queueSize)
		m.queues[t] = q
		go m.worker(q)
	}
	return m
}

// Get renvoie la configuration, secrets masqués.
func (m *Manager) Get() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Config{Channels: make([]Channel, 0, len(m.cfg.Channels))}
	for _, c := range m.cfg.Channels {
		c.BotToken = secret.Mask(c.BotToken)
		c.WebhookURL = secret.Mask(c.WebhookURL)
		c.Token = secret.Mask(c.Token)
		out.Channels = append(out.Channels, c)
	}
	return out
}

// Update remplace la configuration. Un secret vide ou masqué garde sa valeur actuelle.
func (m *Manager) Update(ctx context.Context, in Config) (Config, error) {
	// Les envois (Notify) ne lisent que m.cfg : l'écriture en base ne les retarde pas.
	m.update.Lock()
	defer m.update.Unlock()
	current := m.snapshot()
	next := Config{Channels: []Channel{}}
	seen := map[string]bool{}
	for _, c := range in.Channels {
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		if seen[c.Type] {
			return Config{}, fmt.Errorf("canal %s en double", c.Type)
		}
		seen[c.Type] = true
		old := find(current, c.Type)
		c.BotToken = secret.Merge(c.BotToken, old.BotToken)
		c.WebhookURL = secret.Merge(c.WebhookURL, old.WebhookURL)
		c.Token = secret.Merge(c.Token, old.Token)
		if err := c.normalize(); err != nil {
			return Config{}, err
		}
		next.Channels = append(next.Channels, c)
	}
	raw, _ := json.Marshal(next)
	if err := m.st.Set(ctx, storeKey, string(raw)); err != nil {
		return Config{}, err
	}
	m.mu.Lock()
	m.cfg = next
	m.mu.Unlock()
	return m.Get(), nil
}

func find(list []Channel, typ string) Channel {
	for _, c := range list {
		if c.Type == typ {
			return c
		}
	}
	return Channel{}
}

func (m *Manager) snapshot() []Channel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Channel(nil), m.cfg.Channels...)
}

// Notify met l'alerte en file pour chaque canal actif qui l'accepte, sans attendre l'envoi.
func (m *Manager) Notify(msg Message) {
	for _, c := range m.snapshot() {
		if !c.Enabled || (c.MinLevel == LevelCritical && msg.Severity != "critical") {
			continue
		}
		select {
		case m.queues[c.Type] <- job{c, msg}:
		default:
			m.log.Warn("file du canal d'alerte pleine, message ignoré", "canal", c.Type, "titre", msg.Title)
		}
	}
}

// Test envoie un message d'essai au canal demandé (même désactivé) ou, sans type, à tous les canaux actifs.
func (m *Manager) Test(ctx context.Context, typ string) ([]Result, error) {
	var targets []Channel
	for _, c := range m.snapshot() {
		if c.Type == typ || (typ == "" && c.Enabled) {
			targets = append(targets, c)
		}
	}
	switch {
	case len(targets) == 0 && typ != "":
		return nil, fmt.Errorf("canal %s non configuré", typ)
	case len(targets) == 0:
		return nil, errors.New("aucun canal actif")
	}
	msg := Message{
		Severity: "info", Title: "Vigie est prête",
		Body: "Les alertes de " + m.server + " arriveront ici.", TS: time.Now(),
	}
	results := make([]Result, len(targets))
	var wg sync.WaitGroup
	for i, c := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Result{Type: c.Type, OK: true}
			if err := m.send(ctx, c, msg); err != nil {
				results[i] = Result{Type: c.Type, Error: err.Error()}
			}
		}()
	}
	wg.Wait()
	return results, nil
}

func (m *Manager) worker(q <-chan job) {
	for {
		select {
		case <-m.ctx.Done():
			return
		case j := <-q:
			m.deliver(j)
		}
	}
}

// deliver envoie un message, avec de nouvelles tentatives tant que l'erreur est passagère.
func (m *Manager) deliver(j job) {
	var err error
	for i := range attempts {
		if i > 0 {
			wait := m.backoff * time.Duration(i*i)
			var se *sendError
			if errors.As(err, &se) && se.after > wait {
				wait = min(se.after, time.Minute)
			}
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if err = m.send(m.ctx, j.ch, j.msg); err == nil {
			return
		}
		var se *sendError
		if errors.As(err, &se) && !se.retry {
			break
		}
	}
	m.log.Warn("envoi vers un canal d'alerte", "canal", j.ch.Type, "titre", j.msg.Title, "erreur", err)
}

func (m *Manager) send(ctx context.Context, c Channel, msg Message) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	switch c.Type {
	case TypeTelegram:
		return m.telegram(ctx, c, msg)
	case TypeDiscord:
		return m.discord(ctx, c, msg)
	default:
		return m.ntfy(ctx, c, msg)
	}
}

// ---- Validation ----

var (
	botTokenRe    = regexp.MustCompile(`^[0-9]{5,20}:[A-Za-z0-9_-]{30,64}$`)
	chatIDRe      = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
	topicRe       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	webhookPathRe = regexp.MustCompile(`^/api(/v[0-9]+)?/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)
	discordHosts  = map[string]bool{"discord.com": true, "discordapp.com": true, "ptb.discord.com": true, "canary.discord.com": true}
)

// normalize nettoie le canal, retire les champs des autres types et vérifie ceux du sien.
// Les messages d'erreur ne reprennent jamais la valeur d'un secret.
func (c *Channel) normalize() error {
	c.MinLevel = strings.TrimSpace(c.MinLevel)
	if c.MinLevel == "" {
		c.MinLevel = LevelAll
	}
	if c.MinLevel != LevelAll && c.MinLevel != LevelCritical {
		return errors.New("niveau inconnu (all ou critical)")
	}
	c.ChatID = strings.TrimSpace(c.ChatID)
	c.Server = strings.TrimRight(strings.TrimSpace(c.Server), "/")
	c.Topic = strings.TrimSpace(c.Topic)
	switch c.Type {
	case TypeTelegram:
		c.WebhookURL, c.Server, c.Topic, c.Token = "", "", "", ""
		switch {
		case c.BotToken == "":
			return errors.New("token du bot Telegram manquant")
		case !botTokenRe.MatchString(c.BotToken):
			return errors.New("token de bot Telegram invalide (format 123456789:AAH…, donné par @BotFather)")
		case c.ChatID == "":
			return errors.New("chat id Telegram manquant")
		case !chatIDRe.MatchString(c.ChatID):
			return errors.New("chat id Telegram invalide (nombre comme 123456789 ou -100…, ou @nom_du_canal)")
		}
	case TypeDiscord:
		c.BotToken, c.ChatID, c.Server, c.Topic, c.Token = "", "", "", "", ""
		if c.WebhookURL == "" {
			return errors.New("URL du webhook Discord manquante")
		}
		u, err := url.Parse(c.WebhookURL)
		if err != nil || u.Scheme != "https" || !discordHosts[u.Hostname()] || !webhookPathRe.MatchString(u.Path) {
			return errors.New("URL de webhook Discord invalide (https://discord.com/api/webhooks/…)")
		}
	case TypeNtfy:
		c.BotToken, c.ChatID, c.WebhookURL = "", "", ""
		if c.Server == "" {
			c.Server = "https://ntfy.sh"
		}
		u, err := url.Parse(c.Server)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" {
			return errors.New("adresse du serveur ntfy invalide (ex. https://ntfy.sh)")
		}
		switch {
		case c.Topic == "":
			return errors.New("topic ntfy manquant")
		case !topicRe.MatchString(c.Topic):
			return errors.New("topic ntfy invalide (lettres, chiffres, - et _, 64 caractères au plus)")
		}
	default:
		return fmt.Errorf("type de canal inconnu %q (telegram, discord, ntfy)", c.Type)
	}
	return nil
}
