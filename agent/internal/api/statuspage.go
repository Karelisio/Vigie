package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/statuspage"
)

// ---- Configuration de la page d'état (token principal uniquement) ----

// statusPageOut : configuration et adresse publique complète de la page.
type statusPageOut struct {
	statuspage.Config
	URL string `json:"url"` // VIGIE_PUBLIC_URL + /status/<slug>, vide si l'adresse publique n'est pas définie
}

func (s *Server) statusPageOut(c statuspage.Config) statusPageOut {
	out := statusPageOut{Config: c}
	if s.cfg.PublicURL != "" {
		out.URL = s.cfg.PublicURL + "/status/" + c.Slug
	}
	return out
}

func (s *Server) getStatusPage(w http.ResponseWriter, r *http.Request) {
	if s.StatusPage == nil {
		writeErr(w, http.StatusServiceUnavailable, "page d'état indisponible")
		return
	}
	writeJSON(w, http.StatusOK, s.statusPageOut(s.StatusPage.Get()))
}

// putStatusPage remplace la configuration (champs absents : valeur actuelle). slug et url sont ignorés.
func (s *Server) putStatusPage(w http.ResponseWriter, r *http.Request) {
	if s.StatusPage == nil {
		writeErr(w, http.StatusServiceUnavailable, "page d'état indisponible")
		return
	}
	in := s.statusPageOut(s.StatusPage.Get())
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	out, err := s.StatusPage.Update(r.Context(), in.Config)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	state := "désactivée"
	if out.Enabled {
		state = "activée"
	}
	s.st.Audit(r.Context(), s.clientIP(r), "statuspage", "page d'état", state, r.UserAgent())
	writeJSON(w, http.StatusOK, s.statusPageOut(out))
}

// regenerateStatusPage change l'identifiant secret : l'ancien lien cesse de fonctionner.
func (s *Server) regenerateStatusPage(w http.ResponseWriter, r *http.Request) {
	if s.StatusPage == nil {
		writeErr(w, http.StatusServiceUnavailable, "page d'état indisponible")
		return
	}
	out, err := s.StatusPage.Regenerate(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "statuspage-regenerate", "page d'état", "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, s.statusPageOut(out))
}

// ---- Page publique : sans token, à l'adresse secrète ----

// isPublicStatus reconnaît les seules routes servies sans token : GET /status/<identifiant>[.json].
func isPublicStatus(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	seg, ok := strings.CutPrefix(r.URL.Path, "/status/")
	return ok && seg != "" && !strings.Contains(seg, "/")
}

func publicHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", statuspage.CSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// publicStatus sert la page (HTML) ou sa version JSON ; même réponse 404 si la page est désactivée
// ou si l'identifiant est faux.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	slug := r.PathValue("slug")
	slug, asJSON := strings.CutSuffix(slug, ".json")
	if s.StatusPage == nil || !s.StatusPage.Match(slug) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Page introuvable\n"))
		return
	}
	p := s.StatusPage.Page(r.Context(), time.Now())
	w.Header().Set("Cache-Control", "max-age=30")
	if asJSON {
		writeJSON(w, http.StatusOK, p.JSON())
		return
	}
	body, err := p.HTML()
	if err != nil {
		s.log.Error("page d'état", "erreur", err)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "erreur interne", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}
