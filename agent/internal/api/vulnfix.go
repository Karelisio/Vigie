package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/karelisio/vigie/agent/internal/updater"
	"github.com/karelisio/vigie/agent/internal/vulnfix"
)

// ---- Correction des failles (agent 0.7.0, accès complet uniquement) ----

func (s *Server) vulnFixUnavailable(w http.ResponseWriter) bool {
	if s.VulnFix == nil {
		writeErr(w, http.StatusNotFound, "correction des failles indisponible")
		return true
	}
	return false
}

// GET /api/v1/vulns/fixes : voie proposée pour chaque image.
func (s *Server) getVulnFixes(w http.ResponseWriter, r *http.Request) {
	if s.vulnFixUnavailable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.VulnFix.Plans(ctx))
}

// vulnFixErr répond à un refus détecté avant le lancement de l'opération.
func (s *Server) vulnFixErr(w http.ResponseWriter, r *http.Request, action, target string, err error) bool {
	var nf *vulnfix.NotFixableError
	code := 0
	switch {
	case errors.Is(err, vulnfix.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, vulnfix.ErrRoute):
		code = http.StatusBadRequest
	case errors.As(err, &nf), errors.Is(err, vulnfix.ErrNothing), errors.Is(err, updater.ErrSelf):
		code = http.StatusConflict
	default:
		return false
	}
	s.st.Audit(r.Context(), s.clientIP(r), action, target, "refusé : "+err.Error(), r.UserAgent())
	writeErr(w, code, err.Error())
	return true
}

func readFixRequest(w http.ResponseWriter, r *http.Request) (vulnfix.FixRequest, bool) {
	var req vulnfix.FixRequest
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return req, false
	}
	if req.ImageID == "" && req.Container == "" {
		writeErr(w, http.StatusBadRequest, "image_id ou container obligatoire")
		return req, false
	}
	return req, true
}

func fixTarget(req vulnfix.FixRequest) string {
	if req.Container != "" {
		return req.Container
	}
	return req.ImageID
}

// POST /api/v1/vulns/fix {image_id | container, route?} : corrige une image (202 : opération « vulnfix »).
func (s *Server) postVulnFix(w http.ResponseWriter, r *http.Request) {
	if s.vulnFixUnavailable(w) {
		return
	}
	req, ok := readFixRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	job, err := s.VulnFix.Fix(ctx, req)
	if err != nil && s.vulnFixErr(w, r, "vulnfix", fixTarget(req), err) {
		return
	}
	s.jobStarted(w, r, "vulnfix", fixTarget(req), job, err)
}

// POST /api/v1/vulns/fix-all : corrige l'une après l'autre toutes les images qui peuvent l'être.
func (s *Server) postVulnFixAll(w http.ResponseWriter, r *http.Request) {
	if s.vulnFixUnavailable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	job, err := s.VulnFix.FixAll(ctx)
	if err != nil && s.vulnFixErr(w, r, "vulnfix-all", "toutes les images", err) {
		return
	}
	s.jobStarted(w, r, "vulnfix-all", "toutes les images", job, err)
}

// POST /api/v1/vulns/analyze {image_id | container} : télécharge et analyse la nouvelle version sans l'installer.
func (s *Server) postVulnAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.vulnFixUnavailable(w) {
		return
	}
	req, ok := readFixRequest(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	job, err := s.VulnFix.Analyze(ctx, req)
	if err != nil && s.vulnFixErr(w, r, "vulnfix-analyze", fixTarget(req), err) {
		return
	}
	s.jobStarted(w, r, "vulnfix-analyze", fixTarget(req), job, err)
}
