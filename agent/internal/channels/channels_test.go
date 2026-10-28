package channels

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

const botToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"

func newManager(t *testing.T, st *store.Store) *Manager {
	t.Helper()
	if st == nil {
		var err error
		if st, err = store.Open(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := New(ctx, st, slog.New(slog.NewTextHandler(io.Discard, nil)), "vps-test")
	m.backoff = 10 * time.Millisecond
	return m
}

// request est une requête reçue par un faux service.
type request struct {
	Path, Auth string
	Body       map[string]any
}

// fake simule un service : chaque requête est enregistrée, la réponse vient de reply.
type fake struct {
	*httptest.Server
	mu    sync.Mutex
	reqs  []request
	reply func(n int) (int, string)
	got   chan struct{}
}

func newFake(t *testing.T, reply func(n int) (int, string)) *fake {
	f := &fake{reply: reply, got: make(chan struct{}, 16)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.reqs = append(f.reqs, request{r.URL.Path, r.Header.Get("Authorization"), body})
		n := len(f.reqs)
		f.mu.Unlock()
		code, out := f.reply(n)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, out)
		f.got <- struct{}{}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fake) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.reqs...)
}

// wait attend n requêtes (envoi asynchrone).
func (f *fake) wait(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-f.got:
		case <-time.After(3 * time.Second):
			t.Fatalf("%d requête(s) attendue(s), %d reçue(s)", n, len(f.requests()))
		}
	}
}

func ok(n int) (int, string) { return http.StatusOK, `{"ok":true}` }

func TestTelegramRequest(t *testing.T) {
	f := newFake(t, ok)
	m := newManager(t, nil)
	m.telegramAPI = f.URL
	if _, err := m.Update(context.Background(), Config{Channels: []Channel{{Type: "telegram", Enabled: true, BotToken: botToken, ChatID: "-1001234567890"}}}); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "critical", Title: "web est tombé", Body: "Arrêté avec le code 1 <oom>", TS: time.Now()})
	f.wait(t, 1)
	r := f.requests()[0]
	if r.Path != "/bot"+botToken+"/sendMessage" {
		t.Fatalf("chemin %q", r.Path)
	}
	text, _ := r.Body["text"].(string)
	if r.Body["chat_id"] != "-1001234567890" || r.Body["parse_mode"] != "HTML" ||
		!strings.Contains(text, "<b>web est tombé</b>") || !strings.Contains(text, "&lt;oom&gt;") || !strings.Contains(text, "vps-test") {
		t.Fatalf("corps inattendu : %+v", r.Body)
	}
}

func TestDiscordEmbed(t *testing.T) {
	f := newFake(t, func(int) (int, string) { return http.StatusNoContent, "" })
	m := newManager(t, nil)
	c := Channel{Type: TypeDiscord, WebhookURL: f.URL + "/api/webhooks/1/abc"}
	if err := m.send(context.Background(), c, Message{Severity: "warning", Title: "Disque à 91 %", Body: "@everyone", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r := f.requests()[0]
	embeds, _ := r.Body["embeds"].([]any)
	if r.Path != "/api/webhooks/1/abc" || len(embeds) != 1 {
		t.Fatalf("requête inattendue : %+v", r)
	}
	e := embeds[0].(map[string]any)
	if e["title"] != "Disque à 91 %" || e["color"] != float64(0xEF6C00) || e["description"] != "@everyone" {
		t.Fatalf("embed inattendu : %+v", e)
	}
	if am, _ := r.Body["allowed_mentions"].(map[string]any); am == nil || len(am["parse"].([]any)) != 0 {
		t.Fatalf("mentions non désactivées : %+v", r.Body)
	}
	if err := m.send(context.Background(), c, Message{Severity: "info", Resolved: true, Title: "ok"}); err != nil {
		t.Fatal(err)
	}
	if e := f.requests()[1].Body["embeds"].([]any)[0].(map[string]any); e["color"] != float64(0x2E7D32) || e["description"] != nil {
		t.Fatalf("résolution : %+v", e)
	}
}

func TestNtfyPriorityAndToken(t *testing.T) {
	f := newFake(t, ok)
	m := newManager(t, nil)
	if _, err := m.Update(context.Background(), Config{Channels: []Channel{{Type: "ntfy", Enabled: true, Server: f.URL + "/", Topic: "vigie-alertes", Token: "tk_abcdefghijklmnop"}}}); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "critical", Title: "Processeur à 97 %", Body: "Au-dessus du seuil."})
	m.Notify(Message{Severity: "info", Resolved: true, Title: "Processeur revenu à la normale"})
	f.wait(t, 2)
	reqs := f.requests()
	if reqs[0].Path != "/" || reqs[0].Auth != "Bearer tk_abcdefghijklmnop" || reqs[0].Body["topic"] != "vigie-alertes" {
		t.Fatalf("requête inattendue : %+v", reqs[0])
	}
	tags := reqs[0].Body["tags"].([]any)
	if reqs[0].Body["priority"] != float64(4) || tags[0] != "rotating_light" || reqs[0].Body["message"] != "Au-dessus du seuil.\nvps-test" {
		t.Fatalf("alerte critique : %+v", reqs[0].Body)
	}
	if reqs[1].Body["priority"] != float64(2) || reqs[1].Body["tags"].([]any)[0] != "white_check_mark" {
		t.Fatalf("résolution : %+v", reqs[1].Body)
	}
}

// Les secrets ne sortent que masqués ; un PUT avec la valeur masquée ou vide les conserve.
func TestSecretsMaskedAndKept(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := newManager(t, st)
	ctx := context.Background()
	out, err := m.Update(ctx, Config{Channels: []Channel{
		{Type: "telegram", BotToken: botToken, ChatID: "42"},
		{Type: "ntfy", Topic: "alertes", Token: "tk_abcdefghijklmnop"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Channels[0].BotToken != "••••Dsaw" || out.Channels[1].Token != "••••mnop" || out.Channels[1].Server != "https://ntfy.sh" {
		t.Fatalf("masquage : %+v", out.Channels)
	}
	raw, _ := json.Marshal(m.Get())
	if strings.Contains(string(raw), "AAHdq") || strings.Contains(string(raw), "tk_abc") {
		t.Fatalf("secret divulgué : %s", raw)
	}
	// Renvoi de la configuration masquée (Telegram) et vide (ntfy) : secrets conservés.
	in := m.Get()
	in.Channels[1].Token = ""
	in.Channels[0].Enabled = true
	if _, err := m.Update(ctx, in); err != nil {
		t.Fatal(err)
	}
	// Nouvelle instance : configuration relue en base.
	again := newManager(t, st)
	tg, nt := find(again.snapshot(), "telegram"), find(again.snapshot(), "ntfy")
	if tg.BotToken != botToken || !tg.Enabled || nt.Token != "tk_abcdefghijklmnop" {
		t.Fatalf("secrets perdus : %+v %+v", tg, nt)
	}
	// Retirer un canal efface ses secrets.
	if _, err := m.Update(ctx, Config{Channels: []Channel{in.Channels[1]}}); err != nil {
		t.Fatal(err)
	}
	if find(m.snapshot(), "telegram").BotToken != "" {
		t.Fatal("canal retiré toujours présent")
	}
}

func TestSeverityFilter(t *testing.T) {
	f := newFake(t, ok)
	m := newManager(t, nil)
	cfg := Config{Channels: []Channel{{Type: "ntfy", Enabled: true, MinLevel: "critical", Server: f.URL, Topic: "t"}}}
	if _, err := m.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "warning", Title: "avertissement"})
	m.Notify(Message{Severity: "info", Resolved: true, Title: "résolu"})
	m.Notify(Message{Severity: "critical", Title: "critique"})
	f.wait(t, 1)
	time.Sleep(50 * time.Millisecond)
	if reqs := f.requests(); len(reqs) != 1 || reqs[0].Body["title"] != "critique" {
		t.Fatalf("seule l'alerte critique doit partir : %+v", reqs)
	}
	// Canal désactivé : rien n'est envoyé.
	cfg.Channels[0].Enabled = false
	if _, err := m.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "critical", Title: "ignorée"})
	time.Sleep(50 * time.Millisecond)
	if n := len(f.requests()); n != 1 {
		t.Fatalf("canal désactivé : %d requêtes", n)
	}
}

// Une erreur passagère (5xx) est retentée ; une erreur définitive (4xx) ne l'est pas.
func TestRetry(t *testing.T) {
	f := newFake(t, func(n int) (int, string) {
		if n == 1 {
			return http.StatusBadGateway, ""
		}
		return http.StatusOK, "{}"
	})
	m := newManager(t, nil)
	if _, err := m.Update(context.Background(), Config{Channels: []Channel{{Type: "ntfy", Enabled: true, Server: f.URL, Topic: "t"}}}); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "critical", Title: "x"})
	f.wait(t, 2)

	refused := newFake(t, func(int) (int, string) { return http.StatusForbidden, `{"error":"forbidden"}` })
	if _, err := m.Update(context.Background(), Config{Channels: []Channel{{Type: "ntfy", Enabled: true, Server: refused.URL, Topic: "t"}}}); err != nil {
		t.Fatal(err)
	}
	m.Notify(Message{Severity: "critical", Title: "x"})
	refused.wait(t, 1)
	time.Sleep(80 * time.Millisecond)
	if n := len(refused.requests()); n != 1 {
		t.Fatalf("erreur définitive retentée %d fois", n)
	}
}

func TestTestErrors(t *testing.T) {
	cases := []struct {
		code int
		body string
		want string
	}{
		{401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "token de bot refusé par Telegram"},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, "chat introuvable : écris d'abord au bot, puis vérifie le chat id"},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, "le bot ne peut pas écrire dans ce chat (bloqué, jamais démarré ou retiré du groupe)"},
	}
	for _, c := range cases {
		f := newFake(t, func(int) (int, string) { return c.code, c.body })
		m := newManager(t, nil)
		m.telegramAPI = f.URL
		if _, err := m.Update(context.Background(), Config{Channels: []Channel{{Type: "telegram", BotToken: botToken, ChatID: "42"}}}); err != nil {
			t.Fatal(err)
		}
		// Canal désactivé : le test par type l'essaie quand même ; sans type, il est ignoré.
		res, err := m.Test(context.Background(), "telegram")
		if err != nil || len(res) != 1 || res[0].OK || res[0].Error != c.want {
			t.Errorf("%d : %+v %v", c.code, res, err)
		}
		if _, err := m.Test(context.Background(), ""); err == nil || err.Error() != "aucun canal actif" {
			t.Errorf("sans canal actif : %v", err)
		}
	}
	m := newManager(t, nil)
	if _, err := m.Test(context.Background(), "discord"); err == nil || err.Error() != "canal discord non configuré" {
		t.Errorf("canal absent : %v", err)
	}
	// Serveur injoignable : le jeton (dans l'URL) n'apparaît pas dans l'erreur.
	f := newFake(t, ok)
	f.Close()
	m.telegramAPI = f.URL
	err := m.send(context.Background(), Channel{Type: TypeTelegram, BotToken: botToken, ChatID: "42"}, Message{Title: "x"})
	if err == nil || strings.Contains(err.Error(), "AAHdq") {
		t.Fatalf("erreur réseau : %v", err)
	}
	discord := m.send(context.Background(), Channel{Type: TypeDiscord, WebhookURL: f.URL + "/api/webhooks/1/SECRET"}, Message{Title: "x"})
	if discord == nil || strings.Contains(discord.Error(), "SECRET") {
		t.Fatalf("erreur réseau Discord : %v", discord)
	}
}

func TestValidation(t *testing.T) {
	m := newManager(t, nil)
	cases := []struct {
		ch   Channel
		want string
	}{
		{Channel{Type: "slack"}, `type de canal inconnu "slack" (telegram, discord, ntfy)`},
		{Channel{Type: "telegram", ChatID: "1"}, "token du bot Telegram manquant"},
		{Channel{Type: "telegram", BotToken: "abc", ChatID: "1"}, "token de bot Telegram invalide (format 123456789:AAH…, donné par @BotFather)"},
		{Channel{Type: "telegram", BotToken: botToken, ChatID: "mon chat"}, "chat id Telegram invalide (nombre comme 123456789 ou -100…, ou @nom_du_canal)"},
		{Channel{Type: "discord", WebhookURL: "https://example.com/api/webhooks/1/abc"}, "URL de webhook Discord invalide (https://discord.com/api/webhooks/…)"},
		{Channel{Type: "discord", WebhookURL: "http://discord.com/api/webhooks/1/abc"}, "URL de webhook Discord invalide (https://discord.com/api/webhooks/…)"},
		{Channel{Type: "ntfy", Topic: "mon topic"}, "topic ntfy invalide (lettres, chiffres, - et _, 64 caractères au plus)"},
		{Channel{Type: "ntfy", Server: "ftp://ntfy.sh", Topic: "t"}, "adresse du serveur ntfy invalide (ex. https://ntfy.sh)"},
		{Channel{Type: "ntfy", Topic: "t", MinLevel: "warning"}, "niveau inconnu (all ou critical)"},
	}
	for _, c := range cases {
		if _, err := m.Update(context.Background(), Config{Channels: []Channel{c.ch}}); err == nil || err.Error() != c.want {
			t.Errorf("%+v : %v, attendu %q", c.ch, err, c.want)
		}
	}
	dup := Config{Channels: []Channel{{Type: "ntfy", Topic: "a"}, {Type: "ntfy", Topic: "b"}}}
	if _, err := m.Update(context.Background(), dup); err == nil || err.Error() != "canal ntfy en double" {
		t.Errorf("doublon : %v", err)
	}
	good := Channel{Type: "discord", WebhookURL: "https://discord.com/api/webhooks/123/abc-DEF_1", Enabled: true}
	if _, err := m.Update(context.Background(), Config{Channels: []Channel{good}}); err != nil {
		t.Errorf("webhook valide refusé : %v", err)
	}
}
