package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/store"
)

func TestReadOnlyAllowed(t *testing.T) {
	for _, c := range []struct {
		method, path string
		ok           bool
	}{
		{"GET", "/api/v1/overview", true},
		{"GET", "/api/v1/containers/comet", true},
		{"GET", "/api/v1/ws", true},
		{"POST", "/api/v1/devices", true},
		{"POST", "/api/v1/containers/comet/restart", false},
		{"PUT", "/api/v1/settings", false},
		{"GET", "/api/v1/containers/comet/logs", false},
		{"GET", "/api/v1/containers/comet/logs/stream", false},
		{"GET", "/api/v1/audit", false},
		{"GET", "/api/v1/tokens", false},
		{"GET", "/api/v1/channels", false},
		{"GET", "/api/v1/debrid", false},
		{"GET", "/api/v1/backups/config", false},
		{"GET", "/api/v1/vulns", false},
		{"GET", "/api/v1/ssh/events", false},
		{"GET", "/api/v1/ports", false},
		{"GET", "/api/v1/statuspage", false},
		{"GET", "/api/v1/storage", true},
		{"GET", "/api/v1/domains", true},
		{"POST", "/api/v1/containers/abc/rollback", false},
		// Correction des failles (agent 0.7.0) : ni consultation ni action en lecture seule.
		{"GET", "/api/v1/vulns/fixes", false},
		{"POST", "/api/v1/vulns/fix", false},
		{"POST", "/api/v1/vulns/fix-all", false},
		{"POST", "/api/v1/vulns/analyze", false},
	} {
		if got := readOnlyAllowed(httptest.NewRequest(c.method, c.path, nil)); got != c.ok {
			t.Errorf("%s %s : %v, attendu %v", c.method, c.path, got, c.ok)
		}
	}
}

func TestSharedTokens(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	admin := "secret-token-0123456789-abcdefghijkl"
	s := &Server{
		cfg: &config.Config{ServerName: "VPS", PublicURL: "https://vigie.example"}, st: st,
		tokenHash: sha256.Sum256([]byte(admin)), limiter: newLimiter(100, 100), shared: loadTokens(st),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tokens", s.createToken)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.deleteToken)
	mux.HandleFunc("GET /api/v1/overview", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"role": role(r)}) })
	mux.HandleFunc("POST /api/v1/containers/{id}/restart", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := s.wrap(mux)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := call("POST", "/api/v1/tokens", admin, `{"label":"Lucas"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("création : %d %s", w.Code, w.Body)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Link  string `json:"link"`
		QrPng string `json:"qr_png"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if !strings.HasPrefix(created.Token, "ro_") || !strings.Contains(created.Link, "lecture+seule") {
		t.Fatalf("réponse : %+v", created)
	}
	if png, err := base64.StdEncoding.DecodeString(created.QrPng); err != nil || !strings.HasPrefix(string(png), "\x89PNG") {
		t.Error("QR code absent")
	}
	if strings.Contains(st.Get(t.Context(), tokensKey), created.Token) {
		t.Fatal("jeton conservé en clair")
	}

	if w := call("GET", "/api/v1/overview", created.Token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), roleReader) {
		t.Errorf("lecture : %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/api/v1/containers/comet/restart", created.Token, ""); w.Code != http.StatusForbidden {
		t.Errorf("action en lecture seule : %d", w.Code)
	}
	if w := call("POST", "/api/v1/tokens", created.Token, `{"label":"x"}`); w.Code != http.StatusForbidden {
		t.Errorf("création par un accès partagé : %d", w.Code)
	}
	// Un redémarrage de l'agent conserve les accès.
	s.shared = loadTokens(st)
	if w := call("GET", "/api/v1/overview", created.Token, ""); w.Code != 200 {
		t.Errorf("après rechargement : %d", w.Code)
	}
	if w := call("DELETE", "/api/v1/tokens/"+created.ID, admin, ""); w.Code != 200 {
		t.Fatalf("révocation : %d", w.Code)
	}
	if w := call("GET", "/api/v1/overview", created.Token, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("jeton révoqué accepté : %d", w.Code)
	}
}
