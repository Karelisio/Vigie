package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/alerts"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/debrid"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

const testToken = "secret-token-0123456789-abcdefghijkl"

func newTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{
		Token: testToken, ServerName: "vps", Ignore: map[string]bool{}, RateLimit: 100, RateBurst: 100,
		AlertCPU: 90, AlertRAM: 90, AlertDisk: 90, AlertDuration: 5 * time.Minute, AlertCooldown: 30 * time.Minute,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dk, _ := docker.New("unix:///nonexistent.sock")
	mon := monitor.New(cfg, dk, system.New("/proc", "/sys", "/"), st, settings.Load(ctx, st, cfg), log)
	s := New(cfg, mon, dk, st, nil, alerts.New(ctx, cfg, mon, dk, st, log), nil, log, "test")
	s.Debrid = debrid.New(ctx, st, log)
	return s.Handler(), st
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// Les secrets ne ressortent jamais en clair : ni dans les réponses, ni dans le journal des actions.
func TestSecretRoutes(t *testing.T) {
	h, st := newTestServer(t)
	const bot = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	code, body := call(t, h, "PUT", "/api/v1/channels",
		`{"channels":[{"type":"telegram","enabled":true,"min_level":"critical","bot_token":"`+bot+`","chat_id":"42"}]}`)
	if code != 200 || strings.Contains(body, "AAHdq") || !strings.Contains(body, `"bot_token":"••••Dsaw"`) {
		t.Fatalf("PUT canaux : %d %s", code, body)
	}
	if code, body = call(t, h, "GET", "/api/v1/channels", ""); code != 200 || strings.Contains(body, "AAHdq") {
		t.Fatalf("GET canaux : %d %s", code, body)
	}
	code, body = call(t, h, "PUT", "/api/v1/channels", `{"channels":[{"type":"telegram","bot_token":"nope","chat_id":"42"}]}`)
	if code != 400 || !strings.Contains(body, "token de bot Telegram invalide") {
		t.Fatalf("PUT invalide : %d %s", code, body)
	}
	if code, body = call(t, h, "POST", "/api/v1/channels/test", `{"type":"discord"}`); code != 400 || !strings.Contains(body, "canal discord non configuré") {
		t.Fatalf("test d'un canal absent : %d %s", code, body)
	}

	code, body = call(t, h, "PUT", "/api/v1/debrid/accounts", `[{"provider":"realdebrid","label":"Perso","key":""}]`)
	if code != 400 || !strings.Contains(body, "clé API manquante pour Perso") {
		t.Fatalf("PUT comptes : %d %s", code, body)
	}
	code, body = call(t, h, "GET", "/api/v1/debrid", "")
	var state debrid.State
	if code != 200 || json.Unmarshal([]byte(body), &state) != nil || state.Accounts == nil || len(state.Accounts) != 0 {
		t.Fatalf("GET debrid : %d %s", code, body)
	}
	if code, _ = call(t, h, "POST", "/api/v1/debrid/check", ""); code != 200 {
		t.Fatalf("POST check : %d", code)
	}

	entries, err := st.AuditLog(context.Background(), 10)
	if err != nil || len(entries) != 1 || entries[0].Action != "channels" || entries[0].Target != "telegram" {
		t.Fatalf("journal : %+v %v", entries, err)
	}
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "AAHdq") {
		t.Fatalf("secret dans le journal : %s", raw)
	}
}
