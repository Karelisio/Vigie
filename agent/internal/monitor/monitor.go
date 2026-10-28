// Package monitor maintient l'état courant (système, conteneurs, checks),
// alimente l'historique et diffuse les mises à jour en temps réel.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

type Container struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Image         string   `json:"image"`
	State         string   `json:"state"`
	Status        string   `json:"status"`
	Health        string   `json:"health"`
	CPU           float64  `json:"cpu"`
	MemUsed       uint64   `json:"mem_used"`
	MemLimit      uint64   `json:"mem_limit"`
	MemPct        float64  `json:"mem"`
	NetRx         float64  `json:"net_rx"`
	NetTx         float64  `json:"net_tx"`
	Pids          uint64   `json:"pids"`
	Created       int64    `json:"created"`
	StartedAt     int64    `json:"started_at"`
	FinishedAt    int64    `json:"finished_at"`
	ExitCode      int      `json:"exit_code"`
	OOMKilled     bool     `json:"oom_killed"`
	RestartCount  int      `json:"restart_count"`
	RestartPolicy string   `json:"restart_policy"`
	Project       string   `json:"project"`
	Service       string   `json:"service"`
	Ports         []string `json:"ports"`
	Problem       string   `json:"problem"` // "", down, restarting, unhealthy, oom
	Ignored       bool     `json:"ignored"`
	Self          bool     `json:"self"`
	StoppedByUser bool     `json:"stopped_by_user"`
	// Référence de l'image utilisée à la création (ex. ghcr.io/x/y:latest).
	ImageRef        string `json:"image_ref"`
	UpdateAvailable bool   `json:"update_available"`
	Muted           bool   `json:"muted"`
	// Version publiée écartée par un retour à la version précédente (update_available est alors faux).
	UpdateIgnored bool `json:"update_ignored,omitempty"`
}

type Issue struct {
	Kind     string `json:"kind"`     // container, system, check
	Severity string `json:"severity"` // warning, critical
	Target   string `json:"target"`
	ID       string `json:"id,omitempty"`
	Message  string `json:"message"`
}

type Counts struct {
	Total    int `json:"total"`
	Running  int `json:"running"`
	Stopped  int `json:"stopped"`
	Problems int `json:"problems"`
}

type CheckCounts struct {
	Total int `json:"total"`
	Up    int `json:"up"`
	Down  int `json:"down"`
}

type Overview struct {
	Status     string           `json:"status"` // ok, warning, critical
	Issues     []Issue          `json:"issues"`
	System     *system.Snapshot `json:"system"`
	Containers Counts           `json:"containers"`
	Checks     CheckCounts      `json:"checks"`
	Server     string           `json:"server"`
	UpdatedAt  int64            `json:"updated_at"`
}

type Thresholds struct {
	CPU  float64 `json:"cpu"`
	RAM  float64 `json:"ram"`
	Disk float64 `json:"disk"`
}

type statPrev struct {
	total, system uint64
	rx, tx        uint64
	at            time.Time
}

type inspectEntry struct {
	key string
	in  *docker.Inspect
}

type acc struct {
	n                            int
	cpu, mem, disk, rx, tx, load float64
	temp                         float64
	tempN                        int
}

type ctrAcc struct {
	n        int
	cpu, mem float64
	memBytes float64
	rx, tx   float64
}

type Monitor struct {
	cfg  *config.Config
	dk   *docker.Client
	sys  *system.Collector
	st   *store.Store
	log  *slog.Logger
	hub  hub
	Host system.HostInfo

	mu         sync.RWMutex
	snapshot   system.Snapshot
	containers []Container
	checks     []checks.Status
	overview   Overview
	inspect    map[string]inspectEntry
	prev       map[string]statPrev
	stopped    map[string]bool
	actions    map[string]time.Time
	selfID     string
	sysAcc     acc
	ctrAcc     map[string]*ctrAcc
	dockerErr  string
	set        *settings.Manager
	img        *images.Checker
	updates    map[string]images.Status // nom du conteneur → état de son image
	digests    map[string][]string      // ID d'image → RepoDigests
	transient  map[string]bool          // anciens conteneurs renommés pendant une mise à jour

	refresh chan struct{}

	// Points d'extension utilisés par le moteur d'alertes.
	OnSystem       func(system.Snapshot)
	OnEvent        func(docker.Event)
	OnImageUpdates func(names []string)
	// OnMinute reçoit chaque minute le dernier instantané système (compteur de trafic mensuel).
	OnMinute func(ts time.Time, s system.Snapshot)
	// IgnoredDigest renvoie le digest d'une version écartée par un retour arrière ("" si aucune).
	IgnoredDigest func(name string) string
}

func New(cfg *config.Config, dk *docker.Client, sys *system.Collector, st *store.Store, set *settings.Manager, log *slog.Logger) *Monitor {
	m := &Monitor{
		set:       set,
		img:       images.NewChecker(log),
		updates:   map[string]images.Status{},
		digests:   map[string][]string{},
		transient: map[string]bool{},
		cfg:       cfg,
		dk:        dk,
		sys:       sys,
		st:        st,
		log:       log,
		inspect:   map[string]inspectEntry{},
		prev:      map[string]statPrev{},
		stopped:   map[string]bool{},
		actions:   map[string]time.Time{},
		ctrAcc:    map[string]*ctrAcc{},
		refresh:   make(chan struct{}, 1),
		selfID:    detectSelfID(),
	}
	m.Host = sys.HostInfo()
	if cfg.ServerName == "" {
		cfg.ServerName = m.Host.Hostname
	}
	_ = json.Unmarshal([]byte(st.Get(context.Background(), "stopped_by_user")), &m.stopped)
	_ = json.Unmarshal([]byte(st.Get(context.Background(), "image_updates")), &m.updates)
	if m.updates == nil {
		m.updates = map[string]images.Status{}
	}
	if m.stopped == nil {
		m.stopped = map[string]bool{}
	}
	return m
}

var containerIDRe = regexp.MustCompile(`/(?:docker/containers|docker|cri-containerd|libpod)[-/]([0-9a-f]{64})`)

// detectSelfID retrouve l'ID du conteneur de l'agent (pour s'interdire de s'arrêter).
func detectSelfID() string {
	for _, f := range []string{"/proc/self/mountinfo", "/proc/self/cgroup"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m := containerIDRe.FindStringSubmatch(string(b)); m != nil {
			return m[1]
		}
	}
	return ""
}

func (m *Monitor) SelfID() string { return m.selfID }

func (m *Monitor) Thresholds() Thresholds {
	s := m.set.Get()
	return Thresholds{CPU: s.CPU, RAM: s.RAM, Disk: s.Disk}
}

// Run lance les boucles de collecte jusqu'à l'annulation du contexte.
func (m *Monitor) Run(ctx context.Context) {
	// Amorce : deux mesures CPU pour obtenir un pourcentage dès le départ.
	m.sys.Collect()
	time.Sleep(500 * time.Millisecond)
	m.collectSystem()
	m.collectContainers(ctx)
	if err := m.st.Aggregate(ctx, 26*time.Hour); err != nil {
		m.log.Warn("agrégation initiale", "erreur", err)
	}

	go m.eventsLoop(ctx)
	go m.containersLoop(ctx)
	go m.minuteLoop(ctx)
	go m.imagesLoop(ctx)

	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.collectSystem()
		}
	}
}

func (m *Monitor) collectSystem() {
	s := m.sys.Collect()
	m.mu.Lock()
	m.snapshot = s
	a := &m.sysAcc
	a.n++
	a.cpu += s.CPU
	a.mem += s.MemPct
	a.disk += s.DiskPct
	a.rx += s.NetRx
	a.tx += s.NetTx
	a.load += s.Load1
	if s.Temp != nil {
		a.temp += *s.Temp
		a.tempN++
	}
	m.mu.Unlock()
	m.hub.broadcast("system", s)
	if m.OnSystem != nil {
		m.OnSystem(s)
	}
	m.updateOverview()
}

func (m *Monitor) containersLoop(ctx context.Context) {
	for {
		interval := 15 * time.Second
		if m.hub.count() > 0 {
			interval = 4 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-m.refresh:
			// Laisse le temps au démon d'appliquer les changements groupés.
			time.Sleep(300 * time.Millisecond)
		}
		m.collectContainers(ctx)
	}
}

// Refresh demande une collecte immédiate des conteneurs.
func (m *Monitor) Refresh() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}

func (m *Monitor) collectContainers(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := m.dk.List(ctx)
	if err != nil {
		m.mu.Lock()
		m.dockerErr = err.Error()
		m.mu.Unlock()
		m.log.Warn("liste des conteneurs", "erreur", err)
		m.updateOverview()
		return
	}
	out := make([]Container, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, c := range list {
		wg.Add(1)
		go func(i int, c docker.Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = m.buildContainer(ctx, c)
		}(i, c)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	m.mu.Lock()
	m.dockerErr = ""
	alive := map[string]bool{}
	for _, c := range out {
		alive[c.ID] = true
		if c.State != "running" {
			continue
		}
		a := m.ctrAcc[c.Name]
		if a == nil {
			a = &ctrAcc{}
			m.ctrAcc[c.Name] = a
		}
		a.n++
		a.cpu += c.CPU
		a.mem += c.MemPct
		a.memBytes += float64(c.MemUsed)
		a.rx += c.NetRx
		a.tx += c.NetTx
	}
	for id := range m.inspect {
		if !alive[id] {
			delete(m.inspect, id)
			delete(m.prev, id)
		}
	}
	m.containers = out
	m.mu.Unlock()
	m.hub.broadcast("containers", out)
	m.updateOverview()
}

func (m *Monitor) buildContainer(ctx context.Context, c docker.Container) Container {
	name := c.Name()
	ci := Container{
		ID:      c.ID,
		Name:    name,
		Image:   c.Image,
		State:   c.State,
		Status:  c.Status,
		Health:  c.Health(),
		Created: c.Created * 1000,
		Project: c.Labels["com.docker.compose.project"],
		Service: c.Labels["com.docker.compose.service"],
		Self:    m.selfID != "" && c.ID == m.selfID,
		Ignored: m.cfg.Ignore[name] || strings.EqualFold(c.Labels["vigie.ignore"], "true"),
		Ports:   []string{},
	}
	seenPort := map[string]bool{}
	for _, p := range c.Ports {
		s := fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		if p.PublicPort != 0 {
			s = fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		}
		if !seenPort[s] {
			seenPort[s] = true
			ci.Ports = append(ci.Ports, s)
		}
	}

	key := c.State + "|" + ci.Health
	m.mu.RLock()
	entry, ok := m.inspect[c.ID]
	stoppedByUser := m.stopped[name]
	m.mu.RUnlock()
	if !ok || entry.key != key {
		if in, err := m.dk.Inspect(ctx, c.ID); err == nil {
			entry = inspectEntry{key: key, in: in}
			m.mu.Lock()
			m.inspect[c.ID] = entry
			m.mu.Unlock()
		}
	}
	if in := entry.in; in != nil {
		ci.StartedAt = parseTime(in.State.StartedAt)
		ci.FinishedAt = parseTime(in.State.FinishedAt)
		ci.ExitCode = in.State.ExitCode
		ci.OOMKilled = in.State.OOMKilled
		ci.RestartCount = in.RestartCount
		ci.RestartPolicy = in.HostConfig.RestartPolicy.Name
		if in.State.Health != nil && ci.Health == "" {
			ci.Health = in.State.Health.Status
		}
	}
	ci.StoppedByUser = stoppedByUser && c.State != "running"
	m.mu.RLock()
	if m.transient[name] {
		ci.Ignored = true
	}
	m.mu.RUnlock()
	ci.Muted = m.set.Get().IsMuted(name)
	if entry.in != nil {
		ci.ImageRef = entry.in.Config.Image
	}
	m.mu.RLock()
	ci.UpdateAvailable = m.updates[name].UpdateAvailable
	ci.UpdateIgnored = m.updates[name].Ignored
	m.mu.RUnlock()

	if c.State == "running" {
		if st, err := m.dk.Stats(ctx, c.ID); err == nil {
			m.applyStats(&ci, st)
		}
	}
	ci.Problem = problemOf(&ci)
	return ci
}

func (m *Monitor) applyStats(ci *Container, st *docker.Stats) {
	now := time.Now()
	var rx, tx uint64
	for _, n := range st.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	cur := statPrev{total: st.CPUStats.CPUUsage.TotalUsage, system: st.CPUStats.SystemUsage, rx: rx, tx: tx, at: now}
	m.mu.Lock()
	p, ok := m.prev[ci.ID]
	m.prev[ci.ID] = cur
	m.mu.Unlock()
	if ok && cur.system > p.system && cur.total >= p.total {
		ci.CPU = round2(100 * float64(cur.total-p.total) / float64(cur.system-p.system))
		if dt := now.Sub(p.at).Seconds(); dt > 0 && rx >= p.rx && tx >= p.tx {
			ci.NetRx = float64(rx-p.rx) / dt
			ci.NetTx = float64(tx-p.tx) / dt
		}
	}
	ci.MemUsed = st.MemUsed()
	ci.MemLimit = st.MemoryStats.Limit
	if ci.MemLimit > 0 {
		ci.MemPct = round1(100 * float64(ci.MemUsed) / float64(ci.MemLimit))
	}
	ci.Pids = st.PidsStats.Current
}

// problemOf détermine si un conteneur est en défaut.
func problemOf(c *Container) string {
	if c.Ignored {
		return ""
	}
	switch c.State {
	case "restarting":
		return "restarting"
	case "running":
		if c.Health == "unhealthy" {
			return "unhealthy"
		}
	case "exited", "dead":
		if c.StoppedByUser {
			return ""
		}
		if c.OOMKilled {
			return "oom"
		}
		switch c.ExitCode {
		case 0, 130, 137, 143: // arrêt normal ou demandé
			return ""
		}
		return "down"
	}
	return ""
}

func (m *Monitor) eventsLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := m.dk.Events(ctx, func(e docker.Event) {
			switch e.Action {
			case "start", "die", "stop", "kill", "restart", "pause", "unpause", "destroy", "create", "oom", "rename":
				m.mu.Lock()
				delete(m.inspect, e.Actor.ID)
				if e.Action == "start" {
					delete(m.stopped, e.Name())
				}
				m.mu.Unlock()
				m.Refresh()
			default:
				if strings.HasPrefix(e.Action, "health_status") {
					m.mu.Lock()
					delete(m.inspect, e.Actor.ID)
					m.mu.Unlock()
					m.Refresh()
				} else {
					return
				}
			}
			m.recordEvent(e)
			m.hub.broadcast("event", map[string]any{
				"action": e.Action, "id": e.Actor.ID, "name": e.Name(), "ts": e.Time * 1000,
				"exit_code": e.Actor.Attributes["exitCode"],
			})
			if m.OnEvent != nil {
				m.OnEvent(e)
			}
		})
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		m.log.Warn("flux d'événements Docker interrompu", "erreur", err, "reprise_dans", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (m *Monitor) minuteLoop(ctx context.Context) {
	next := time.Now().Truncate(time.Minute).Add(time.Minute)
	for {
		select {
		case <-ctx.Done():
			m.flush(context.Background(), next.Add(-time.Minute))
			return
		case <-time.After(time.Until(next)):
		}
		ts := next.Add(-time.Minute)
		m.flush(ctx, ts)
		if next.Minute()%5 == 0 {
			if err := m.st.Aggregate(ctx, 2*time.Hour); err != nil {
				m.log.Warn("agrégation", "erreur", err)
			}
		}
		if next.Minute() == 7 {
			if err := m.st.Cleanup(ctx); err != nil {
				m.log.Warn("nettoyage", "erreur", err)
			}
		}
		next = next.Add(time.Minute)
	}
}

func (m *Monitor) flush(ctx context.Context, ts time.Time) {
	m.mu.Lock()
	a := m.sysAcc
	m.sysAcc = acc{}
	ctr := m.ctrAcc
	m.ctrAcc = map[string]*ctrAcc{}
	snap := m.snapshot
	m.mu.Unlock()
	if a.n > 0 {
		n := float64(a.n)
		p := store.SysSample{TS: ts.UnixMilli(), CPU: a.cpu / n, Mem: a.mem / n, Disk: a.disk / n, Rx: a.rx / n, Tx: a.tx / n, Load1: a.load / n}
		if a.tempN > 0 {
			t := a.temp / float64(a.tempN)
			p.Temp = &t
		}
		if err := m.st.InsertSys(ctx, p); err != nil {
			m.log.Warn("historique système", "erreur", err)
		}
	}
	samples := make([]store.CtrSample, 0, len(ctr))
	for name, c := range ctr {
		n := float64(c.n)
		samples = append(samples, store.CtrSample{Name: name, TS: ts.UnixMilli(), CPU: c.cpu / n, Mem: c.mem / n, MemBytes: int64(c.memBytes / n), Rx: c.rx / n, Tx: c.tx / n})
	}
	if err := m.st.InsertCtr(ctx, samples); err != nil {
		m.log.Warn("historique conteneurs", "erreur", err)
	}
	if m.OnMinute != nil {
		m.OnMinute(ts, snap)
	}
}

// SetChecks met à jour l'état des checks (appelé par le runner).
func (m *Monitor) SetChecks(list []checks.Status) {
	m.mu.Lock()
	m.checks = list
	m.mu.Unlock()
	m.hub.broadcast("checks", list)
	m.updateOverview()
}

func (m *Monitor) updateOverview() {
	m.mu.Lock()
	ov := m.computeOverview()
	prev := m.overview
	m.overview = ov
	m.mu.Unlock()
	if ov.Status != prev.Status || !sameIssues(ov.Issues, prev.Issues) || ov.Containers != prev.Containers || ov.Checks != prev.Checks {
		m.hub.broadcast("overview", ov)
	}
}

func sameIssues(a, b []Issue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Monitor) computeOverview() Overview {
	s := m.snapshot
	ov := Overview{Status: "ok", Issues: []Issue{}, System: &s, Server: m.cfg.ServerName, UpdatedAt: time.Now().UnixMilli()}
	add := func(i Issue) {
		ov.Issues = append(ov.Issues, i)
		if i.Severity == "critical" {
			ov.Status = "critical"
		} else if ov.Status == "ok" {
			ov.Status = "warning"
		}
	}
	if m.dockerErr != "" {
		add(Issue{Kind: "docker", Severity: "critical", Target: "docker", Message: "Docker injoignable"})
	}
	metric := func(name, label string, v, limit float64) {
		switch {
		case v >= limit:
			add(Issue{Kind: "system", Severity: "critical", Target: name, Message: fmt.Sprintf("%s à %.0f %%", label, v)})
		case v >= limit-10:
			add(Issue{Kind: "system", Severity: "warning", Target: name, Message: fmt.Sprintf("%s à %.0f %%", label, v)})
		}
	}
	th := m.set.Get()
	metric("cpu", "Processeur", s.CPU, th.CPU)
	metric("ram", "Mémoire", s.MemPct, th.RAM)
	metric("disk", "Disque", s.DiskPct, th.Disk)

	for _, c := range m.containers {
		ov.Containers.Total++
		if c.State == "running" {
			ov.Containers.Running++
		} else {
			ov.Containers.Stopped++
		}
		if c.Problem == "" {
			continue
		}
		ov.Containers.Problems++
		msg := map[string]string{
			"down":       fmt.Sprintf("arrêté (code %d)", c.ExitCode),
			"oom":        "tué : mémoire insuffisante",
			"restarting": "redémarre en boucle",
			"unhealthy":  "en mauvaise santé",
		}[c.Problem]
		add(Issue{Kind: "container", Severity: "critical", Target: c.Name, ID: c.ID, Message: msg})
	}
	for _, ch := range m.checks {
		if !ch.Enabled {
			continue
		}
		ov.Checks.Total++
		switch ch.State {
		case "up":
			ov.Checks.Up++
		case "down":
			ov.Checks.Down++
			msg := "ne répond pas"
			if ch.Last != nil && ch.Last.Error != "" {
				msg = ch.Last.Error
			}
			sev := "warning"
			if ch.ConsecutiveFailures >= 2 {
				sev = "critical"
			}
			add(Issue{Kind: "check", Severity: sev, Target: ch.Name, ID: fmt.Sprint(ch.ID), Message: msg})
		}
	}
	return ov
}

// ---- Accès en lecture ----

func (m *Monitor) Snapshot() system.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *Monitor) Containers() []Container {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Container{}, m.containers...)
}

func (m *Monitor) Checks() []checks.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]checks.Status{}, m.checks...)
}

func (m *Monitor) Overview() Overview {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.overview
}

// Find retrouve un conteneur par ID complet, préfixe d'ID (12+) ou nom.
func (m *Monitor) Find(ref string) (Container, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ref = strings.TrimPrefix(ref, "/")
	for _, c := range m.containers {
		if c.ID == ref || c.Name == ref || (len(ref) >= 12 && strings.HasPrefix(c.ID, ref)) {
			return c, true
		}
	}
	return Container{}, false
}

// ---- Suivi des actions de l'utilisateur ----

func (m *Monitor) NoteAction(name, action string) {
	m.mu.Lock()
	m.actions[name] = time.Now()
	switch action {
	case "stop":
		m.stopped[name] = true
	case "start", "restart":
		delete(m.stopped, name)
	}
	b, _ := json.Marshal(m.stopped)
	m.mu.Unlock()
	_ = m.st.Set(context.Background(), "stopped_by_user", string(b))
}

// RecentAction indique si l'utilisateur a agi sur ce conteneur récemment.
func (m *Monitor) RecentAction(name string, within time.Duration) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.actions[name]
	return ok && time.Since(t) < within
}

func (m *Monitor) IsStoppedByUser(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stopped[name]
}

// ---- Temps réel ----

func (m *Monitor) Subscribe() *Sub           { return m.hub.subscribe() }
func (m *Monitor) Unsubscribe(s *Sub)        { m.hub.unsubscribe(s) }
func (m *Monitor) Broadcast(t string, d any) { m.hub.broadcast(t, d) }
func (m *Monitor) Subscribers() int          { return m.hub.count() }

func parseTime(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 {
		return 0
	}
	return t.UnixMilli()
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// recordEvent conserve les événements utiles pour la chronologie d'un conteneur.
func (m *Monitor) recordEvent(e docker.Event) {
	action, detail := e.Action, ""
	switch {
	case action == "die":
		detail = e.Actor.Attributes["exitCode"]
	case strings.HasPrefix(action, "health_status"):
		detail = strings.TrimSpace(strings.TrimPrefix(action, "health_status:"))
		action = "health"
	case action == "start", action == "stop", action == "restart", action == "oom",
		action == "create", action == "destroy", action == "pause", action == "unpause":
	default:
		return
	}
	ts := e.TimeNano / int64(time.Millisecond)
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}
	m.st.AddEvent(context.Background(), e.Name(), action, detail, ts)
}

// ---- Mises à jour d'images ----

func (m *Monitor) imagesLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second):
	}
	for {
		m.CheckImages(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

// CheckImages compare l'image de chaque conteneur à la dernière publiée sur son registre.
func (m *Monitor) CheckImages(ctx context.Context, force bool) map[string]images.Status {
	type item struct{ name, ref, imageID string }
	var items []item
	m.mu.RLock()
	for _, c := range m.containers {
		if e, ok := m.inspect[c.ID]; ok && e.in != nil {
			items = append(items, item{c.Name, e.in.Config.Image, e.in.Image})
		}
	}
	m.mu.RUnlock()

	results := map[string]images.Status{}
	for _, it := range items {
		if ctx.Err() != nil {
			return nil
		}
		st := images.Status{CheckedAt: time.Now().UnixMilli()}
		if _, ok := images.Parse(it.ref); !ok {
			continue
		}
		remote, err := m.img.RemoteDigest(ctx, it.ref, force)
		if err != nil {
			st.Error = err.Error()
		} else if digests, err := m.repoDigests(ctx, it.imageID); err != nil {
			st.Error = err.Error()
		} else if upd, known := images.UpdateAvailable(it.ref, remote, digests); known {
			st.UpdateAvailable = upd
			if upd && m.IgnoredDigest != nil {
				ignoreUpdate(&st, remote, m.IgnoredDigest(it.name))
			}
		}
		results[it.name] = st
	}

	var newly []string
	m.mu.Lock()
	for name, st := range results {
		if st.UpdateAvailable && !m.updates[name].UpdateAvailable {
			newly = append(newly, name)
		}
	}
	m.updates = results
	b, _ := json.Marshal(results)
	m.mu.Unlock()
	_ = m.st.Set(context.Background(), "image_updates", string(b))
	sort.Strings(newly)
	if len(newly) > 0 && m.OnImageUpdates != nil {
		m.OnImageUpdates(newly)
	}
	m.Refresh()
	return results
}

func (m *Monitor) repoDigests(ctx context.Context, imageID string) ([]string, error) {
	m.mu.RLock()
	d, ok := m.digests[imageID]
	m.mu.RUnlock()
	if ok {
		return d, nil
	}
	img, err := m.dk.ImageDetails(ctx, imageID)
	if err != nil {
		return nil, err
	}
	// Image corrigée par Vigie : pas de digest de registre, ceux de son image d'origine comptent.
	d = images.EffectiveDigests(img.RepoDigests, images.LabelsOf(img.Config))
	m.mu.Lock()
	m.digests[imageID] = d
	m.mu.Unlock()
	return d, nil
}

func (m *Monitor) ImageUpdates() map[string]images.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]images.Status, len(m.updates))
	for k, v := range m.updates {
		out[k] = v
	}
	return out
}

func (m *Monitor) Settings() *settings.Manager { return m.set }

// SetTransient masque un conteneur temporaire (ancienne version renommée pendant une mise à jour).
func (m *Monitor) SetTransient(name string, on bool) {
	m.mu.Lock()
	if on {
		m.transient[name] = true
	} else {
		delete(m.transient, name)
	}
	m.mu.Unlock()
}

// ignoreUpdate marque comme ignorée la version publiée remote si un retour arrière l'a écartée
// (ignored : son digest) ; une version plus récente reste signalée.
func ignoreUpdate(st *images.Status, remote, ignored string) {
	if st.UpdateAvailable && ignored != "" && remote == ignored {
		st.UpdateAvailable, st.Ignored = false, true
	}
}

// UnignoreUpdate signale de nouveau comme disponible la version qu'un retour arrière faisait
// ignorer, sans nouvelle notification (elle n'est pas nouvelle).
func (m *Monitor) UnignoreUpdate(name string) {
	m.mu.Lock()
	st, ok := m.updates[name]
	if !ok || !st.Ignored {
		m.mu.Unlock()
		return
	}
	st.Ignored, st.UpdateAvailable = false, true
	m.updates[name] = st
	b, _ := json.Marshal(m.updates)
	m.mu.Unlock()
	_ = m.st.Set(context.Background(), "image_updates", string(b))
	m.Refresh()
}

// AfterUpdate rafraîchit la liste puis l'état des images une fois les conteneurs recréés.
func (m *Monitor) AfterUpdate() {
	m.Refresh()
	go func() {
		time.Sleep(5 * time.Second)
		m.CheckImages(context.Background(), false)
	}()
}
