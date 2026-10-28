package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/karelisio/vigie/agent/internal/debrid"
)

// getDebrid renvoie l'état des comptes debrid, clés masquées.
func (s *Server) getDebrid(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Debrid.State())
}

// putDebridAccounts remplace la liste des comptes (clé vide ou masquée : clé actuelle conservée), puis les vérifie.
func (s *Server) putDebridAccounts(w http.ResponseWriter, r *http.Request) {
	var in []debrid.Account
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	// La vérification se termine même si l'appli n'attend pas la réponse.
	out, err := s.Debrid.Update(context.WithoutCancel(r.Context()), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "debrid", strconv.Itoa(len(out.Accounts))+" compte(s)", "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, out)
}

// checkDebrid vérifie tous les comptes maintenant.
func (s *Server) checkDebrid(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Debrid.Check(context.WithoutCancel(r.Context())))
}
