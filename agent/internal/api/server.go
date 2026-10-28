// Package api expose l'API REST et WebSocket de l'agent.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/alerts"
	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/oracle"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
)

type Server struct {
	cfg       *config.Config
	mon       *monitor.Monitor
	dk        *docker.Client
	st        *store.Store
	runner    *checks.Runner
	alerts    *alerts.Engine
	upd       *updater.Manager
	log       *slog.Logger
	version   string
	started   time.Time
	tokenHash [32]byte
	limiter   *limiter
}

func New(cfg *config.Config, mon *monitor.Monitor, dk *docker.Client, st *store.Store, runner *checks.Runner, al *alerts.Engine, upd *updater.Manager, log *slog.Logger, version string) *Server {
	return &Server{
		cfg: cfg, mon: mon, dk: dk, st: st, runner: runner, alerts: al, upd: upd, log: log, version: version,
		started:   time.Now(),
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		limiter:   newLimiter(cfg.RateLimit, cfg.RateBurst),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /api/v1/info", s.info)
	mux.HandleFunc("GET /api/v1/overview", s.overview)
	mux.HandleFunc("GET /api/v1/system", s.system)
	mux.HandleFunc("GET /api/v1/system/history", s.systemHistory)

	mux.HandleFunc("GET /api/v1/containers", s.containers)
	mux.HandleFunc("GET /api/v1/containers/{id}", s.container)
	mux.HandleFunc("GET /api/v1/containers/{id}/history", s.containerHistory)
	mux.HandleFunc("GET /api/v1/containers/{id}/logs", s.logs)
	mux.HandleFunc("GET /api/v1/containers/{id}/logs/stream", s.logsStream)
	mux.HandleFunc("POST /api/v1/containers/{id}/start", s.action("start"))
	mux.HandleFunc("POST /api/v1/containers/{id}/stop", s.action("stop"))
	mux.HandleFunc("POST /api/v1/containers/{id}/restart", s.action("restart"))
	mux.HandleFunc("POST /api/v1/containers/{id}/update", s.updateContainer)
	mux.HandleFunc("POST /api/v1/stacks/{project}/{action}", s.stackAction)
	mux.HandleFunc("POST /api/v1/updates/apply", s.updateAll)
	mux.HandleFunc("POST /api/v1/agent/update", s.updateAgent)
	mux.HandleFunc("GET /api/v1/jobs", s.jobs)

	mux.HandleFunc("GET /api/v1/checks", s.listChecks)
	mux.HandleFunc("POST /api/v1/checks", s.createCheck)
	mux.HandleFunc("PUT /api/v1/checks/{id}", s.updateCheck)
	mux.HandleFunc("DELETE /api/v1/checks/{id}", s.deleteCheck)
	mux.HandleFunc("POST /api/v1/checks/{id}/run", s.runCheck)
	mux.HandleFunc("GET /api/v1/checks/{id}/history", s.checkHistory)

	mux.HandleFunc("POST /api/v1/devices", s.registerDevice)
	mux.HandleFunc("DELETE /api/v1/devices", s.unregisterDevice)
	mux.HandleFunc("POST /api/v1/devices/test", s.testPush)

	mux.HandleFunc("GET /api/v1/containers/{id}/events", s.containerEvents)
	mux.HandleFunc("POST /api/v1/containers/{id}/mute", s.muteContainer)

	mux.HandleFunc("GET /api/v1/settings", s.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", s.putSettings)
	mux.HandleFunc("GET /api/v1/oracle", s.oracleReport)
	mux.HandleFunc("GET /api/v1/images", s.imageUpdates)
	mux.HandleFunc("POST /api/v1/images/check", s.checkImages)

	mux.HandleFunc("GET /api/v1/alerts", s.listAlerts)
	mux.HandleFunc("GET /api/v1/audit", s.audit)

	mux.HandleFunc("GET /api/v1/ws", s.live)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "route inconnue")
	})
	return s.wrap(mux)
}

// ---- Utilitaires ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func queryInt(r *http.Request, k string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(k))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

// ---- Système ----

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.version})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	var dv *docker.Version
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	dv, _ = s.dk.Version(ctx)
	out := map[string]any{
		"name":             s.cfg.ServerName,
		"version":          s.version,
		"api":              1,
		"host":             s.mon.Host,
		"agent_started":    s.started.UnixMilli(),
		"thresholds":       s.mon.Thresholds(),
		"push_enabled":     s.alerts.PushEnabled(),
		"public_url":       s.cfg.PublicURL,
		"updates_enabled":  s.upd.Enabled(),
		"last_self_update": updater.LastSelfUpdate(s.cfg.DataDir),
	}
	if dv != nil {
		out["docker"] = dv
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.Overview())
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.Snapshot())
}

func (s *Server) systemHistory(w http.ResponseWriter, r *http.Request) {
	rg, err := store.ParseRange(r.URL.Query().Get("range"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pts, err := s.st.SysHistory(r.Context(), rg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": r.URL.Query().Get("range"), "bucket_ms": rg.Bucket.Milliseconds(), "points": pts})
}

// ---- Conteneurs ----

func (s *Server) containers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.Containers())
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) (monitor.Container, bool) {
	c, ok := s.mon.Find(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "conteneur introuvable")
	}
	return c, ok
}

type mountInfo struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

func (s *Server) container(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	out := map[string]any{"container": c}
	if in, err := s.dk.Inspect(r.Context(), c.ID); err == nil {
		nets := map[string]string{}
		for n, v := range in.NetworkSettings.Networks {
			nets[n] = v.IPAddress
		}
		mounts := []mountInfo{}
		for _, m := range in.Mounts {
			mounts = append(mounts, mountInfo{m.Type, m.Source, m.Destination, m.RW})
		}
		out["networks"] = nets
		out["mounts"] = mounts
		out["image_id"] = in.Image
		out["error"] = in.State.Error
		if h := in.State.Health; h != nil {
			hl := map[string]any{"status": h.Status, "failing_streak": h.FailingStreak}
			if n := len(h.Log); n > 0 {
				last := h.Log[n-1]
				hl["last_output"] = strings.TrimSpace(last.Output)
				hl["last_exit_code"] = last.ExitCode
			}
			out["health"] = hl
		}
		if in.HostConfig.Memory > 0 {
			out["mem_reservation"] = in.HostConfig.Memory
		}
		if in.HostConfig.NanoCPUs > 0 {
			out["cpu_limit"] = float64(in.HostConfig.NanoCPUs) / 1e9
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) containerHistory(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	rg, err := store.ParseRange(r.URL.Query().Get("range"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pts, err := s.st.CtrHistory(r.Context(), c.Name, rg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": r.URL.Query().Get("range"), "bucket_ms": rg.Bucket.Milliseconds(), "points": pts})
}

func (s *Server) action(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.find(w, r)
		ip, ua := s.clientIP(r), r.UserAgent()
		if !ok {
			s.st.Audit(r.Context(), ip, name, r.PathValue("id"), "introuvable", ua)
			return
		}
		if c.Self && name == "stop" {
			s.st.Audit(r.Context(), ip, name, c.Name, "refusé (agent)", ua)
			writeErr(w, http.StatusConflict, "l'agent Vigie ne peut pas s'arrêter lui-même")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		s.mon.NoteAction(c.Name, name)
		var err error
		switch name {
		case "start":
			err = s.dk.Start(ctx, c.ID)
		case "stop":
			err = s.dk.Stop(ctx, c.ID, 15*time.Second)
		case "restart":
			if c.Self {
				// Répondre avant de redémarrer, sinon la requête est coupée.
				s.st.Audit(r.Context(), ip, name, c.Name, "ok", ua)
				writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "state": "restarting"})
				go func() {
					time.Sleep(500 * time.Millisecond)
					_ = s.dk.Restart(context.Background(), c.ID, 10*time.Second)
				}()
				return
			}
			err = s.dk.Restart(ctx, c.ID, 15*time.Second)
		}
		if err != nil {
			s.st.Audit(r.Context(), ip, name, c.Name, "échec : "+err.Error(), ua)
			s.log.Warn("action", "action", name, "conteneur", c.Name, "erreur", err)
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.st.Audit(r.Context(), ip, name, c.Name, "ok", ua)
		s.log.Info("action", "action", name, "conteneur", c.Name, "ip", ip)
		state := ""
		if in, err := s.dk.Inspect(ctx, c.ID); err == nil {
			state = in.State.Status
		}
		s.mon.Refresh()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": state})
	}
}

// ---- Logs ----

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	tail := queryInt(r, "tail", 300, 1, 5000)
	tty := false
	if in, err := s.dk.Inspect(r.Context(), c.ID); err == nil {
		tty = in.Config.Tty
	}
	lines := make([]docker.LogLine, 0, tail)
	err := s.dk.Logs(r.Context(), c.ID, tail, false, tty, func(l docker.LogLine) { lines = append(lines, l) })
	if err != nil && !errors.Is(err, context.Canceled) {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// ---- Checks ----

func (s *Server) listChecks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.runner.Statuses())
}

type checkInput struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	ExpectedStatus int    `json:"expected_status"`
	Keyword        string `json:"keyword"`
	IntervalSec    int    `json:"interval_sec"`
	TimeoutMS      int    `json:"timeout_ms"`
	Enabled        *bool  `json:"enabled"`
	Position       int    `json:"position"`
}

func (in checkInput) toCheck() store.Check {
	c := store.Check{
		Name: in.Name, URL: in.URL, ExpectedStatus: in.ExpectedStatus, Keyword: strings.TrimSpace(in.Keyword),
		IntervalSec: in.IntervalSec, TimeoutMS: in.TimeoutMS, Enabled: true, Position: in.Position,
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	return c
}

func (s *Server) createCheck(w http.ResponseWriter, r *http.Request) {
	var in checkInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	c := in.toCheck()
	if err := checks.Validate(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.CreateCheck(r.Context(), &c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "check.create", c.Name, "ok", r.UserAgent())
	_ = s.runner.Reload(r.Context())
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) checkID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "identifiant invalide")
		return 0, false
	}
	return id, true
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.checkID(w, r)
	if !ok {
		return
	}
	var in checkInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	existing, err := s.st.Check(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "check introuvable")
		return
	}
	c := in.toCheck()
	c.ID = id
	if in.Position == 0 {
		c.Position = existing.Position
	}
	if err := checks.Validate(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.UpdateCheck(r.Context(), &c); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "check.update", c.Name, "ok", r.UserAgent())
	s.runner.Forget(id)
	_ = s.runner.Reload(r.Context())
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.checkID(w, r)
	if !ok {
		return
	}
	c, err := s.st.Check(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "check introuvable")
		return
	}
	if err := s.st.DeleteCheck(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "check.delete", c.Name, "ok", r.UserAgent())
	_ = s.runner.Reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) runCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := s.checkID(w, r)
	if !ok {
		return
	}
	st, err := s.runner.RunNow(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "check introuvable")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) checkHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := s.checkID(w, r)
	if !ok {
		return
	}
	var span time.Duration
	switch rg := r.URL.Query().Get("range"); rg {
	case "", "24h":
		span = 24 * time.Hour
	case "7d":
		span = 7 * 24 * time.Hour
	case "30d":
		span = 30 * 24 * time.Hour
	case "90d":
		span = 90 * 24 * time.Hour
	default:
		writeErr(w, http.StatusBadRequest, "période inconnue (24h, 7d, 30d, 90d)")
		return
	}
	n := queryInt(r, "buckets", 48, 1, 200)
	buckets, err := s.st.UptimeBuckets(r.Context(), id, span, n)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, okCount := 0, 0
	var latSum float64
	latN := 0
	for _, b := range buckets {
		total += b.Total
		okCount += b.OK
		if b.Latency != nil {
			latSum += *b.Latency * float64(b.OK)
			latN += b.OK
		}
	}
	uptime := -1.0
	if total > 0 {
		uptime = float64(int(10000*float64(okCount)/float64(total))) / 100
	}
	latency := 0.0
	if latN > 0 {
		latency = float64(int(latSum/float64(latN)*10)) / 10
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": buckets, "uptime": uptime, "latency": latency})
}

// ---- Appareils et alertes ----

type deviceInput struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceInput
	if err := readJSON(r, &in); err != nil || len(in.Token) < 20 || len(in.Token) > 4096 {
		writeErr(w, http.StatusBadRequest, "jeton d'appareil invalide")
		return
	}
	if len(in.Name) > 100 {
		in.Name = in.Name[:100]
	}
	if err := s.st.UpsertDevice(r.Context(), in.Token, in.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "push_enabled": s.alerts.PushEnabled()})
}

func (s *Server) unregisterDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceInput
	if err := readJSON(r, &in); err != nil || in.Token == "" {
		writeErr(w, http.StatusBadRequest, "jeton d'appareil requis")
		return
	}
	_ = s.st.DeleteDevice(r.Context(), in.Token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) testPush(w http.ResponseWriter, r *http.Request) {
	var in deviceInput
	_ = readJSON(r, &in)
	n, err := s.alerts.SendTest(r.Context(), in.Token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": n})
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.Alerts(r.Context(), queryInt(r, "limit", 50, 1, 500))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.AuditLog(r.Context(), queryInt(r, "limit", 100, 1, 1000))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---- Chronologie, sourdine ----

func (s *Server) containerEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	list, err := s.st.Events(r.Context(), c.Name, queryInt(r, "limit", 80, 1, 500))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) muteContainer(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	var in struct {
		Muted bool `json:"muted"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	set, err := s.mon.Settings().SetMuted(r.Context(), c.Name, in.Muted)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	action := "unmute"
	if in.Muted {
		action = "mute"
	}
	s.st.Audit(r.Context(), s.clientIP(r), action, c.Name, "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, set)
}

// ---- Réglages ----

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.Settings().Get())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	// Les champs absents gardent leur valeur actuelle (appli plus ancienne que l'agent).
	in := s.mon.Settings().Get()
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide : "+err.Error())
		return
	}
	out, err := s.mon.Settings().Update(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.Audit(r.Context(), s.clientIP(r), "settings", "alertes", "ok", r.UserAgent())
	writeJSON(w, http.StatusOK, out)
}

// ---- Oracle, images ----

func (s *Server) oracleReport(w http.ResponseWriter, r *http.Request) {
	rep, err := oracle.Evaluate(r.Context(), s.st, s.mon.Host.Kernel, s.mon.Host.NumCPU)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) imageUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.mon.ImageUpdates())
}

func (s *Server) checkImages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	writeJSON(w, http.StatusOK, s.mon.CheckImages(ctx, true))
}
