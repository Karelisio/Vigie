package api

import (
	"net/http"
	"strconv"
	"time"
)

// maintenance coupe les notifications pendant `minutes` (0 : reprise immédiate).
func (s *Server) maintenance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes int `json:"minutes"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	if body.Minutes < 0 || body.Minutes > 24*60 {
		writeErr(w, http.StatusBadRequest, "durée hors limites (0 à 24 h)")
		return
	}
	set := s.mon.Settings()
	cur := set.Get()
	cur.MaintenanceUntil = 0
	result := "terminée"
	if body.Minutes > 0 {
		cur.MaintenanceUntil = time.Now().Add(time.Duration(body.Minutes) * time.Minute).UnixMilli()
		result = strconv.Itoa(body.Minutes) + " min"
	}
	out, err := set.Update(r.Context(), cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "maintenance", "notifications", result, r.UserAgent())
	writeJSON(w, http.StatusOK, out)
}
