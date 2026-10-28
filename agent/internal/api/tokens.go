package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"github.com/karelisio/vigie/agent/internal/store"
)

// ---- Accès en lecture seule : jetons secondaires créés depuis l'appli ----

const (
	tokensKey  = "shared_tokens"
	maxTokens  = 20
	roleAdmin  = "admin"
	roleReader = "readonly"
)

type sharedToken struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Hash     string `json:"hash"` // SHA-256 du jeton : le jeton lui-même n'est jamais conservé
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used"`
}

type tokenSet struct {
	st        *store.Store
	mu        sync.Mutex
	list      []sharedToken
	persisted time.Time // dernier enregistrement de LastUsed
}

func loadTokens(st *store.Store) *tokenSet {
	t := &tokenSet{st: st}
	_ = json.Unmarshal([]byte(st.Get(context.Background(), tokensKey)), &t.list)
	return t
}

func (t *tokenSet) save(ctx context.Context) error {
	b, _ := json.Marshal(t.list)
	t.persisted = time.Now()
	return t.st.Set(ctx, tokensKey, string(b))
}

// match compare l'empreinte à chaque jeton, à temps constant, et note son utilisation.
func (t *tokenSet) match(sum [32]byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	found := -1
	for i, tk := range t.list {
		h, err := hex.DecodeString(tk.Hash)
		if err == nil && subtle.ConstantTimeCompare(h, sum[:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return false
	}
	t.list[found].LastUsed = time.Now().UnixMilli()
	if time.Since(t.persisted) > 10*time.Minute {
		_ = t.save(context.Background())
	}
	return true
}

type ctxKey int

const roleKey ctxKey = 0

// role renvoie « admin » (token principal) ou « readonly » (accès partagé).
func role(r *http.Request) string {
	if v, ok := r.Context().Value(roleKey).(string); ok {
		return v
	}
	return roleAdmin
}

// readOnlyAllowed : un accès partagé consulte l'état, reçoit les notifications, mais n'agit sur rien
// et ne voit ni les logs ni les configurations qui contiennent des secrets.
func readOnlyAllowed(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/v1/devices" || p == "/api/v1/devices/test" {
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	for _, prefix := range []string{"/api/v1/tokens", "/api/v1/audit", "/api/v1/channels", "/api/v1/debrid", "/api/v1/backups"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}
	return !(strings.HasPrefix(p, "/api/v1/containers/") && (strings.HasSuffix(p, "/logs") || strings.HasSuffix(p, "/logs/stream")))
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	s.shared.mu.Lock()
	out := make([]map[string]any, 0, len(s.shared.list))
	for _, tk := range s.shared.list {
		out = append(out, map[string]any{"id": tk.ID, "label": tk.Label, "created": tk.Created, "last_used": tk.LastUsed})
	}
	s.shared.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" || len([]rune(label)) > 40 {
		writeErr(w, http.StatusBadRequest, "nom obligatoire (40 caractères au plus)")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := "ro_" + base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(sum[:4])

	s.shared.mu.Lock()
	if len(s.shared.list) >= maxTokens {
		s.shared.mu.Unlock()
		writeErr(w, http.StatusConflict, "20 accès partagés au maximum")
		return
	}
	s.shared.list = append(s.shared.list, sharedToken{ID: id, Label: label, Hash: hex.EncodeToString(sum[:]), Created: time.Now().UnixMilli()})
	err := s.shared.save(r.Context())
	s.shared.mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "token-create", label, "ok", r.UserAgent())

	q := url.Values{}
	q.Set("name", s.cfg.ServerName+" (lecture seule)")
	q.Set("url", s.cfg.PublicURL)
	q.Set("token", token)
	link := "vigie://connect?" + q.Encode()
	// Le jeton n'est renvoyé qu'ici, une seule fois.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": label, "token": token, "link": link, "qr_png": qrPNG(link)})
}

// qrPNG encode le lien en image PNG (base64) pour l'afficher dans l'appli.
func qrPNG(link string) string {
	code, err := qr.Encode(link, qr.M)
	if err != nil {
		return ""
	}
	code.Scale = 8
	return base64.StdEncoding.EncodeToString(code.PNG())
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.shared.mu.Lock()
	label := ""
	kept := s.shared.list[:0]
	for _, tk := range s.shared.list {
		if tk.ID == id {
			label = tk.Label
			continue
		}
		kept = append(kept, tk)
	}
	s.shared.list = kept
	err := s.shared.save(r.Context())
	s.shared.mu.Unlock()
	if label == "" {
		writeErr(w, http.StatusNotFound, "accès introuvable")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "token-revoke", label, "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
