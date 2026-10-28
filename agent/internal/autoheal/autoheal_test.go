package autoheal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

// clock est l'horloge simulée partagée par le faux monitor et le réparateur.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeDocker simule les conteneurs : état inspecté et effet d'un démarrage ou d'un redémarrage.
type fakeDocker struct {
	mu    sync.Mutex
	state map[string]*docker.Inspect       // ID → état
	after map[string]func(*docker.Inspect) // ID → état après l'action (défaut : en marche, en bonne santé)
	calls []string
	fail  error
}

func inspect(running bool, health string, exit int) *docker.Inspect {
	in := &docker.Inspect{}
	in.State.Running = running
	in.State.ExitCode = exit
	if health != "" {
		_ = json.Unmarshal([]byte(`{"State":{"Health":{"Status":"`+health+`"}}}`), in)
	}
	return in
}

func (f *fakeDocker) Inspect(ctx context.Context, id string) (*docker.Inspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.state[id]
	if !ok {
		return nil, docker.ErrNotFound
	}
	cp := *in
	if in.State.Health != nil {
		h := *in.State.Health
		cp.State.Health = &h
	}
	return &cp, nil
}

func (f *fakeDocker) act(action, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action+" "+id)
	if f.fail != nil {
		return f.fail
	}
	in := f.state[id]
	if fn := f.after[id]; fn != nil {
		fn(in)
		return nil
	}
	in.State.Running, in.State.ExitCode, in.State.OOMKilled = true, 0, false
	if in.State.Health != nil {
		in.State.Health.Status = "healthy"
	}
	return nil
}

func (f *fakeDocker) Start(ctx context.Context, id string) error { return f.act("start", id) }

func (f *fakeDocker) Restart(ctx context.Context, id string, timeout time.Duration) error {
	return f.act("restart", id)
}

func (f *fakeDocker) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

// fakeMonitor simule la liste des conteneurs et le suivi des actions.
type fakeMonitor struct {
	mu         sync.Mutex
	clock      *clock
	containers []monitor.Container
	actions    map[string]time.Time
	stopped    map[string]bool
	noted      []string
}

func (m *fakeMonitor) Containers() []monitor.Container {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]monitor.Container{}, m.containers...)
}

func (m *fakeMonitor) NoteAction(name, action string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actions[name] = m.clock.now()
	m.noted = append(m.noted, action+" "+name)
}

func (m *fakeMonitor) RecentAction(name string, within time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.actions[name]
	return ok && m.clock.now().Sub(t) < within
}

func (m *fakeMonitor) IsStoppedByUser(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped[name]
}

func (m *fakeMonitor) set(cs ...monitor.Container) {
	m.mu.Lock()
	m.containers = cs
	m.mu.Unlock()
}

type env struct {
	h       *Healer
	dk      *fakeDocker
	mon     *fakeMonitor
	st      *store.Store
	clock   *clock
	notices chan Notice

	mu  sync.Mutex
	set settings.Settings
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	// Proche de l'heure réelle : l'historique relu au démarrage est daté avec time.Now.
	c := &clock{t: time.Now().Truncate(time.Second)}
	e := &env{
		dk:      &fakeDocker{state: map[string]*docker.Inspect{}, after: map[string]func(*docker.Inspect){}},
		mon:     &fakeMonitor{clock: c, actions: map[string]time.Time{}, stopped: map[string]bool{}},
		st:      st,
		clock:   c,
		notices: make(chan Notice, 32),
		set:     settings.Settings{AutoHeal: true, AutoHealAll: true, AutoHealDelay: 3, AutoHealMax: 3},
	}
	e.h = e.healer()
	return e
}

func (e *env) healer() *Healer {
	h := New(e.dk, e.mon, e.st, e.settings, func(n Notice) { e.notices <- n }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.now = e.clock.now
	h.verifyEvery, h.verifyFor, h.stableFor = time.Millisecond, time.Second, 0
	return h
}

func (e *env) settings() settings.Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set
}

func (e *env) configure(fn func(*settings.Settings)) {
	e.mu.Lock()
	fn(&e.set)
	e.mu.Unlock()
}

// scan examine les conteneurs puis attend la fin des réparations lancées.
func (e *env) scan(t *testing.T) {
	t.Helper()
	e.h.scan(t.Context())
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.h.mu.Lock()
		n := len(e.h.busy)
		e.h.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("réparation toujours en cours")
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *env) notice(t *testing.T) Notice {
	t.Helper()
	select {
	case n := <-e.notices:
		return n
	default:
		t.Fatal("aucune notification")
		return Notice{}
	}
}

func (e *env) noNotice(t *testing.T) {
	t.Helper()
	select {
	case n := <-e.notices:
		t.Fatalf("notification inattendue %+v", n)
	default:
	}
}

func (e *env) event(action, name string) {
	ev := docker.Event{Action: action, TimeNano: e.clock.now().UnixNano()}
	ev.Actor.Attributes = map[string]string{"name": name}
	e.h.OnEvent(ev)
}

func sickContainer(name, id string) monitor.Container {
	return monitor.Container{ID: id, Name: name, State: "running", Health: "unhealthy", Problem: "unhealthy"}
}

func downContainer(name, id string, exit int, finished time.Time) monitor.Container {
	return monitor.Container{ID: id, Name: name, State: "exited", ExitCode: exit, Problem: "down", FinishedAt: finished.UnixMilli()}
}

func TestUnhealthyRestartedAfterDelay(t *testing.T) {
	e := newEnv(t)
	e.mon.set(sickContainer("comet", "c1"))
	e.dk.state["c1"] = inspect(true, "unhealthy", 0)

	// Le délai part de l'événement Docker, pas du premier examen.
	e.event("health_status: unhealthy", "comet")
	e.clock.advance(2 * time.Minute)
	e.scan(t)
	e.clock.advance(59 * time.Second)
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 0 {
		t.Fatalf("réparation avant le délai : %v", calls)
	}
	e.clock.advance(time.Second)
	e.scan(t)
	if calls := e.dk.actions(); !slices.Equal(calls, []string{"restart c1"}) {
		t.Fatalf("appels Docker %v", calls)
	}
	if n := e.notice(t); n.Severity != "info" || n.Container != "comet" || n.ContainerID != "c1" ||
		n.Body != "comet était en mauvaise santé depuis 3 min : redémarré automatiquement." {
		t.Errorf("notification %+v", n)
	}
	if !slices.Equal(e.mon.noted, []string{"restart comet"}) {
		t.Errorf("actions notées %v", e.mon.noted)
	}
	hist := e.h.History()
	if len(hist) != 1 || hist[0] != (Repair{TS: e.clock.now().UnixMilli(), Container: "comet", Reason: "unhealthy", Action: "restart", OK: true}) {
		t.Errorf("historique %+v", hist)
	}
	audit, _ := e.st.AuditLog(t.Context(), 10)
	if len(audit) != 1 || audit[0].Action != "autoheal" || audit[0].Target != "comet" || audit[0].Result != "ok" || audit[0].UA != "vigie" {
		t.Errorf("journal %+v", audit)
	}
	events, _ := e.st.Events(t.Context(), "comet", 10)
	if len(events) != 1 || events[0].Action != "autoheal" || events[0].Detail != "unhealthy" {
		t.Errorf("chronologie %+v", events)
	}

	// De retour en bonne santé : plus rien à faire, et un nouvel épisode attend de nouveau le délai.
	e.event("health_status: healthy", "comet")
	e.mon.set(monitor.Container{ID: "c1", Name: "comet", State: "running", Health: "healthy"})
	e.clock.advance(10 * time.Minute)
	e.scan(t)
	e.event("health_status: unhealthy", "comet")
	e.mon.set(sickContainer("comet", "c1"))
	e.dk.state["c1"] = inspect(true, "unhealthy", 0)
	e.clock.advance(time.Minute)
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 1 {
		t.Fatalf("réparation sans attendre le délai : %v", calls)
	}
}

func TestCrashedContainerStarted(t *testing.T) {
	e := newEnv(t)
	now := e.clock.now()
	oom := monitor.Container{ID: "o1", Name: "oomy", State: "exited", ExitCode: 137, OOMKilled: true, Problem: "oom", FinishedAt: now.Add(-time.Minute).UnixMilli()}
	clean := monitor.Container{ID: "k1", Name: "cache", State: "exited", ExitCode: 0, FinishedAt: now.Add(-time.Hour).UnixMilli()}
	e.mon.set(downContainer("db", "d1", 1, now.Add(-5*time.Second)), oom, clean)
	e.dk.state["d1"] = inspect(false, "", 1)
	e.dk.state["o1"] = inspect(false, "", 137)
	e.dk.state["k1"] = inspect(false, "", 0)

	// db vient de s'arrêter : la grâce de 20 s n'est pas écoulée.
	e.scan(t)
	if calls := e.dk.actions(); !slices.Equal(calls, []string{"start o1"}) {
		t.Fatalf("appels Docker %v", calls)
	}
	if n := e.notice(t); n.Body != "oomy avait été tué par manque de mémoire : relancé automatiquement." {
		t.Errorf("notification %+v", n)
	}
	e.clock.advance(10 * time.Second)
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 1 {
		t.Fatalf("relancé avant la fin de la grâce : %v", calls)
	}
	e.clock.advance(5 * time.Second)
	e.scan(t)
	if n := e.notice(t); n.Body != "db s'était arrêté (code 1) : relancé automatiquement." || n.Severity != "info" {
		t.Errorf("notification %+v", n)
	}
	if calls := e.dk.actions(); !slices.Equal(calls, []string{"start o1", "start d1"}) {
		t.Fatalf("appels Docker %v", calls)
	}
	hist := e.h.History()
	if len(hist) != 2 || !hist[0].OK || hist[0].Container != "db" || hist[0].Reason != "down" || hist[0].Action != "start" || hist[1].Reason != "oom" {
		t.Errorf("historique %+v", hist)
	}

	// Déjà reparti (par sa politique de redémarrage) : Docker a le dernier mot.
	e.dk.state["d1"].State.Running = true
	e.clock.advance(5 * time.Minute)
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 2 {
		t.Errorf("conteneur déjà reparti relancé : %v", calls)
	}
}

func TestHourlyLimitAndAbandon(t *testing.T) {
	e := newEnv(t)
	e.configure(func(s *settings.Settings) { s.AutoHealMax = 2 })
	e.mon.set(downContainer("db", "d1", 1, e.clock.now().Add(-time.Minute)))
	e.dk.state["d1"] = inspect(false, "", 1)
	// Plante aussitôt relancé.
	e.dk.after["d1"] = func(in *docker.Inspect) { in.State.Running, in.State.ExitCode = false, 1 }

	e.scan(t)
	if n := e.notice(t); n.Severity != "info" {
		t.Fatalf("notification %+v", n)
	}
	if hist := e.h.History(); len(hist) != 1 || hist[0].OK || hist[0].Error != "de nouveau arrêté (code 1)" {
		t.Fatalf("historique %+v", hist)
	}
	// Réparation récente : pas de nouvel essai avant 2 min.
	e.clock.advance(time.Minute)
	e.scan(t)
	e.noNotice(t)
	e.clock.advance(time.Minute)
	e.scan(t)
	e.notice(t)
	if calls := e.dk.actions(); len(calls) != 2 {
		t.Fatalf("appels Docker %v", calls)
	}

	// Limite atteinte : abandon signalé une seule fois.
	e.clock.advance(2 * time.Minute)
	e.scan(t)
	if n := e.notice(t); n.Severity != "critical" || n.Title != "Réparation automatique abandonnée : db" || n.Resolved {
		t.Errorf("abandon %+v", n)
	}
	e.clock.advance(2 * time.Minute)
	e.scan(t)
	e.noNotice(t)
	if calls := e.dk.actions(); len(calls) != 2 {
		t.Fatalf("réparation malgré l'abandon : %v", calls)
	}

	// L'historique, et avec lui la limite, survit à un redémarrage de l'agent.
	e.h = e.healer()
	if hist := e.h.History(); len(hist) != 2 {
		t.Fatalf("historique relu %+v", hist)
	}
	e.scan(t)
	if n := e.notice(t); n.Severity != "critical" {
		t.Errorf("abandon après redémarrage %+v", n)
	}

	// Retour à la normale (relancé à la main) : l'alerte est close, la réparation reprend.
	e.mon.set(monitor.Container{ID: "d1", Name: "db", State: "running"})
	e.scan(t)
	if n := e.notice(t); !n.Resolved || n.Title != "db fonctionne de nouveau" {
		t.Errorf("retour à la normale %+v", n)
	}
	// Nouveau plantage une heure plus tard : la fenêtre glissante est vide.
	e.clock.advance(time.Hour)
	e.mon.set(downContainer("db", "d1", 1, e.clock.now().Add(-time.Minute)))
	e.scan(t)
	if n := e.notice(t); n.Severity != "info" {
		t.Errorf("notification %+v", n)
	}
	if calls := e.dk.actions(); len(calls) != 3 {
		t.Errorf("appels Docker %v", calls)
	}
}

func TestDockerFailure(t *testing.T) {
	e := newEnv(t)
	e.mon.set(downContainer("db", "d1", 1, e.clock.now().Add(-time.Minute)))
	e.dk.state["d1"] = inspect(false, "", 1)
	e.dk.fail = errors.New("réseau introuvable")
	e.scan(t)
	if n := e.notice(t); n.Severity != "warning" || n.Body != "réseau introuvable" {
		t.Errorf("notification %+v", n)
	}
	if hist := e.h.History(); len(hist) != 1 || hist[0].OK || hist[0].Error != "réseau introuvable" {
		t.Errorf("historique %+v", hist)
	}
	if audit, _ := e.st.AuditLog(t.Context(), 10); len(audit) != 1 || audit[0].Result != "échec : réseau introuvable" {
		t.Errorf("journal %+v", audit)
	}
	if events, _ := e.st.Events(t.Context(), "db", 10); len(events) != 0 {
		t.Errorf("chronologie %+v", events)
	}
}

func TestExclusions(t *testing.T) {
	e := newEnv(t)
	past := e.clock.now().Add(-time.Minute)
	self := sickContainer("vigie-agent", "a1")
	self.Self = true
	ignored := downContainer("ignored", "i1", 1, past)
	ignored.Ignored = true
	e.mon.set(
		self, ignored,
		downContainer("stopped", "s1", 1, past),
		downContainer("busy", "b1", 1, past),
		downContainer("other", "x1", 1, past),
		downContainer("listed", "l1", 1, past),
	)
	for _, id := range []string{"i1", "s1", "b1", "x1", "l1"} {
		e.dk.state[id] = inspect(false, "", 1)
	}
	e.dk.state["a1"] = inspect(true, "unhealthy", 0)
	e.event("health_status: unhealthy", "vigie-agent")
	e.mon.stopped["stopped"] = true
	e.configure(func(s *settings.Settings) {
		s.AutoHealAll, s.AutoHealList = false, []string{"vigie-agent", "ignored", "stopped", "busy", "listed"}
	})
	e.clock.advance(10 * time.Minute)
	e.mon.actions["busy"] = e.clock.now().Add(-time.Minute) // opération en cours (mise à jour, action de l'appli)

	e.scan(t)
	if calls := e.dk.actions(); !slices.Equal(calls, []string{"start l1"}) {
		t.Fatalf("appels Docker %v", calls)
	}

	// Maintenance : aucune réparation tant qu'elle dure.
	e.dk.state["l1"] = inspect(false, "", 1)
	e.clock.advance(time.Hour)
	until := e.clock.now().Add(30 * time.Minute).UnixMilli()
	e.configure(func(s *settings.Settings) { s.MaintenanceUntil = until })
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 1 {
		t.Fatalf("maintenance : %v", calls)
	}
	e.clock.advance(31 * time.Minute)
	e.mon.actions["busy"] = e.clock.now()
	e.scan(t)
	if calls := e.dk.actions(); !slices.Equal(calls, []string{"start l1", "start l1"}) {
		t.Fatalf("après la maintenance : %v", calls)
	}

	// Réparation désactivée : plus rien, même pour un conteneur listé.
	e.configure(func(s *settings.Settings) { s.AutoHeal = false })
	e.dk.state["l1"] = inspect(false, "", 1)
	e.clock.advance(time.Hour)
	e.scan(t)
	if calls := e.dk.actions(); len(calls) != 2 {
		t.Errorf("réparation désactivée : %v", calls)
	}
}
