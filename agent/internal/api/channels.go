package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/karelisio/vigie/agent/internal/channels"
)

// getChannels renvoie la configuration des canaux d'alerte, secrets masqués.
func (s *Server) getChannels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.alerts.Channels.Get())
}

// putChannels remplace la configuration ; un secret vide ou masqué garde sa valeur actuelle.
func (s *Server) putChannels(w http.ResponseWriter, r *http.Request) {
	var in channels.Config
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	out, err := s.alerts.Channels.Update(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var active []string
	for _, c := range out.Channels {
		if c.Enabled {
			active = append(active, c.Type)
		}
	}
	target := "aucun canal actif"
	if len(active) > 0 {
		target = strings.Join(active, ", ")
	}
	s.st.Audit(r.Context(), s.clientIP(r), "channels", target, "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, out)
}

// testChannels envoie un message d'essai au canal demandé, ou à tous les canaux actifs.
func (s *Server) testChannels(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type string `json:"type"`
	}
	if err := readJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	res, err := s.alerts.Channels.Test(r.Context(), in.Type)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}
