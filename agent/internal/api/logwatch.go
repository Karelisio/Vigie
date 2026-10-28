package api

import (
	"net/http"

	"github.com/karelisio/vigie/agent/internal/logwatch"
)

// LogWatcher fournit l'état des alertes sur les logs.
type LogWatcher interface {
	Status() []logwatch.Status
}

func (s *Server) logWatch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.LogWatch.Status())
}
