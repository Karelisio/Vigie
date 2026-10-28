package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/updater"
)

// ---- Mises à jour : téléchargement d'images, recréation, actions de stack ----

func member(c monitor.Container) updater.Member {
	return updater.Member{ID: c.ID, Name: c.Name, Self: c.Self, Running: c.State == "running" || c.State == "restarting"}
}

// jobStarted répond 202 avec l'opération, ou l'erreur adaptée, et l'inscrit au journal.
func (s *Server) jobStarted(w http.ResponseWriter, r *http.Request, action, target string, job updater.Job, err error) {
	ip, ua := s.clientIP(r), r.UserAgent()
	if err != nil {
		code := http.StatusBadRequest
		switch {
		case errors.Is(err, updater.ErrBusy):
			code = http.StatusConflict
		case errors.Is(err, updater.ErrDisabled):
			code = http.StatusForbidden
		}
		s.st.Audit(r.Context(), ip, action, target, "refusé : "+err.Error(), ua)
		writeErr(w, code, err.Error())
		return
	}
	s.st.Audit(r.Context(), ip, action, target, "lancé", ua)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) updateContainer(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	var body struct {
		PullOnly bool `json:"pull_only"`
	}
	if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "corps invalide : "+err.Error())
		return
	}
	action := "update"
	if body.PullOnly {
		action = "pull"
	}
	job, err := s.upd.UpdateContainer(member(c), body.PullOnly)
	s.jobStarted(w, r, action, c.Name, job, err)
}

func (s *Server) stackAction(w http.ResponseWriter, r *http.Request) {
	project, action := r.PathValue("project"), r.PathValue("action")
	var members []updater.Member
	for _, c := range s.mon.Containers() {
		if c.Project == project {
			members = append(members, member(c))
		}
	}
	if len(members) == 0 {
		writeErr(w, http.StatusNotFound, "stack introuvable")
		return
	}
	job, err := s.upd.Group("stack", project, action, members)
	s.jobStarted(w, r, "stack-"+action, project, job, err)
}

func (s *Server) updateAll(w http.ResponseWriter, r *http.Request) {
	var members []updater.Member
	for _, c := range s.mon.Containers() {
		if c.UpdateAvailable {
			members = append(members, member(c))
		}
	}
	if len(members) == 0 {
		writeErr(w, http.StatusConflict, "aucune mise à jour d'image disponible")
		return
	}
	job, err := s.upd.Group("batch", "all", "update", members)
	s.jobStarted(w, r, "update-all", "toutes les images", job, err)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	for _, c := range s.mon.Containers() {
		if c.Self {
			job, err := s.upd.UpdateContainer(member(c), false)
			s.jobStarted(w, r, "update", c.Name, job, err)
			return
		}
	}
	writeErr(w, http.StatusConflict, "conteneur de l'agent non détecté : mets-le à jour depuis Dockhand")
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.upd.Jobs())
}
