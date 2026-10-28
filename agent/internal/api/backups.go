package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/karelisio/vigie/agent/internal/backup"
)

// ---- Sauvegardes : toutes les routes sous /api/v1/backups ----

// backupState est la configuration (secrets masqués) et la prochaine sauvegarde planifiée.
type backupState struct {
	Config  backup.Config `json:"config"`
	NextRun int64         `json:"next_run"`
}

func (s *Server) backupState(ctx context.Context) backupState {
	return backupState{Config: s.Backups.Config(ctx), NextRun: s.Backups.NextRun(ctx)}
}

func (s *Server) getBackupConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backupState(r.Context()))
}

func (s *Server) putBackupConfig(w http.ResponseWriter, r *http.Request) {
	// Champs absents : valeur actuelle conservée ; secrets vides ou masqués : inchangés.
	in := s.Backups.Config(r.Context())
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	if _, err := s.Backups.Update(r.Context(), in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "backup.config", "sauvegardes", "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, s.backupState(r.Context()))
}

func (s *Server) backupItems(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	items, err := s.Backups.Items(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Docker : "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) backupRun(w http.ResponseWriter, r *http.Request) {
	job, err := s.Backups.Start("manual")
	s.jobStarted(w, r, "backup", "sauvegarde", job, err)
}

// backupTest essaie le stockage avec la configuration reçue (champs absents : celle enregistrée).
// Le résultat, réussi ou non, est une réponse 200 : {ok, message}.
func (s *Server) backupTest(w http.ResponseWriter, r *http.Request) {
	in := s.Backups.Config(r.Context())
	if err := readJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	msg, err := s.Backups.Test(ctx, in)
	if err != nil {
		msg = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "message": msg})
}

func (s *Server) backupHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Backups.History(r.Context()))
}

func (s *Server) backupObjects(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	list, err := s.Backups.Objects(ctx)
	switch {
	case errors.Is(err, backup.ErrNotConfigured):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadGateway, err.Error())
	default:
		writeJSON(w, http.StatusOK, list)
	}
}
