package api

import (
	"net/http"

	"github.com/karelisio/vigie/agent/internal/autoheal"
)

// AutoHealer fournit l'historique des réparations automatiques.
type AutoHealer interface {
	History() []autoheal.Repair
}

func (s *Server) autoHeal(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.AutoHeal.History())
}
