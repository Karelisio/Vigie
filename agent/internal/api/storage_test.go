package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/storage"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

func TestStorageRoutes(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = io.WriteString(w, `{"ApiVersion":"1.47"}`)
		case "/system/df":
			_, _ = io.WriteString(w, `{"LayersSize":1024,"Volumes":[{"Name":"app_data","UsageData":{"Size":2048,"RefCount":1}}]}`)
		case "/containers/json":
			_, _ = io.WriteString(w, `[{"Names":["/app"],"Mounts":[{"Type":"volume","Name":"app_data"}]}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	dk, _ := docker.New("tcp://" + strings.TrimPrefix(fake.URL, "http://"))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set := settings.Load(ctx, st, &config.Config{})
	snap := system.Snapshot{DiskTotal: 100 << 30, DiskUsed: 40 << 30}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{Storage: storage.New(ctx, st, dk, func() system.Snapshot { return snap }, set.Get, log)}
	scanned := make(chan struct{}, 1)
	s.Storage.OnVolumes = func([]storage.Volume) { scanned <- struct{}{} }

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/storage", s.getStorage)
	mux.HandleFunc("POST /api/v1/storage/refresh", s.refreshStorage)
	call := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	w := call("GET", "/api/v1/storage")
	var r map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || w.Code != 200 {
		t.Fatalf("GET %d : %s", w.Code, w.Body)
	}
	disk := r["disk"].(map[string]any)
	if disk["free"].(float64) != 60<<30 || disk["status"] != "insufficient" || disk["days_left"] != nil || r["volumes_at"].(float64) != 0 {
		t.Fatalf("GET avant tout calcul : %s", w.Body)
	}

	if w := call("POST", "/api/v1/storage/refresh"); w.Code != http.StatusAccepted {
		t.Fatalf("POST refresh : %d %s", w.Code, w.Body)
	}
	select {
	case <-scanned:
	case <-time.After(5 * time.Second):
		t.Fatal("calcul des volumes non terminé")
	}
	for i := 0; i < 100 && strings.Contains(call("GET", "/api/v1/storage").Body.String(), `"refreshing":true`); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	w = call("POST", "/api/v1/storage/refresh")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "réessaie dans") {
		t.Fatalf("second POST : %d %s", w.Code, w.Body)
	}
	body := call("GET", "/api/v1/storage").Body.String()
	if !strings.Contains(body, `"name":"app_data"`) || !strings.Contains(body, `"containers":["app"]`) {
		t.Fatalf("volumes : %s", body)
	}

	// Lecture seule : consultation permise, recalcul refusé.
	if !readOnlyAllowed(httptest.NewRequest("GET", "/api/v1/storage", nil)) || readOnlyAllowed(httptest.NewRequest("POST", "/api/v1/storage/refresh", nil)) {
		t.Fatal("droits en lecture seule incorrects")
	}
}
