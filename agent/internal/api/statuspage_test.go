package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/alerts"
	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/domains"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/statuspage"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

// newPublicServer : serveur complet avec page d'état (un check « en ligne », au nom interne) et suivi des domaines.
func newPublicServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{
		Token: testToken, ServerName: "vps", PublicURL: "https://vigie.15122000.xyz", Ignore: map[string]bool{}, RateLimit: 100, RateBurst: 100,
		AlertCPU: 90, AlertRAM: 90, AlertDisk: 90, AlertDuration: 5 * time.Minute, AlertCooldown: 30 * time.Minute, CheckFailures: 2,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dk, _ := docker.New("unix:///nonexistent.sock")
	set := settings.Load(ctx, st, cfg)
	mon := monitor.New(cfg, dk, system.New("/proc", "/sys", "/"), st, set, log)
	s := New(cfg, mon, dk, st, nil, alerts.New(ctx, cfg, mon, dk, st, log), nil, log, "test")
	last := store.CheckResult{TS: time.Now().UnixMilli(), OK: true, Status: 200}
	list := []checks.Status{{Check: store.Check{ID: 7, Name: "aiostreams-interne", URL: "https://aiostreams.15122000.xyz/x", Enabled: true}, Last: &last}}
	s.StatusPage, err = statuspage.New(ctx, st, statuspage.Options{Checks: func() []checks.Status { return list }, DownAfter: 2})
	if err != nil {
		t.Fatal(err)
	}
	s.Domains = domains.New(ctx, st, log, domains.Options{PublicURL: cfg.PublicURL, Manual: func() []string { return set.Get().Domains }})
	return s.Handler(), st
}

// anon envoie une requête sans token.
func anon(h http.Handler, method, path string) *httptest.ResponseRecorder {
	return anonFrom(h, "203.0.113.9", method, path)
}

func anonFrom(h http.Handler, ip, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = ip + ":4000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStatusPagePublicAccess(t *testing.T) {
	h, st := newPublicServer(t)
	code, body := call(t, h, "GET", "/api/v1/statuspage", "")
	var cfg struct {
		statuspage.Config
		URL string `json:"url"`
	}
	if code != 200 || json.Unmarshal([]byte(body), &cfg) != nil || cfg.Enabled || len(cfg.Slug) < 16 || cfg.URL != "https://vigie.15122000.xyz/status/"+cfg.Slug {
		t.Fatalf("GET : %d %s", code, body)
	}
	slug := cfg.Slug

	// Désactivée ou mauvais identifiant : même 404, sans rien révéler.
	disabled := anon(h, "GET", "/status/"+slug)
	wrong := anon(h, "GET", "/status/"+slug[:len(slug)-1]+"x")
	wrongJSON := anon(h, "GET", "/status/nimporte-quoi-0123456789.json")
	for _, w := range []*httptest.ResponseRecorder{disabled, wrong, wrongJSON} {
		if w.Code != 404 || w.Body.String() != "Page introuvable\n" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("404 attendu : %d %q %v", w.Code, w.Body.String(), w.Header())
		}
	}

	code, body = call(t, h, "PUT", "/api/v1/statuspage", `{"enabled":true,"slug":"impose","url":"x","title":"Mes services",
		"items":[{"kind":"check","ref":"7","label":"Recherche"}]}`)
	if code != 200 || !strings.Contains(body, `"slug":"`+slug+`"`) || !strings.Contains(body, `"show_uptime":true`) {
		t.Fatalf("PUT : %d %s", code, body)
	}
	if code, body = call(t, h, "PUT", "/api/v1/statuspage", `{"items":[{"kind":"check","ref":"7","label":""}]}`); code != 400 || !strings.Contains(body, "nom public") {
		t.Fatalf("PUT invalide : %d %s", code, body)
	}
	if code, _ = call(t, h, "PUT", "/api/v1/statuspage", `{"inconnu":1}`); code != 400 {
		t.Fatalf("champ inconnu accepté : %d", code)
	}

	page := anon(h, "GET", "/status/"+slug)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Recherche") || !strings.Contains(page.Body.String(), "Tous les services fonctionnent") {
		t.Fatalf("page : %d %s", page.Code, page.Body.String())
	}
	hd := page.Header()
	if !strings.HasPrefix(hd.Get("Content-Type"), "text/html") || !strings.HasPrefix(hd.Get("Content-Security-Policy"), "default-src 'none'; style-src 'sha256-") ||
		hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "no-referrer" || hd.Get("Cache-Control") != "max-age=30" ||
		hd.Get("X-Frame-Options") != "DENY" || !strings.Contains(hd.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("en-têtes : %v", hd)
	}
	js := anon(h, "GET", "/status/"+slug+".json")
	var pub statuspage.PublicJSON
	if js.Code != 200 || json.Unmarshal(js.Body.Bytes(), &pub) != nil || pub.Status != "operational" || len(pub.Items) != 1 ||
		pub.Items[0].Name != "Recherche" || pub.Items[0].Status != "online" || len(pub.Items[0].Days) != statuspage.Days || pub.Title != "Mes services" {
		t.Fatalf("JSON : %d %s", js.Code, js.Body.String())
	}
	if !strings.HasPrefix(js.Header().Get("Content-Type"), "application/json") || js.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("en-têtes JSON : %v", js.Header())
	}
	for _, out := range []string{page.Body.String(), js.Body.String()} {
		for _, secret := range []string{"aiostreams-interne", "15122000", testToken, slug} {
			if strings.Contains(out, secret) {
				t.Fatalf("fuite de %q", secret)
			}
		}
	}
	if head := anon(h, "HEAD", "/status/"+slug); head.Code != 200 {
		t.Fatalf("HEAD : %d", head.Code)
	}

	// Nouvel identifiant : l'ancien lien cesse de fonctionner.
	code, body = call(t, h, "POST", "/api/v1/statuspage/regenerate", "")
	if code != 200 || json.Unmarshal([]byte(body), &cfg) != nil || cfg.Slug == slug || !cfg.Enabled || cfg.URL != "https://vigie.15122000.xyz/status/"+cfg.Slug {
		t.Fatalf("régénération : %d %s", code, body)
	}
	if w := anon(h, "GET", "/status/"+slug); w.Code != 404 {
		t.Fatalf("ancien lien : %d", w.Code)
	}
	if w := anon(h, "GET", "/status/"+cfg.Slug); w.Code != 200 {
		t.Fatalf("nouveau lien : %d", w.Code)
	}
	audit, _ := st.AuditLog(context.Background(), 10)
	if len(audit) != 2 || audit[0].Action != "statuspage-regenerate" || audit[1].Action != "statuspage" || strings.Contains(audit[0].Target+audit[0].Result, cfg.Slug) {
		t.Fatalf("journal : %+v", audit)
	}
}

// Seules les pages publiques échappent au token : le reste de l'API reste protégé.
func TestPublicStatusExemptionIsNarrow(t *testing.T) {
	h, _ := newPublicServer(t)
	for i, c := range []struct{ method, path string }{
		{"GET", "/api/v1/statuspage"},
		{"POST", "/api/v1/statuspage/regenerate"},
		{"GET", "/api/v1/domains"},
		{"GET", "/api/v1/info"},
		{"POST", "/status/abcdefghijklmnopqrstuv"},
		{"PUT", "/status/abcdefghijklmnopqrstuv"},
		{"GET", "/status/"},
		{"GET", "/status"},
		{"GET", "/status/a/b"},
		{"GET", "/status/a/../../api/v1/info"},
		{"GET", "/status/a%2F..%2F..%2Fapi%2Fv1%2Finfo"},
		{"GET", "/statusx/abc"},
	} {
		// Une adresse par requête : 10 échecs d'authentification bloquent l'adresse.
		if w := anonFrom(h, fmt.Sprintf("198.51.100.%d", i+1), c.method, c.path); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s sans token : %d", c.method, c.path, w.Code)
		}
	}
	if !isPublicStatus(httptest.NewRequest("GET", "/status/abc.json", nil)) || isPublicStatus(httptest.NewRequest("DELETE", "/status/abc", nil)) {
		t.Error("isPublicStatus")
	}
}

func TestPublicStatusRateLimited(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{cfg: &config.Config{}, st: st, log: log, limiter: newLimiter(0.001, 3)}
	h := s.Handler()
	codes := []int{}
	for range 5 {
		codes = append(codes, anon(h, "GET", "/status/abcdefghijklmnopqrstuv").Code)
	}
	if codes[0] != 404 || codes[3] != http.StatusTooManyRequests || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("codes : %v", codes)
	}
}

// Accès partagé : la liste des domaines est lisible, la configuration de la page d'état non.
func TestReadOnlyDomainsAndStatusPage(t *testing.T) {
	h, _ := newPublicServer(t)
	code, body := call(t, h, "POST", "/api/v1/tokens", `{"label":"Lucas"}`)
	var created struct {
		Token string `json:"token"`
	}
	if code != 201 || json.Unmarshal([]byte(body), &created) != nil {
		t.Fatalf("création d'accès : %d %s", code, body)
	}
	ro := func(method, path string) int {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+created.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := ro("GET", "/api/v1/domains"); c != 200 {
		t.Errorf("GET domaines en lecture seule : %d", c)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/domains/check"}, {"GET", "/api/v1/statuspage"}, {"PUT", "/api/v1/statuspage"}, {"POST", "/api/v1/statuspage/regenerate"},
	} {
		if got := ro(c.method, c.path); got != http.StatusForbidden {
			t.Errorf("%s %s en lecture seule : %d", c.method, c.path, got)
		}
	}
}

func TestDomainsRoute(t *testing.T) {
	h, _ := newPublicServer(t)
	if code, body := call(t, h, "PUT", "/api/v1/settings", `{"domains":["https://www.Example.co.uk/x"],"domain_days":45}`); code != 200 ||
		!strings.Contains(body, `"domains":["www.example.co.uk"]`) || !strings.Contains(body, `"domain_days":45`) {
		t.Fatalf("réglages : %d %s", code, body)
	}
	code, body := call(t, h, "GET", "/api/v1/domains", "")
	var list []domains.Status
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list) != 2 {
		t.Fatalf("GET : %d %s", code, body)
	}
	if list[0].Domain != "15122000.xyz" || list[0].Source != "auto" || list[0].Services[0] != "Vigie" ||
		list[1].Domain != "example.co.uk" || list[1].Source != "manual" || list[1].CheckedAt != 0 {
		t.Fatalf("domaines : %+v", list)
	}
	for _, field := range []string{`"domain"`, `"source"`, `"expires":0`, `"days_left":null`, `"registered":0`, `"registrar":""`, `"status":[]`, `"checked_at":0`} {
		if !strings.Contains(body, field) {
			t.Errorf("champ %s absent : %s", field, body)
		}
	}
}
