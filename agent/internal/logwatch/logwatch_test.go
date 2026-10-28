package logwatch

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
)

// fake simule Docker (flux de logs), la liste du monitor et les réglages.
type fake struct {
	mu         sync.Mutex
	streams    map[string]*stream
	opened     chan string
	containers []monitor.Container
	set        settings.Settings
	hits       chan Hit
}

type stream struct {
	fn     func(docker.LogLine)
	tail   int
	follow bool
	stop   chan struct{}
	ended  chan struct{}
}

func newFake(rules ...settings.LogRule) *fake {
	return &fake{
		streams: map[string]*stream{},
		opened:  make(chan string, 16),
		set:     settings.Settings{LogRules: rules},
		hits:    make(chan Hit, 64),
	}
}

func (f *fake) Inspect(ctx context.Context, id string) (*docker.Inspect, error) {
	return &docker.Inspect{ID: id}, nil
}

func (f *fake) Logs(ctx context.Context, id string, tail int, follow bool, tty bool, fn func(docker.LogLine)) error {
	s := &stream{fn: fn, tail: tail, follow: follow, stop: make(chan struct{}), ended: make(chan struct{})}
	defer close(s.ended)
	f.mu.Lock()
	f.streams[id] = s
	f.mu.Unlock()
	f.opened <- id
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop: // le conteneur s'arrête : Docker ferme le flux
		return nil
	}
}

func (f *fake) stream(id string) *stream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[id]
}

func (f *fake) emit(id string, lines ...string) {
	s := f.stream(id)
	for _, l := range lines {
		s.fn(docker.LogLine{Stream: "stdout", Line: l})
	}
}

func (f *fake) list() []monitor.Container {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]monitor.Container{}, f.containers...)
}

func (f *fake) settings() settings.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set
}

func (f *fake) setContainers(cs ...monitor.Container) {
	f.mu.Lock()
	f.containers = cs
	f.mu.Unlock()
}

func (f *fake) watcher() *Watcher {
	return New(f, f.list, f.settings, func(h Hit) { f.hits <- h }, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (f *fake) waitOpened(t *testing.T, want string) {
	t.Helper()
	select {
	case id := <-f.opened:
		if id != want {
			t.Fatalf("flux ouvert pour %s, attendu %s", id, want)
		}
		if s := f.stream(id); s.tail != 0 || !s.follow {
			t.Fatalf("flux %s : tail %d, follow %v", id, s.tail, s.follow)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("aucun flux ouvert (attendu %s)", want)
	}
}

func (f *fake) noneOpened(t *testing.T) {
	t.Helper()
	select {
	case id := <-f.opened:
		t.Fatalf("flux inattendu pour %s", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func (f *fake) hit(t *testing.T) Hit {
	t.Helper()
	select {
	case h := <-f.hits:
		return h
	case <-time.After(2 * time.Second):
		t.Fatal("aucune alerte")
		return Hit{}
	}
}

func (f *fake) noHit(t *testing.T) {
	t.Helper()
	select {
	case h := <-f.hits:
		t.Fatalf("alerte inattendue %+v", h)
	case <-time.After(50 * time.Millisecond):
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func statusOf(w *Watcher, id string) Status {
	for _, s := range w.Status() {
		if s.RuleID == id {
			return s
		}
	}
	return Status{}
}

func running(name, id string) monitor.Container {
	return monitor.Container{ID: id, Name: name, State: "running"}
}

func TestMatching(t *testing.T) {
	f := newFake(
		settings.LogRule{ID: "r1", Container: "web", Pattern: "Error"},
		settings.LogRule{ID: "r2", Container: "web", Pattern: `timeout after \d+s`, Regex: true},
		settings.LogRule{ID: "r3", Container: "db", Pattern: "fatal"},
	)
	f.setContainers(running("web", "w1"), monitor.Container{ID: "d1", Name: "db", State: "exited"})
	w := f.watcher()
	w.reconcile(t.Context())
	f.waitOpened(t, "w1")
	f.noneOpened(t) // db est arrêté

	f.emit("w1", "all good", "an ERROR occurred", "Timeout after 30s", "timeout after 30s")
	got := map[string]Hit{}
	for range 2 {
		h := f.hit(t)
		got[h.RuleID] = h
	}
	f.noHit(t)
	if h := got["r1"]; h.Line != "an ERROR occurred" || h.Container != "web" || h.ContainerID != "w1" || h.Pattern != "Error" || h.Others != 0 {
		t.Errorf("alerte texte %+v", h)
	}
	if h := got["r2"]; h.Line != "timeout after 30s" {
		t.Errorf("alerte regex %+v", h)
	}
	if s := statusOf(w, "r1"); !s.Active || s.Matches24h != 1 || s.LastMatch == 0 || s.LastLine != "an ERROR occurred" {
		t.Errorf("état r1 %+v", s)
	}
	if s := statusOf(w, "r3"); s.Active || s.Matches24h != 0 || s.Container != "db" {
		t.Errorf("état r3 %+v", s)
	}

	// Ligne très longue : seul le début est analysé, l'alerte en recopie 300 caractères.
	f.mu.Lock()
	f.set.LogRules = []settings.LogRule{{ID: "r4", Container: "web", Pattern: "panic"}}
	f.mu.Unlock()
	w.reconcile(t.Context())
	f.emit("w1", strings.Repeat("é", 5000)+" panic", "panic: "+strings.Repeat("x", 1000))
	h := f.hit(t)
	if n := utf8.RuneCountInString(h.Line); n != 300 || !strings.HasPrefix(h.Line, "panic: x") || !strings.HasSuffix(h.Line, "…") {
		t.Errorf("ligne tronquée : %d caractères", n)
	}
}

func TestCooldownAndCount(t *testing.T) {
	f := newFake(settings.LogRule{ID: "r1", Container: "web", Pattern: "error"})
	f.setContainers(running("web", "w1"))
	w := f.watcher()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var clock sync.Mutex
	w.now = func() time.Time { clock.Lock(); defer clock.Unlock(); return now }
	advance := func(d time.Duration) { clock.Lock(); now = now.Add(d); clock.Unlock() }
	w.reconcile(t.Context())
	f.waitOpened(t, "w1")

	f.emit("w1", "error 1")
	if h := f.hit(t); h.Line != "error 1" || h.Others != 0 {
		t.Fatalf("première alerte %+v", h)
	}
	advance(time.Minute)
	f.emit("w1", "error 2", "error 3")
	f.noHit(t)
	advance(14 * time.Minute)
	f.emit("w1", "error 4")
	if h := f.hit(t); h.Line != "error 4" || h.Others != 2 {
		t.Fatalf("alerte après le délai %+v", h)
	}

	// Conteneur en sourdine : comptée, mais pas d'alerte ni d'occurrence en attente.
	f.mu.Lock()
	f.set.Muted = []string{"web"}
	f.mu.Unlock()
	w.reconcile(t.Context())
	advance(20 * time.Minute)
	f.emit("w1", "error 5")
	f.noHit(t)
	f.mu.Lock()
	f.set.Muted = nil
	f.mu.Unlock()
	w.reconcile(t.Context())
	f.emit("w1", "error 6")
	if h := f.hit(t); h.Others != 0 {
		t.Fatalf("alerte après la sourdine %+v", h)
	}
	if s := statusOf(w, "r1"); s.Matches24h != 6 || s.LastLine != "error 6" {
		t.Errorf("état %+v", s)
	}
	advance(25 * time.Hour)
	if s := statusOf(w, "r1"); s.Matches24h != 0 {
		t.Errorf("correspondances anciennes encore comptées : %+v", s)
	}
}

func TestFollowerLifecycle(t *testing.T) {
	f := newFake(settings.LogRule{ID: "r1", Container: "web", Pattern: "error"})
	f.setContainers(running("web", "old"), running("other", "o1"))
	w := f.watcher()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go w.Run(ctx)
	f.waitOpened(t, "old")
	f.noneOpened(t) // un seul suiveur, pour le seul conteneur concerné

	// Mise à jour : l'ancien conteneur s'arrête (Docker ferme le flux)…
	close(f.stream("old").stop)
	eventually(t, "suiveur toujours actif après l'arrêt", func() bool { return !statusOf(w, "r1").Active })
	// …puis le nouveau démarre sous un autre ID, avant que la liste du monitor ne soit à jour.
	ev := docker.Event{Action: "start"}
	ev.Actor.ID = "new"
	ev.Actor.Attributes = map[string]string{"name": "web"}
	w.OnEvent(ev)
	f.waitOpened(t, "new")
	eventually(t, "suiveur inactif", func() bool { return statusOf(w, "r1").Active })
	w.Reload()
	f.noneOpened(t)

	// Événement d'un conteneur non concerné : rien ne change.
	ev.Actor.ID = "o1"
	ev.Actor.Attributes = map[string]string{"name": "other"}
	w.OnEvent(ev)
	f.noneOpened(t)

	// Règle supprimée : le suiveur s'arrête.
	f.mu.Lock()
	f.set.LogRules = nil
	f.mu.Unlock()
	w.Reload()
	select {
	case <-f.stream("new").ended:
	case <-time.After(2 * time.Second):
		t.Fatal("suiveur non arrêté après la suppression de la règle")
	}
	if len(w.Status()) != 0 {
		t.Error("état d'une règle supprimée")
	}
}

func TestLimiter(t *testing.T) {
	t0 := time.Now()
	l := limiter{tokens: lineBurst, last: t0}
	for i := range lineBurst {
		if !l.take(t0) {
			t.Fatalf("ligne %d refusée dans la rafale", i)
		}
	}
	if l.take(t0) || l.skipped != 1 {
		t.Fatal("rafale dépassée sans refus")
	}
	ok := 0
	for range 2 * lineRate {
		if l.take(t0.Add(time.Second)) {
			ok++
		}
	}
	if ok != lineRate {
		t.Fatalf("%d lignes acceptées en une seconde, attendu %d", ok, lineRate)
	}
}
