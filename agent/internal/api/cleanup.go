package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/karelisio/vigie/agent/internal/cleanup"
)

// volumesSupported : Docker ≥ 23 (API 1.42) ne supprime que les volumes anonymes orphelins.
func (s *Server) volumesSupported(ctx context.Context) bool {
	v, err := s.dk.Version(ctx)
	return err == nil && v.AtLeast(1, 42)
}

func (s *Server) cleanupPreview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	du, err := s.dk.DiskUsage(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Docker : "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		cleanup.Preview
		// Images gardées pour revenir à une version précédente, comprises dans images (agent 0.6.0).
		RollbackImages cleanup.Category `json:"rollback_images"`
	}{cleanup.Estimate(du, s.volumesSupported(ctx)), cleanup.Among(du, s.upd.RollbackImages(ctx))})
}

func (s *Server) cleanupRun(w http.ResponseWriter, r *http.Request) {
	opt := cleanup.Options{Images: true, BuildCache: true}
	if err := readJSON(r, &opt); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	volumes := s.volumesSupported(r.Context())
	job, err := s.upd.Run("cleanup", "cleanup", "disque", func(ctx context.Context, progress func(float64, string)) (string, error) {
		freed, err := cleanup.Run(ctx, s.dk, opt, volumes, progress)
		if err != nil {
			return "", err
		}
		if freed == 0 {
			return "Rien à supprimer : le disque était déjà propre", nil
		}
		return cleanup.HumanBytes(freed) + " libérés", nil
	})
	s.jobStarted(w, r, "cleanup", "disque", job, err)
}
