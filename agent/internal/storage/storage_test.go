package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

const hexName = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeDocker simule /version, /system/df et /containers/json.
type fakeDocker struct {
	mu      sync.Mutex
	api     string
	queries []string         // requêtes reçues sur /system/df
	sizes   map[string]int64 // taille des volumes nommés
	block   chan struct{}    // /system/df attend sa fermeture (calcul long)
	fail    bool
}

func (f *fakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	api, fail, block := f.api, f.fail, f.block
	sizes := map[string]int64{}
	for k, v := range f.sizes {
		sizes[k] = v
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/version":
		_ = json.NewEncoder(w).Encode(map[string]string{"Version": "27.0.0", "ApiVersion": api})
	case "/system/df":
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.RawQuery)
		f.mu.Unlock()
		if block != nil {
			<-block
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"disk usage en cours"}`)
			return
		}
		vols := []map[string]any{
			{"Name": hexName, "Labels": map[string]string{}, "UsageData": map[string]int64{"Size": 100 << 20, "RefCount": 0}},
			{"Name": "build_tmp", "Labels": map[string]string{"com.docker.volume.anonymous": ""}, "UsageData": map[string]int64{"Size": 50 << 20, "RefCount": 1}},
			{"Name": "s3fs", "Driver": "rexray", "Labels": nil, "UsageData": map[string]int64{"Size": -1, "RefCount": 1}},
		}
		for name, size := range sizes {
			vols = append(vols, map[string]any{
				"Name": name, "Labels": map[string]string{"com.docker.compose.project": strings.Split(name, "_")[0]},
				"UsageData": map[string]int64{"Size": size, "RefCount": 1},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"LayersSize": 5 << 30,
			"Images":     []map[string]int64{{"Size": 3 << 30, "SharedSize": 1 << 30}, {"Size": 3 << 30, "SharedSize": 1 << 30}},
			"Volumes":    vols,
			"BuildCache": []map[string]any{{"Size": 200 << 20, "Shared": false}, {"Size": 300 << 20, "Shared": true}},
		})
	case "/containers/json":
		mount := func(typ, name string) map[string]string { return map[string]string{"Type": typ, "Name": name} }
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Names": []string{"/immich_server"}, "Mounts": []map[string]string{mount("volume", "immich_pgdata"), mount("bind", "")}},
			{"Names": []string{"/immich_postgres"}, "State": "exited", "Mounts": []map[string]string{mount("volume", "immich_pgdata")}},
			{"Names": []string{"/web"}, "Mounts": []map[string]string{}},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"inconnu"}`)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T, f *fakeDocker, snap system.Snapshot) (*Service, *store.Store, *clock) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	dk, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	set := settings.Load(context.Background(), st, &config.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(ctx, st, dk, func() system.Snapshot { return snap }, set.Get, log)
	c := &clock{t: time.Now().Truncate(time.Minute)}
	s.now = c.now
	return s, st, c
}

func (s *Service) scanSync(t *testing.T) {
	t.Helper()
	if err := s.begin(false); err != nil {
		t.Fatal(err)
	}
	s.scan(context.Background())
}

func TestScanReadsSystemDF(t *testing.T) {
	f := &fakeDocker{api: "1.47", sizes: map[string]int64{"immich_pgdata": 3 << 30, "jellyfin_config": 200 << 20}}
	s, _, _ := newService(t, f, system.Snapshot{})
	var notified []Volume
	s.OnVolumes = func(v []Volume) { notified = v }
	s.scanSync(t)

	if len(f.queries) != 1 || f.queries[0] != "type=image&type=volume&type=build-cache" {
		t.Fatalf("requêtes /system/df : %q", f.queries)
	}
	r := s.Report(context.Background())
	if r.Error != "" || r.VolumesAt == 0 || r.Refreshing {
		t.Fatalf("état : %+v", r)
	}
	if len(r.Volumes) != 2 || r.Volumes[0].Name != "immich_pgdata" || r.Volumes[1].Name != "jellyfin_config" {
		t.Fatalf("volumes (nommés, du plus gros au plus petit) : %+v", r.Volumes)
	}
	v := r.Volumes[0]
	if v.Project != "immich" || v.Size != 3<<30 || strings.Join(v.Containers, ",") != "immich_postgres,immich_server" {
		t.Fatalf("volume : %+v", v)
	}
	if v.GrowthDay != nil || v.GrowthWeek != nil || len(v.History) != 1 || v.Fast {
		t.Fatalf("premier calcul : pas de croissance, une mesure : %+v", v)
	}
	if len(r.Volumes[1].Containers) != 0 || r.Volumes[1].Containers == nil {
		t.Fatalf("conteneurs d'un volume inutilisé : %#v", r.Volumes[1].Containers)
	}
	if r.Images != (Usage{Count: 2, Size: 5 << 30}) || r.BuildCache != (Usage{Count: 2, Size: 200 << 20}) {
		t.Fatalf("images %+v, cache %+v", r.Images, r.BuildCache)
	}
	if r.Anonymous != (Usage{Count: 2, Size: 150 << 20}) {
		t.Fatalf("volumes anonymes : %+v", r.Anonymous)
	}
	if r.FastGrowth != 5<<30 || len(notified) != 2 {
		t.Fatalf("seuil %d, volumes transmis aux alertes : %d", r.FastGrowth, len(notified))
	}

	// JSON : champs attendus par l'appli, croissance absente → null.
	raw, _ := json.Marshal(r)
	for _, k := range []string{`"volumes_at":`, `"growth_day":null`, `"growth_week":null`, `"build_cache":{"count":2`, `"trend_per_day":null`, `"days_left":null`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("JSON sans %s : %s", k, raw)
		}
	}

	// Redémarrage : le dernier calcul est servi tout de suite.
	again := New(context.Background(), s.st, s.dk, s.snapshot, s.settings, s.log)
	if r2 := again.Report(context.Background()); len(r2.Volumes) != 2 || r2.VolumesAt != r.VolumesAt {
		t.Fatalf("après redémarrage : %+v", r2)
	}
}

func TestScheduleFollowsLastScan(t *testing.T) {
	f := &fakeDocker{api: "1.47", sizes: map[string]int64{"data": 1 << 30}}
	s, _, c := newService(t, f, system.Snapshot{})
	if d := s.untilDue(); d != 0 {
		t.Fatalf("jamais calculé : %v", d)
	}
	s.scanSync(t) // calcul à la demande : le calcul planifié est repoussé de 6 h
	c.add(2 * time.Hour)
	if d := s.untilDue(); d != 4*time.Hour {
		t.Fatalf("prochain calcul dans %v, attendu 4 h", d)
	}
}

func TestScanOldDockerWithoutTypeFilter(t *testing.T) {
	f := &fakeDocker{api: "1.41", sizes: map[string]int64{"data": 1 << 30}}
	s, _, _ := newService(t, f, system.Snapshot{})
	s.scanSync(t)
	if len(f.queries) != 1 || f.queries[0] != "" {
		t.Fatalf("Docker < 23 : pas de filtre « type », obtenu %q", f.queries)
	}
}

func TestScanErrorKeepsLastResult(t *testing.T) {
	f := &fakeDocker{api: "1.47", sizes: map[string]int64{"data": 1 << 30}}
	s, _, _ := newService(t, f, system.Snapshot{})
	s.scanSync(t)
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	s.scanSync(t)
	r := s.Report(context.Background())
	if !strings.HasPrefix(r.Error, "Docker : ") || len(r.Volumes) != 1 {
		t.Fatalf("erreur %q, volumes %+v", r.Error, r.Volumes)
	}
}

func TestVolumeGrowth(t *testing.T) {
	f := &fakeDocker{api: "1.47", sizes: map[string]int64{"logs_data": 9 << 30, "calme_db": 1 << 30}}
	s, st, c := newService(t, f, system.Snapshot{})
	ctx := context.Background()
	now := c.now()
	at := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	// logs_data : 2 Gio il y a 24 h, 1 Gio il y a 7 jours ; calme_db stable.
	for _, p := range []struct {
		ago  time.Duration
		logs int64
	}{{7 * 24 * time.Hour, 1 << 30}, {30 * time.Hour, 1 << 30}, {24 * time.Hour, 2 << 30}, {12 * time.Hour, 5 << 30}} {
		if err := st.InsertVolumeSizes(ctx, at(p.ago), map[string]int64{"logs_data": p.logs, "calme_db": 1 << 30}); err != nil {
			t.Fatal(err)
		}
	}
	var notified []Volume
	s.OnVolumes = func(v []Volume) { notified = v }
	s.scanSync(t)
	r := s.Report(ctx)
	byName := map[string]Volume{}
	for _, v := range r.Volumes {
		byName[v.Name] = v
	}
	logs, calm := byName["logs_data"], byName["calme_db"]
	if logs.GrowthDay == nil || *logs.GrowthDay != 7<<30 || logs.GrowthWeek == nil || *logs.GrowthWeek != 8<<30 || !logs.Fast {
		t.Fatalf("logs_data : %+v", logs)
	}
	if calm.GrowthDay == nil || *calm.GrowthDay != 0 || calm.Fast {
		t.Fatalf("calme_db : %+v", calm)
	}
	if len(logs.History) != 5 || logs.History[4].Size != 9<<30 {
		t.Fatalf("historique : %+v", logs.History)
	}
	if len(notified) != 2 {
		t.Fatalf("volumes transmis : %+v", notified)
	}

	// Mesure trop récente (moins de 18 h) : pas de croissance sur 24 h.
	pts := []store.SizePoint{{TS: at(10 * time.Hour), Size: 1}, {TS: at(0), Size: 2}}
	if g := growth(pts, 24*time.Hour, 18*time.Hour, 30*time.Hour); g != nil {
		t.Fatalf("croissance sans recul : %d", *g)
	}
	// 20 h d'écart : ramenée à 24 h.
	pts = []store.SizePoint{{TS: at(20 * time.Hour), Size: 0}, {TS: at(0), Size: 10 << 30}}
	if g := growth(pts, 24*time.Hour, 18*time.Hour, 30*time.Hour); g == nil || *g != 12<<30 {
		t.Fatalf("croissance ramenée à 24 h : %v", g)
	}
}

func TestRefreshLimits(t *testing.T) {
	f := &fakeDocker{api: "1.47", sizes: map[string]int64{"data": 1 << 30}, block: make(chan struct{})}
	s, _, c := newService(t, f, system.Snapshot{})
	done := make(chan struct{}, 2)
	s.OnVolumes = func([]Volume) { done <- struct{}{} }

	if _, err := s.Refresh(); err != nil {
		t.Fatal(err)
	}
	// Calcul en cours : pas de second calcul en parallèle, pas d'erreur non plus.
	if _, err := s.Refresh(); err != nil {
		t.Fatalf("pendant le calcul : %v", err)
	}
	if !s.Report(context.Background()).Refreshing {
		t.Fatal("refreshing attendu pendant le calcul")
	}
	close(f.block)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("calcul non terminé")
	}
	for s.Report(context.Background()).Refreshing {
		time.Sleep(10 * time.Millisecond)
	}
	c.add(20 * time.Second)
	wait, err := s.Refresh()
	if !errors.Is(err, ErrTooSoon) || wait != 40*time.Second {
		t.Fatalf("calcul rapproché : %v, attente %v", err, wait)
	}
	f.mu.Lock()
	n := len(f.queries)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d appels à /system/df, attendu 1", n)
	}
	c.add(41 * time.Second)
	if _, err := s.Refresh(); err != nil {
		t.Fatalf("après une minute : %v", err)
	}
	for s.Report(context.Background()).Refreshing {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDiskForecastFromHistory(t *testing.T) {
	total := uint64(47 << 30)
	used := uint64(30 << 30)
	s, st, c := newService(t, &fakeDocker{api: "1.47"}, system.Snapshot{
		DiskTotal: total, DiskUsed: used, Disks: []system.Disk{{Mount: "/", Total: total, Used: used}},
	})
	ctx := context.Background()

	r := s.Report(ctx)
	if r.Disk.Status != StatusInsufficient || r.Disk.PerDay != nil || r.Disk.DaysLeft != nil || r.Disk.Free != 17<<30 {
		t.Fatalf("sans historique : %+v", r.Disk)
	}

	// 3 jours à +1 Gio par jour, en pourcentage du disque (comme l'historique système).
	now := time.Now()
	for m := 72 * 60; m >= 0; m-- {
		ts := now.Add(-time.Duration(m) * time.Minute)
		u := float64(used) - float64(m)/1440*float64(1<<30)
		pct := math.Round(u/float64(total)*1000) / 10
		if err := st.InsertSys(ctx, store.SysSample{TS: ts.UnixMilli(), Disk: pct}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Aggregate(ctx, 80*time.Hour); err != nil {
		t.Fatal(err)
	}
	c.add(2 * time.Minute) // cache de la prévision expiré
	d := s.Report(ctx).Disk
	if d.Status != StatusOK || d.PerDay == nil || d.DaysLeft == nil {
		t.Fatalf("prévision : %+v", d)
	}
	if math.Abs(float64(*d.PerDay)/(1<<30)-1) > 0.1 || math.Abs(*d.DaysLeft-17) > 2 {
		t.Fatalf("tendance %.2f Gio/jour, %.1f jours", float64(*d.PerDay)/(1<<30), *d.DaysLeft)
	}
	if len(d.Points) < 70 || d.Points[len(d.Points)-1].Used != int64(used) || d.Hours < 70 {
		t.Fatalf("%d points, recul %.1f h", len(d.Points), d.Hours)
	}
}
