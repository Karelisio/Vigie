package regression

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

func TestForgetAfterRollback(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	update := time.Now().Add(-7 * time.Hour).Truncate(time.Minute)
	fill(t, st, "comet", update.Add(-3*time.Hour), 180, 1, 200<<20)
	fill(t, st, "comet", update.Add(20*time.Minute), 400, 1, 600<<20)
	fill(t, st, "redis", update.Add(-3*time.Hour), 180, 0.5, 20<<20)

	var got []string
	w := New(st, func() settings.Settings { return settings.Settings{RegressionAlert: true} },
		func(name, title, body string) { got = append(got, name) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.OnUpdated(ctx, []string{"comet", "redis"}, update)
	w.Forget(ctx, "comet") // revenu à sa version précédente
	w.Forget(ctx, "absent")
	if list := w.load(ctx); len(list) != 1 || list[0].Name != "redis" {
		t.Fatalf("comparaisons prévues : %+v", list)
	}
	w.Tick(ctx, time.Now())
	if len(got) != 0 {
		t.Errorf("alerte après un retour arrière : %v", got)
	}
}
