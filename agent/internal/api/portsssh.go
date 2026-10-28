package api

import (
	"context"
	"net/http"
	"time"
)

// ports renvoie les ports en écoute sur l'hôte ; ?refresh=1 (ou aucune analyse encore faite) les analyse maintenant.
func (s *Server) ports(w http.ResponseWriter, r *http.Request) {
	if s.Ports == nil {
		writeErr(w, http.StatusServiceUnavailable, "analyse des ports indisponible")
		return
	}
	rep := s.Ports.Report()
	if rep.TS == 0 || r.URL.Query().Get("refresh") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		rep = s.Ports.Scan(ctx)
	}
	writeJSON(w, http.StatusOK, rep)
}

// sshSummary renvoie l'état de la lecture du journal SSH, les statistiques et les listes.
func (s *Server) sshSummary(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveillance SSH indisponible")
		return
	}
	out, err := s.SSH.Summary(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lecture des connexions SSH : "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// sshEvents renvoie les derniers événements SSH (?limit=, 100 par défaut, 500 au plus).
func (s *Server) sshEvents(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil {
		writeErr(w, http.StatusServiceUnavailable, "surveillance SSH indisponible")
		return
	}
	list, err := s.SSH.Events(r.Context(), queryInt(r, "limit", 100, 1, 500))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "lecture des connexions SSH : "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
