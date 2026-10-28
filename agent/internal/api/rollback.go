package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/karelisio/vigie/agent/internal/updater"
)

// ---- Retour à la version précédente d'un conteneur (agent 0.6.0) ----

// rollbackErr répond à une erreur de retour arrière détectée avant le lancement de l'opération.
func (s *Server) rollbackErr(w http.ResponseWriter, r *http.Request, action, target string, err error) bool {
	code := 0
	switch {
	case errors.Is(err, updater.ErrNoPoint):
		code = http.StatusNotFound
	case errors.Is(err, updater.ErrRolledBack), errors.Is(err, updater.ErrStalePoint), errors.Is(err, updater.ErrRollbackSelf):
		code = http.StatusConflict
	default:
		return false
	}
	if r.Method != http.MethodGet {
		s.st.Audit(r.Context(), s.clientIP(r), action, target, "refusé : "+err.Error(), r.UserAgent())
	}
	writeErr(w, code, err.Error())
	return true
}

func (s *Server) getRollback(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v, err := s.upd.RollbackPoint(ctx, member(c))
	if err != nil {
		if !s.rollbackErr(w, r, "rollback", c.Name, err) {
			writeErr(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) postRollback(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	job, err := s.upd.Rollback(ctx, member(c))
	if err != nil && s.rollbackErr(w, r, "rollback", c.Name, err) {
		return
	}
	s.jobStarted(w, r, "rollback", c.Name, job, err)
}

// deleteRollbackIgnore autorise de nouveau la version écartée par le retour arrière.
func (s *Server) deleteRollbackIgnore(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v, err := s.upd.Unignore(ctx, member(c))
	if err != nil {
		if !s.rollbackErr(w, r, "rollback-unignore", c.Name, err) {
			writeErr(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	s.mon.UnignoreUpdate(c.Name)
	s.st.Audit(r.Context(), s.clientIP(r), "rollback-unignore", c.Name, "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, v)
}
