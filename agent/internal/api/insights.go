package api

import (
	"net/http"
	"time"

	"github.com/karelisio/vigie/agent/internal/report"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/traffic"
)

// ---- Trafic du mois, top consommateurs, rapport hebdomadaire ----

func (s *Server) trafficReport(w http.ResponseWriter, r *http.Request) {
	rep, err := traffic.Build(r.Context(), s.st, s.mon.Settings().Get().NetQuotaTB, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) topConsumers(w http.ResponseWriter, r *http.Request) {
	rg, err := store.ParseRange(r.URL.Query().Get("range"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.st.Top(r.Context(), rg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Seuls les conteneurs encore présents sont classés (ni supprimés, ni anciens noms d'une mise à jour).
	present := map[string]bool{}
	for _, c := range s.mon.Containers() {
		present[c.Name] = true
	}
	out := []store.Consumer{}
	for _, c := range list {
		if present[c.Name] {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) weeklyReport(w http.ResponseWriter, r *http.Request) {
	// L'historique détaillé (checks, système) couvre 8 jours.
	days := queryInt(r, "days", 7, 1, 7)
	rep, err := report.Build(r.Context(), s.st, report.FromMonitor(s.mon), time.Now(), days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
