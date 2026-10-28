package regression

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

func TestCompare(t *testing.T) {
	if _, worse := compare(1, 200<<20, 1.5, 260<<20); worse {
		t.Error("hausse modeste signalée")
	}
	body, worse := compare(1, 200<<20, 1, 480<<20)
	if !worse || !strings.HasPrefix(body, "RAM : 200 Mo → 480 Mo") {
		t.Errorf("mémoire : %q", body)
	}
	body, worse = compare(2, 100<<20, 9.5, 100<<20)
	if !worse || !strings.HasPrefix(body, "CPU : 2,0 % → 9,5 %") {
		t.Errorf("CPU : %q", body)
	}
	if _, worse := compare(0.1, 10<<20, 3, 10<<20); worse {
		t.Error("hausse de CPU sous le seuil absolu signalée")
	}
}

func fill(t *testing.T, st *store.Store, name string, from time.Time, minutes int, cpu float64, mem int64) {
	t.Helper()
	var samples []store.CtrSample
	for i := 0; i < minutes; i++ {
		samples = append(samples, store.CtrSample{Name: name, TS: from.Add(time.Duration(i) * time.Minute).UnixMilli(), CPU: cpu, MemBytes: mem})
	}
	if err := st.InsertCtr(context.Background(), samples); err != nil {
		t.Fatal(err)
	}
}

func TestWatcher(t *testing.T) {
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
	fill(t, st, "redis", update.Add(20*time.Minute), 400, 0.5, 21<<20)
	fill(t, st, "nouveau", update.Add(-10*time.Minute), 10, 1, 1<<20) // trop peu d'historique

	set := settings.Settings{RegressionAlert: true}
	var got []string
	w := New(st, func() settings.Settings { return set }, func(name, title, body string) { got = append(got, title) },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.OnUpdated(ctx, []string{"comet", "redis", "nouveau"}, update)
	if n := len(w.load(ctx)); n != 2 {
		t.Fatalf("comparaisons prévues : %d", n)
	}

	w.Tick(ctx, update.Add(time.Hour)) // trop tôt
	if len(got) != 0 || len(w.load(ctx)) != 2 {
		t.Fatalf("comparaison anticipée : %v", got)
	}
	w.Tick(ctx, time.Now())
	if len(got) != 1 || got[0] != "comet consomme plus depuis sa mise à jour" {
		t.Fatalf("alertes : %v", got)
	}
	if len(w.load(ctx)) != 0 {
		t.Error("comparaisons non oubliées")
	}
}
