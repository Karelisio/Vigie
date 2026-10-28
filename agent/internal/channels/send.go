package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/secret"
)

// sendError est une erreur d'envoi lisible, sans secret ; retry : erreur passagère (réseau, 429, 5xx).
type sendError struct {
	msg   string
	retry bool
	after time.Duration // délai demandé par le service (429)
}

func (e *sendError) Error() string { return e.msg }

// fail interprète une réponse en erreur : 429 et 5xx sont passagers, le reste est définitif.
func fail(service string, status int, after time.Duration, refused, detail string) error {
	switch {
	case status == http.StatusTooManyRequests:
		return &sendError{msg: "trop de messages, " + service + " limite l'envoi", retry: true, after: after}
	case status >= 500:
		return &sendError{msg: fmt.Sprintf("%s indisponible (%d)", service, status), retry: true}
	case refused != "":
		return &sendError{msg: refused}
	case detail != "":
		return &sendError{msg: "message refusé par " + service + " : " + detail}
	}
	return &sendError{msg: fmt.Sprintf("message refusé par %s (%d)", service, status)}
}

// post envoie un corps JSON et renvoie le code HTTP et le début de la réponse.
func (m *Manager) post(ctx context.Context, url string, body any, header http.Header) (int, []byte, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, &sendError{msg: secret.Describe(err)}
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Vigie (+https://github.com/Karelisio/Vigie)")
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, nil, &sendError{msg: secret.Describe(err), retry: true}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	return resp.StatusCode, data, nil
}

// cut tronque un texte à n caractères.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---- Telegram ----

func (m *Manager) telegram(ctx context.Context, c Channel, msg Message) error {
	icon := map[string]string{"critical": "🔴", "warning": "🟠"}[msg.Severity]
	switch {
	case msg.Resolved:
		icon = "✅"
	case icon == "":
		icon = "🔵"
	}
	text := icon + " <b>" + html.EscapeString(cut(msg.Title, 250)) + "</b>"
	if msg.Body != "" {
		text += "\n" + html.EscapeString(cut(msg.Body, 3500))
	}
	text += "\n<i>" + html.EscapeString(m.server) + "</i>"
	status, data, err := m.post(ctx, m.telegramAPI+"/bot"+c.BotToken+"/sendMessage", map[string]any{
		"chat_id":              c.ChatID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
	}, nil)
	if err != nil {
		return err
	}
	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(data, &r)
	if status == http.StatusOK && r.OK {
		return nil
	}
	refused := ""
	switch {
	case status == http.StatusUnauthorized || status == http.StatusNotFound:
		refused = "token de bot refusé par Telegram"
	case strings.Contains(strings.ToLower(r.Description), "chat not found"):
		refused = "chat introuvable : écris d'abord au bot, puis vérifie le chat id"
	case status == http.StatusForbidden:
		refused = "le bot ne peut pas écrire dans ce chat (bloqué, jamais démarré ou retiré du groupe)"
	}
	return fail("Telegram", status, time.Duration(r.Parameters.RetryAfter)*time.Second, refused, r.Description)
}

// ---- Discord ----

// color : couleur de la barre de l'embed selon la sévérité.
func color(msg Message) int {
	switch {
	case msg.Resolved:
		return 0x2E7D32
	case msg.Severity == "critical":
		return 0xC62828
	case msg.Severity == "warning":
		return 0xEF6C00
	}
	return 0x00838F
}

func (m *Manager) discord(ctx context.Context, c Channel, msg Message) error {
	embed := map[string]any{
		"title":     cut(msg.Title, 256),
		"color":     color(msg),
		"timestamp": msg.TS.UTC().Format(time.RFC3339),
		"footer":    map[string]string{"text": cut(m.server, 256)},
	}
	if msg.Body != "" {
		embed["description"] = cut(msg.Body, 4000)
	}
	status, data, err := m.post(ctx, c.WebhookURL, map[string]any{
		"username": "Vigie",
		"embeds":   []any{embed},
		// Aucune mention (@everyone…) même si une ligne de log en contient.
		"allowed_mentions": map[string]any{"parse": []string{}},
	}, nil)
	if err != nil {
		return err
	}
	if status/100 == 2 {
		return nil
	}
	var r struct {
		Message    string  `json:"message"`
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(data, &r)
	refused := ""
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
		refused = "webhook refusé par Discord (supprimé ou URL incorrecte)"
	}
	return fail("Discord", status, time.Duration(r.RetryAfter*float64(time.Second)), refused, r.Message)
}

// ---- ntfy ----

// priority et tag : priorité ntfy (1 à 5) et émoji selon la sévérité.
func priority(msg Message) (int, string) {
	switch {
	case msg.Resolved:
		return 2, "white_check_mark"
	case msg.Severity == "critical":
		return 4, "rotating_light"
	case msg.Severity == "warning":
		return 3, "warning"
	}
	return 3, "information_source"
}

func (m *Manager) ntfy(ctx context.Context, c Channel, msg Message) error {
	prio, tag := priority(msg)
	header := http.Header{}
	if c.Token != "" {
		header.Set("Authorization", "Bearer "+c.Token)
	}
	// Publication en JSON (à la racine du serveur) : accents et retours à la ligne sans encodage d'en-tête.
	status, data, err := m.post(ctx, c.Server+"/", map[string]any{
		"topic":    c.Topic,
		"title":    cut(msg.Title, 250),
		"message":  strings.TrimSpace(cut(msg.Body, 3500) + "\n" + m.server),
		"priority": prio,
		"tags":     []string{tag},
	}, header)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return nil
	}
	var r struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &r)
	refused := ""
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		refused = "accès refusé par ntfy : vérifie le jeton (topic protégé ?)"
	case http.StatusNotFound:
		refused = "aucun serveur ntfy à cette adresse"
	}
	return fail("ntfy", status, 0, refused, r.Error)
}
