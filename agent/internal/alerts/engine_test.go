package alerts

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

func newEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Ignore: map[string]bool{}, AlertCooldown: 30 * time.Minute, RestartLoopCount: 3, RestartLoopWindow: 10 * time.Minute, CheckFailures: 2}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dk, _ := docker.New("unix:///nonexistent.sock")
	cfg.AlertCPU, cfg.AlertRAM, cfg.AlertDisk, cfg.AlertDuration = 90, 90, 90, 5*time.Minute
	set := settings.Load(context.Background(), st, cfg)
	mon := monitor.New(cfg, dk, system.New("/proc", "/sys", "/"), st, set, log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return New(ctx, cfg, mon, dk, st, log), st
}

func healthEvent(status string) docker.Event {
	e := docker.Event{Action: "health_status: " + status}
	e.Actor.ID = "abc"
	e.Actor.Attributes = map[string]string{"name": "web"}
	return e
}

func TestHealthyWithoutAlertIsSilent(t *testing.T) {
	e, st := newEngine(t)
	e.OnEvent(healthEvent("healthy"))
	if list, _ := st.Alerts(context.Background(), 10); len(list) != 0 {
		t.Fatalf("aucune alerte attendue, obtenu %+v", list)
	}
	e.OnEvent(healthEvent("unhealthy"))
	e.OnEvent(healthEvent("healthy"))
	list, _ := st.Alerts(context.Background(), 10)
	if len(list) != 2 || !list[0].Resolved || list[1].Resolved {
		t.Fatalf("alerte puis résolution attendues, obtenu %+v", list)
	}
}
