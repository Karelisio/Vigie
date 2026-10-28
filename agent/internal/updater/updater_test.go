package updater

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
)

func newManager(t *testing.T, url string, prune bool) *Manager {
	dk, err := docker.New(url)
	if err != nil {
		t.Fatal(err)
	}
	m := New(dk, slog.New(slog.NewTextHandler(io.Discard, nil)), Hooks{}, Options{
		Enabled: true, PruneImages: func() bool { return prune },
	})
	m.settle, m.crashGrace, m.poll, m.helperDelay = 50*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond, 0
	return m
}

func waitJob(t *testing.T, m *Manager) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if jobs := m.Jobs(); len(jobs) > 0 && jobs[0].Done {
			return jobs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("opération non terminée")
	return Job{}
}

func compose(service string) map[string]any {
	return map[string]any{
		"com.docker.compose.project": "stremio", "com.docker.compose.service": service,
		"com.docker.compose.image": "sha256:old", "org.opencontainers.image.version": "1.0",
	}
}

func setupComet(f *fakeDocker) *fakeCtr {
	f.addImage("g0ldyy/comet:latest", "sha256:old", map[string]any{
		"Env": []any{"PATH=/usr/bin", "APP_VERSION=1.0"}, "Cmd": []any{"python", "main.py"},
		"Labels": map[string]any{"org.opencontainers.image.version": "1.0"},
	})
	f.remote["g0ldyy/comet:latest"] = "sha256:new"
	f.imageCfg["sha256:new"] = map[string]any{"Env": []any{"PATH=/usr/bin", "APP_VERSION=2.0"}, "Cmd": []any{"python", "app.py"}}
	return f.addContainer(&fakeCtr{
		ID: strings.Repeat("a", 64), Name: "comet", Image: "sha256:old", Running: true,
		Config: map[string]any{
			"Image": "g0ldyy/comet:latest", "Hostname": "aaaaaaaaaaaa",
			"Env":    []any{"PATH=/usr/bin", "APP_VERSION=1.0", "TOKEN=secret"},
			"Cmd":    []any{"python", "main.py"},
			"Labels": compose("comet"),
		},
		HostConfig: map[string]any{"NetworkMode": "arr_backend", "RestartPolicy": map[string]any{"Name": "unless-stopped"}},
		Networks: map[string]map[string]any{
			"arr_backend":       {"Aliases": []any{"comet", "aaaaaaaaaaaa"}, "IPAddress": "172.18.0.5", "MacAddress": "02:42"},
			"pangolin_frontend": {"Aliases": []any{"comet"}},
		},
	})
}

func TestUpdateRecreatesWithNewImage(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	m := newManager(t, srv.URL, true)

	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if !j.OK || len(j.Results) != 1 || !j.Results[0].Updated {
		t.Fatalf("job = %+v", j)
	}
	nc := f.byName("comet")
	if nc == nil || nc.ID == old.ID || nc.Image != "sha256:new" || !nc.Running {
		t.Fatalf("nouveau conteneur = %+v", nc)
	}
	if f.find(old.ID) != nil {
		t.Error("l'ancien conteneur n'a pas été supprimé")
	}
	cfg := nc.Config
	// Les valeurs de l'ancienne image laissent place à celles de la nouvelle.
	if env := cfg["Env"].([]any); len(env) != 1 || env[0] != "TOKEN=secret" {
		t.Errorf("Env = %v", env)
	}
	if _, ok := cfg["Cmd"]; ok {
		t.Errorf("Cmd hérité de l'ancienne image : %v", cfg["Cmd"])
	}
	if _, ok := cfg["Hostname"]; ok {
		t.Error("Hostname = ancien ID")
	}
	labels := cfg["Labels"].(map[string]any)
	if labels["com.docker.compose.project"] != "stremio" || labels["com.docker.compose.image"] != "sha256:new" {
		t.Errorf("labels = %v", labels)
	}
	if _, ok := labels["org.opencontainers.image.version"]; ok {
		t.Error("label de l'ancienne image recopié")
	}
	// Deux réseaux à la création (API ≥ 1.44), sans l'IP ni l'alias de l'ancien ID.
	if len(nc.Networks) != 2 {
		t.Fatalf("réseaux = %v", nc.Networks)
	}
	ep := nc.Networks["arr_backend"]
	if _, ok := ep["IPAddress"]; ok {
		t.Error("IP attribuée recopiée")
	}
	if a := ep["Aliases"].([]any); len(a) != 1 || a[0] != "comet" {
		t.Errorf("alias = %v", a)
	}
	if len(f.removedImg) != 1 || f.removedImg[0] != "sha256:old" {
		t.Errorf("images supprimées = %v", f.removedImg)
	}
}

func TestUpdateRollsBackWhenNewVersionCrashes(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	f.crashing["sha256:new"] = true
	m := newManager(t, srv.URL, true)

	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if j.OK || !strings.Contains(j.Error, "ancienne version rétablie") || !strings.Contains(j.Error, "boom") {
		t.Fatalf("job = %+v", j)
	}
	c := f.byName("comet")
	if c == nil || c.ID != old.ID || !c.Running {
		t.Fatalf("ancien conteneur non rétabli : %+v", c)
	}
	if len(f.containers) != 1 {
		t.Errorf("conteneurs restants = %d", len(f.containers))
	}
	if len(f.removedImg) != 0 {
		t.Error("image supprimée malgré l'échec")
	}
}

func TestAlreadyUpToDate(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	f.remote["g0ldyy/comet:latest"] = "sha256:old"
	m := newManager(t, srv.URL, true)
	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if !j.OK || j.Results[0].Updated || f.byName("comet").ID != old.ID {
		t.Fatalf("job = %+v", j)
	}
}

func TestDependentFollowsNetworkProvider(t *testing.T) {
	f, srv := newFake(t)
	f.addImage("fosrl/gerbil:latest", "sha256:g1", map[string]any{})
	f.remote["fosrl/gerbil:latest"] = "sha256:g2"
	f.imageCfg["sha256:g2"] = map[string]any{}
	f.addImage("traefik:v3", "sha256:t1", map[string]any{})
	gerbil := f.addContainer(&fakeCtr{
		ID: strings.Repeat("b", 64), Name: "gerbil", Image: "sha256:g1", Running: true,
		Config:     map[string]any{"Image": "fosrl/gerbil:latest"},
		HostConfig: map[string]any{"NetworkMode": "pangolin_frontend"},
		Networks:   map[string]map[string]any{"pangolin_frontend": {}},
	})
	f.addContainer(&fakeCtr{
		ID: strings.Repeat("c", 64), Name: "traefik", Image: "sha256:t1", Running: true,
		Config:     map[string]any{"Image": "traefik:v3", "Hostname": gerbil.ID[:12]},
		HostConfig: map[string]any{"NetworkMode": "container:" + gerbil.ID},
	})
	m := newManager(t, srv.URL, false)
	if _, err := m.UpdateContainer(Member{ID: gerbil.ID, Name: "gerbil", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if !j.OK {
		t.Fatalf("job = %+v", j)
	}
	ng, tr := f.byName("gerbil"), f.byName("traefik")
	if ng == nil || tr == nil || !tr.Running {
		t.Fatalf("gerbil = %+v, traefik = %+v", ng, tr)
	}
	if tr.HostConfig["NetworkMode"] != "container:"+ng.ID {
		t.Errorf("traefik pointe vers %v au lieu du nouveau gerbil", tr.HostConfig["NetworkMode"])
	}
	if len(f.containers) != 2 {
		t.Errorf("conteneurs restants = %d", len(f.containers))
	}
}

func TestOldDockerConnectsExtraNetworks(t *testing.T) {
	f, srv := newFake(t)
	f.api = "1.43"
	old := setupComet(f)
	m := newManager(t, srv.URL, false)
	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); !j.OK {
		t.Fatalf("job = %+v", j)
	}
	if len(f.connected) != 1 || f.connected[0] != "pangolin_frontend" {
		t.Errorf("réseaux connectés après création = %v", f.connected)
	}
	if len(f.byName("comet").Networks) != 2 {
		t.Error("réseau manquant")
	}
}

func TestBusyAndDisabled(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	m := newManager(t, srv.URL, false)
	m.opt.Enabled = false
	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet"}, false); err != ErrDisabled {
		t.Fatalf("err = %v", err)
	}
	if _, err := m.Group("stack", "stremio", "restart", []Member{{ID: old.ID, Name: "comet"}}); err != nil {
		t.Fatalf("redémarrer une stack ne dépend pas des mises à jour : %v", err)
	}
	waitJob(t, m)
	m.opt.Enabled = true
	m.current = &Job{}
	if _, err := m.UpdateContainer(Member{ID: old.ID, Name: "comet"}, false); err != ErrBusy {
		t.Fatalf("err = %v", err)
	}
}

func TestGroupSkipsAgentForStop(t *testing.T) {
	f, srv := newFake(t)
	old := setupComet(f)
	m := newManager(t, srv.URL, false)
	_, err := m.Group("stack", "stremio", "stop", []Member{
		{ID: old.ID, Name: "comet", Running: true},
		{ID: "self", Name: "vigie-agent", Self: true, Running: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if !j.OK || len(j.Results) != 2 || f.byName("comet").Running {
		t.Fatalf("job = %+v", j)
	}
}

func TestSplitRef(t *testing.T) {
	for ref, want := range map[string][2]string{
		"redis":                             {"redis", "latest"},
		"redis:7-alpine":                    {"redis", "7-alpine"},
		"ghcr.io/karelisio/vigie-agent:0.3": {"ghcr.io/karelisio/vigie-agent", "0.3"},
		"localhost:5000/app":                {"localhost:5000/app", "latest"},
		"localhost:5000/app:dev":            {"localhost:5000/app", "dev"},
		"nginx@sha256:abc":                  {"nginx@sha256:abc", ""},
	} {
		repo, tag := docker.SplitRef(ref)
		if repo != want[0] || tag != want[1] {
			t.Errorf("SplitRef(%q) = %q, %q", ref, repo, tag)
		}
	}
}

func TestBuildHelper(t *testing.T) {
	var self docker.ContainerJSON
	_ = json.Unmarshal([]byte(`{
		"Id": "`+strings.Repeat("d", 64)+`", "Name": "/vigie-agent", "Image": "sha256:agent1",
		"Config": {"Image": "ghcr.io/karelisio/vigie-agent:latest", "Hostname": "dddddddddddd",
			"Env": ["VIGIE_TOKEN=x"], "Labels": {"com.docker.compose.project": "vigie", "maintainer": "k"}},
		"HostConfig": {"NetworkMode": "arr_backend", "PortBindings": {"8080/tcp": [{"HostPort": "8080"}]},
			"RestartPolicy": {"Name": "unless-stopped"}, "Binds": ["/var/run/docker.sock:/var/run/docker.sock"]},
		"NetworkSettings": {"Networks": {"arr_backend": {"Aliases": ["vigie-agent"]}}}
	}`), &self)
	h := buildHelper(&self, "VIGIE_HELPER_PRUNE=true")
	if h["Image"] != "sha256:agent1" {
		t.Errorf("image = %v (doit rester le code en cours)", h["Image"])
	}
	env := h["Env"].([]any)
	if env[len(env)-2] != "VIGIE_HELPER_REPLACE="+self.ID || env[len(env)-1] != "VIGIE_HELPER_PRUNE=true" {
		t.Errorf("env = %v", env)
	}
	labels := h["Labels"].(map[string]any)
	if _, ok := labels["com.docker.compose.project"]; ok || labels["vigie.ignore"] != "true" {
		t.Errorf("labels = %v", labels)
	}
	hc := h["HostConfig"].(map[string]any)
	if hc["AutoRemove"] != true || hc["PortBindings"] != nil {
		t.Errorf("hostconfig = %v", hc)
	}
	ep := h["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)["arr_backend"].(map[string]any)
	if _, ok := ep["Aliases"]; ok {
		t.Error("le conteneur auxiliaire ne doit pas reprendre l'alias de l'agent")
	}
}

func TestAnonymousVolumesKept(t *testing.T) {
	var old docker.ContainerJSON
	_ = json.Unmarshal([]byte(`{
		"Id": "e", "Name": "/redis", "Image": "sha256:r1",
		"Config": {"Image": "redis:7"},
		"HostConfig": {"NetworkMode": "host", "Binds": ["/srv/conf:/etc/redis:ro"]},
		"Mounts": [
			{"Type": "volume", "Name": "abc123", "Destination": "/data", "RW": true},
			{"Type": "bind", "Source": "/srv/conf", "Destination": "/etc/redis", "RW": false}
		]
	}`), &old)
	spec := buildCreate(&old, nil, "", "", true)
	mounts := spec.Body["HostConfig"].(map[string]any)["Mounts"].([]any)
	if len(mounts) != 1 || mounts[0].(map[string]any)["Source"] != "abc123" {
		t.Errorf("mounts = %v", mounts)
	}
	if _, ok := spec.Body["NetworkingConfig"]; ok {
		t.Error("pas de configuration réseau en mode host")
	}
}

func TestCustomEntrypointKeepsCmd(t *testing.T) {
	cfg := map[string]any{"Entrypoint": []any{"/custom"}, "Cmd": []any{"serve"}}
	stripImageDefaults(cfg, map[string]any{"Entrypoint": []any{"/init"}, "Cmd": []any{"serve"}})
	if cfg["Cmd"] == nil || cfg["Entrypoint"] == nil {
		t.Errorf("cfg = %v", cfg)
	}
}

func TestSelfUpdateThroughHelper(t *testing.T) {
	f, srv := newFake(t)
	f.addImage("ghcr.io/karelisio/vigie-agent:latest", "sha256:a1", map[string]any{"Env": []any{"VIGIE_DATA_DIR=/data"}})
	f.remote["ghcr.io/karelisio/vigie-agent:latest"] = "sha256:a2"
	f.imageCfg["sha256:a2"] = map[string]any{"Env": []any{"VIGIE_DATA_DIR=/data"}}
	self := f.addContainer(&fakeCtr{
		ID: strings.Repeat("f", 64), Name: "vigie-agent", Image: "sha256:a1", Running: true,
		Config:     map[string]any{"Image": "ghcr.io/karelisio/vigie-agent:latest", "Env": []any{"VIGIE_DATA_DIR=/data", "TZ=Europe/Paris"}},
		HostConfig: map[string]any{"NetworkMode": "arr_backend"},
		Networks:   map[string]map[string]any{"arr_backend": {"Aliases": []any{"vigie-agent"}}},
	})
	m := newManager(t, srv.URL, false)
	m.opt.SelfID = self.ID

	// 1. L'agent télécharge sa nouvelle image et lance le conteneur auxiliaire.
	if _, err := m.UpdateContainer(Member{ID: self.ID, Name: "vigie-agent", Self: true, Running: true}, false); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, m); !j.OK || j.Kind != "self" {
		t.Fatalf("job = %+v", j)
	}
	var helper *fakeCtr
	for _, c := range f.containers {
		if strings.HasPrefix(c.Name, "vigie-agent-maj-") {
			helper = c
		}
	}
	if helper == nil || !helper.Running || helper.Image != "sha256:a1" {
		t.Fatalf("conteneur auxiliaire = %+v", helper)
	}

	// 2. Le conteneur auxiliaire remplace l'agent puis consigne le résultat.
	h := newManager(t, srv.URL, false)
	h.opt.DataDir = t.TempDir()
	h.opt.Version = "0.3.0"
	if err := h.RunHelper(t.Context(), self.ID); err != nil {
		t.Fatal(err)
	}
	agent := f.byName("vigie-agent")
	if agent == nil || agent.ID == self.ID || agent.Image != "sha256:a2" || !agent.Running {
		t.Fatalf("nouvel agent = %+v", agent)
	}
	if r := LastSelfUpdate(h.opt.DataDir); r == nil || !r.OK || r.From != "0.3.0" {
		t.Fatalf("résultat = %+v", r)
	}
}

func TestDependentFailureRollsBackProvider(t *testing.T) {
	f, srv := newFake(t)
	f.addImage("fosrl/gerbil:latest", "sha256:g1", map[string]any{})
	f.remote["fosrl/gerbil:latest"] = "sha256:g2"
	f.imageCfg["sha256:g2"] = map[string]any{}
	f.addImage("traefik:v3", "sha256:t1", map[string]any{})
	f.addImage("crowdsec:latest", "sha256:c1", map[string]any{})
	gerbil := f.addContainer(&fakeCtr{
		ID: strings.Repeat("b", 64), Name: "gerbil", Image: "sha256:g1", Running: true,
		Config: map[string]any{"Image": "fosrl/gerbil:latest"}, HostConfig: map[string]any{"NetworkMode": "pangolin_frontend"},
		Networks: map[string]map[string]any{"pangolin_frontend": {}},
	})
	for _, n := range []string{"crowdsec", "traefik"} {
		f.addContainer(&fakeCtr{
			ID: strings.Repeat(n[:1], 64), Name: n, Image: map[string]string{"crowdsec": "sha256:c1", "traefik": "sha256:t1"}[n], Running: true,
			Config:     map[string]any{"Image": map[string]string{"crowdsec": "crowdsec:latest", "traefik": "traefik:v3"}[n]},
			HostConfig: map[string]any{"NetworkMode": "container:" + gerbil.ID},
		})
	}
	f.failCreate["traefik"] = true
	m := newManager(t, srv.URL, false)
	if _, err := m.UpdateContainer(Member{ID: gerbil.ID, Name: "gerbil", Running: true}, false); err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if j.OK || !strings.Contains(j.Error, "traefik") {
		t.Fatalf("job = %+v", j)
	}
	g := f.byName("gerbil")
	if g == nil || g.ID != gerbil.ID || !g.Running {
		t.Fatalf("ancien gerbil non rétabli : %+v", g)
	}
	for _, n := range []string{"crowdsec", "traefik"} {
		c := f.byName(n)
		if c == nil || !c.Running || c.HostConfig["NetworkMode"] != "container:"+gerbil.ID {
			t.Errorf("%s = %+v", n, c)
		}
	}
	if len(f.containers) != 3 {
		t.Errorf("conteneurs restants = %d", len(f.containers))
	}
}

func TestStackUpdateWithDependentMember(t *testing.T) {
	f, srv := newFake(t)
	f.addImage("fosrl/gerbil:latest", "sha256:g1", map[string]any{})
	f.remote["fosrl/gerbil:latest"] = "sha256:g2"
	f.imageCfg["sha256:g2"] = map[string]any{}
	f.addImage("traefik:v3", "sha256:t1", map[string]any{})
	f.remote["traefik:v3"] = "sha256:t1"
	gerbil := f.addContainer(&fakeCtr{
		ID: strings.Repeat("b", 64), Name: "gerbil", Image: "sha256:g1", Running: true,
		Config: map[string]any{"Image": "fosrl/gerbil:latest"}, HostConfig: map[string]any{"NetworkMode": "pangolin_frontend"},
		Networks: map[string]map[string]any{"pangolin_frontend": {}},
	})
	traefik := f.addContainer(&fakeCtr{
		ID: strings.Repeat("c", 64), Name: "traefik", Image: "sha256:t1", Running: true,
		Config: map[string]any{"Image": "traefik:v3"}, HostConfig: map[string]any{"NetworkMode": "container:" + gerbil.ID},
	})
	m := newManager(t, srv.URL, false)
	_, err := m.Group("stack", "pangolin", "update", []Member{
		{ID: traefik.ID, Name: "traefik", Running: true}, {ID: gerbil.ID, Name: "gerbil", Running: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, m)
	if !j.OK || len(j.Results) != 2 {
		t.Fatalf("job = %+v", j)
	}
	if j.Results[0].Name != "gerbil" {
		t.Errorf("le fournisseur de réseau doit passer en premier : %+v", j.Results)
	}
	if tr := f.byName("traefik"); tr == nil || !tr.Running || tr.HostConfig["NetworkMode"] != "container:"+f.byName("gerbil").ID {
		t.Errorf("traefik = %+v", tr)
	}
}
