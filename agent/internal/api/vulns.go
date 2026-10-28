package api

import (
	"errors"
	"net/http"

	"github.com/karelisio/vigie/agent/internal/vulns"
)

// ---- Failles connues dans les images (accès complet uniquement) ----

func (s *Server) getVulns(w http.ResponseWriter, r *http.Request) {
	if s.Vulns == nil {
		writeErr(w, http.StatusNotFound, "analyse des failles indisponible")
		return
	}
	writeJSON(w, http.StatusOK, s.Vulns.Report(r.Context()))
}

// scanVulns lance une analyse en arrière-plan (202) ; si une analyse est déjà en cours, renvoie son état.
func (s *Server) scanVulns(w http.ResponseWriter, r *http.Request) {
	if s.Vulns == nil {
		writeErr(w, http.StatusNotFound, "analyse des failles indisponible")
		return
	}
	err := s.Vulns.Start("manual")
	if errors.Is(err, vulns.ErrDisabled) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	result := "lancée"
	if errors.Is(err, vulns.ErrBusy) {
		result = "déjà en cours"
	}
	s.st.Audit(r.Context(), s.clientIP(r), "vulns.scan", "failles", result, r.UserAgent())
	writeJSON(w, http.StatusAccepted, s.Vulns.Report(r.Context()))
}
