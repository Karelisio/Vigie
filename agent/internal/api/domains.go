package api

import (
	"context"
	"net/http"
)

// getDomains renvoie les domaines suivis et le dernier résultat connu de chacun, sans interroger les registres.
func (s *Server) getDomains(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeErr(w, http.StatusServiceUnavailable, "suivi des domaines indisponible")
		return
	}
	writeJSON(w, http.StatusOK, s.Domains.State(r.Context()))
}

// checkDomains interroge les registres maintenant (2 min au plus) et renvoie le nouvel état.
func (s *Server) checkDomains(w http.ResponseWriter, r *http.Request) {
	if s.Domains == nil {
		writeErr(w, http.StatusServiceUnavailable, "suivi des domaines indisponible")
		return
	}
	// La vérification se termine même si l'appli n'attend pas la réponse.
	writeJSON(w, http.StatusOK, s.Domains.Check(context.WithoutCancel(r.Context())))
}
