package monitor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

func TestIgnoreUpdate(t *testing.T) {
	st := images.Status{UpdateAvailable: true}
	ignoreUpdate(&st, "sha256:new", "")
	if !st.UpdateAvailable || st.Ignored {
		t.Errorf("rien d'ignoré : %+v", st)
	}
	ignoreUpdate(&st, "sha256:newer", "sha256:new")
	if !st.UpdateAvailable || st.Ignored {
		t.Errorf("version plus récente que celle écartée : %+v", st)
	}
	ignoreUpdate(&st, "sha256:new", "sha256:new")
	if st.UpdateAvailable || !st.Ignored {
		t.Errorf("version écartée : %+v", st)
	}
}

func TestUnignoreUpdate(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{Ignore: map[string]bool{}, AlertCPU: 90, AlertRAM: 90, AlertDisk: 90}
	dk, _ := docker.New("unix:///nonexistent.sock")
	m := New(cfg, dk, system.New("/proc", "/sys", "/"), st, settings.Load(context.Background(), st, cfg), slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.updates = map[string]images.Status{"comet": {Ignored: true, CheckedAt: 1}, "redis": {}}

	m.UnignoreUpdate("redis") // rien d'ignoré : inchangé
	m.UnignoreUpdate("comet")
	if got := m.ImageUpdates(); !got["comet"].UpdateAvailable || got["comet"].Ignored || got["redis"].UpdateAvailable {
		t.Fatalf("état = %+v", got)
	}
	var saved map[string]images.Status
	if err := json.Unmarshal([]byte(st.Get(context.Background(), "image_updates")), &saved); err != nil || !saved["comet"].UpdateAvailable {
		t.Errorf("état enregistré = %+v (%v)", saved, err)
	}
}
