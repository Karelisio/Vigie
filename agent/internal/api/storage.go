package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/karelisio/vigie/agent/internal/storage"
)

// getStorage renvoie la prévision du disque et le dernier calcul des volumes (permis en lecture seule).
func (s *Server) getStorage(w http.ResponseWriter, r *http.Request) {
	if s.Storage == nil {
		writeErr(w, http.StatusServiceUnavailable, "suivi du stockage indisponible")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.Storage.Report(ctx))
}

// refreshStorage relance le calcul des volumes en arrière-plan (202) ; l'appli suit `refreshing`.
func (s *Server) refreshStorage(w http.ResponseWriter, r *http.Request) {
	if s.Storage == nil {
		writeErr(w, http.StatusServiceUnavailable, "suivi du stockage indisponible")
		return
	}
	wait, err := s.Storage.Refresh()
	if errors.Is(err, storage.ErrTooSoon) {
		secs := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeErr(w, http.StatusTooManyRequests, fmt.Sprintf("Calcul lancé il y a moins d'une minute : réessaie dans %d s", secs))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeJSON(w, http.StatusAccepted, s.Storage.Report(ctx))
}
