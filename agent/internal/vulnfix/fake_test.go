package vulnfix

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

// ---- Démon Docker simulé : images avec système de fichiers, conteneurs, commit, archive ----

type fsNode struct {
	data string
	link string
}

type fakeFS map[string]fsNode

func (fs fakeFS) clone() fakeFS {
	out := fakeFS{}
	for k, v := range fs {
		out[k] = v
	}
	return out
}

type fakeImage struct {
	id      string
	cfg     map[string]any
	digests []string
	fs      fakeFS
}

type fakeCtr struct {
	id, name, image string
	cfg             map[string]any
	hc              map[string]any
	fs              fakeFS
	running, done   bool
	exit            int
	logs            []string
}

type daemon struct {
	t   *testing.T
	mu  sync.Mutex
	seq int

	images     map[string]*fakeImage
	tags       map[string]string     // référence → ID
	registry   map[string]string     // référence publiée → ID (téléchargement)
	byDigest   map[string]*fakeImage // « dépôt@sha256:… » publié → image (re-téléchargement)
	containers map[string]*fakeCtr
	created    []map[string]any // corps des créations
	commits    []map[string]any // configurations reçues au commit
	removedImg []string
	pulls      int

	// upgrade simule la commande du conteneur temporaire : modifie son système de fichiers et
	// renvoie le code de sortie et le journal.
	upgrade func(c *fakeCtr, script string) (int, []string)
	// commitMutate altère la configuration de l'image créée (essai de la vérification de fidélité).
	commitMutate func(cfg map[string]any)
}

func newDaemon(t *testing.T) (*daemon, *docker.Client) {
	d := &daemon{
		t: t, images: map[string]*fakeImage{}, tags: map[string]string{}, registry: map[string]string{},
		containers: map[string]*fakeCtr{}, byDigest: map[string]*fakeImage{},
	}
	srv := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(srv.Close)
	dk, err := docker.New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return d, dk
}

func copyJSON(v any) map[string]any {
	b, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (d *daemon) addImage(id string, refs []string, digests []string, cfg map[string]any, fs fakeFS) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[id] = &fakeImage{id: id, cfg: copyJSON(cfg), digests: digests, fs: fs}
	for _, r := range refs {
		d.tags[r] = id
	}
}

func (d *daemon) publish(ref, id string) {
	d.mu.Lock()
	d.registry[ref] = id
	d.mu.Unlock()
}

func (d *daemon) addContainer(name, ref string, running bool, labels map[string]any) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	img := d.images[d.tags[ref]]
	d.seq++
	id := fmt.Sprintf("%064d", d.seq)
	cfg := copyJSON(img.cfg)
	cfg["Image"] = ref
	l, _ := cfg["Labels"].(map[string]any)
	if l == nil {
		l = map[string]any{}
	}
	for k, v := range labels {
		l[k] = v
	}
	cfg["Labels"] = l
	cfg["Env"] = append(toList(cfg["Env"]), "SECRET=1")
	d.containers[id] = &fakeCtr{id: id, name: name, image: img.id, cfg: cfg, hc: map[string]any{"NetworkMode": "bridge", "RestartPolicy": map[string]any{"Name": "unless-stopped"}}, fs: img.fs.clone(), running: running}
	return id
}

func toList(v any) []any {
	l, _ := v.([]any)
	return append([]any{}, l...)
}

func (d *daemon) byName(name string) *fakeCtr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.find(name)
}

func (d *daemon) find(ref string) *fakeCtr {
	for _, c := range d.containers {
		if c.id == ref || c.name == ref {
			return c
		}
	}
	return nil
}

func (d *daemon) resolve(ref string) *fakeImage {
	if id, ok := d.tags[ref]; ok {
		return d.images[id]
	}
	if img, ok := d.images[ref]; ok {
		return img
	}
	for _, img := range d.images {
		for _, dg := range img.digests {
			if dg == ref {
				return img
			}
		}
	}
	return nil
}

func (d *daemon) tagOf(ref string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tags[ref]
}

func (d *daemon) image(id string) *fakeImage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[id]
}

func (d *daemon) names() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, c := range d.containers {
		out = append(out, c.name)
	}
	sort.Strings(out)
	return out
}

func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// tarOf imite GET /containers/{id}/archive : le dernier élément d'un lien n'est pas suivi.
func tarOf(fs fakeFS, p string) ([]byte, bool) {
	n, ok := fs[p]
	if !ok {
		return nil, false
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if n.link != "" {
		_ = tw.WriteHeader(&tar.Header{Name: path.Base(p), Typeflag: tar.TypeSymlink, Linkname: n.link, Mode: 0o777})
	} else {
		_ = tw.WriteHeader(&tar.Header{Name: path.Base(p), Typeflag: tar.TypeReg, Size: int64(len(n.data)), Mode: 0o644})
		_, _ = tw.Write([]byte(n.data))
	}
	_ = tw.Close()
	return buf.Bytes(), true
}

func (d *daemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, q := r.URL.Path, r.URL.Query()
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/version":
		send(w, 200, map[string]any{"ApiVersion": "1.47", "Version": "fake"})

	case p == "/containers/json":
		var out []map[string]any
		for _, c := range d.containers {
			state := "exited"
			if c.running {
				state = "running"
			}
			out = append(out, map[string]any{"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.cfg["Image"], "ImageID": c.image, "State": state})
		}
		send(w, 200, out)

	case p == "/containers/create":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.created = append(d.created, copyJSON(body))
		name := q.Get("name")
		if d.find(name) != nil {
			send(w, 409, map[string]any{"message": "name in use"})
			return
		}
		ref, _ := body["Image"].(string)
		img := d.resolve(ref)
		if img == nil {
			send(w, 404, map[string]any{"message": "no such image " + ref})
			return
		}
		hc, _ := body["HostConfig"].(map[string]any)
		delete(body, "HostConfig")
		delete(body, "NetworkingConfig")
		// Comme Docker : la configuration de l'image complète celle demandée.
		cfg := copyJSON(img.cfg)
		for k, v := range body {
			cfg[k] = v
		}
		if _, ok := body["Entrypoint"]; ok {
			if _, ok := body["Cmd"]; !ok {
				delete(cfg, "Cmd")
			}
		}
		labels, _ := img.cfg["Labels"].(map[string]any)
		merged := copyJSON(labels)
		if l, ok := body["Labels"].(map[string]any); ok {
			for k, v := range l {
				merged[k] = v
			}
		}
		cfg["Labels"] = merged
		d.seq++
		id := fmt.Sprintf("%064d", d.seq)
		d.containers[id] = &fakeCtr{id: id, name: name, image: img.id, cfg: cfg, hc: hc, fs: img.fs.clone()}
		send(w, 201, map[string]any{"Id": id})

	case p == "/commit":
		c := d.find(q.Get("container"))
		if c == nil {
			send(w, 404, map[string]any{"message": "no such container"})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.commits = append(d.commits, copyJSON(body))
		cfg := copyJSON(body)
		if d.commitMutate != nil {
			d.commitMutate(cfg)
		}
		d.seq++
		id := fmt.Sprintf("sha256:commit%d", d.seq)
		d.images[id] = &fakeImage{id: id, cfg: cfg, fs: c.fs.clone()}
		send(w, 201, map[string]any{"Id": id})

	case strings.HasPrefix(p, "/images/create"):
		d.pulls++
		ref := q.Get("fromImage") + ":" + q.Get("tag")
		if strings.HasPrefix(q.Get("tag"), "sha256:") {
			ref = q.Get("fromImage") + "@" + q.Get("tag")
			if img := d.resolve(ref); img != nil {
				w.WriteHeader(200)
				return
			}
			if img, ok := d.byDigest[ref]; ok {
				d.images[img.id] = img
				w.WriteHeader(200)
				return
			}
			send(w, 200, map[string]any{"error": "manifest unknown"})
			return
		}
		id, ok := d.registry[ref]
		if !ok {
			send(w, 200, map[string]any{"error": "manifest unknown"})
			return
		}
		d.tags[ref] = id
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"Pull complete","id":"l1"}`)

	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/tag"):
		img := d.resolve(strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/tag"))
		if img == nil {
			send(w, 404, map[string]any{"message": "no such image"})
			return
		}
		d.tags[q.Get("repo")+":"+q.Get("tag")] = img.id
		w.WriteHeader(201)

	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json"):
		img := d.resolve(strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json"))
		if img == nil {
			send(w, 404, map[string]any{"message": "no such image"})
			return
		}
		send(w, 200, map[string]any{"Id": img.id, "Config": img.cfg, "RepoDigests": img.digests, "Created": "2026-09-01T10:00:00Z"})

	case strings.HasPrefix(p, "/images/") && r.Method == http.MethodDelete:
		img := d.resolve(strings.TrimPrefix(p, "/images/"))
		if img == nil {
			send(w, 404, map[string]any{"message": "no such image"})
			return
		}
		for _, c := range d.containers {
			if c.image == img.id {
				send(w, 409, map[string]any{"message": "image is being used"})
				return
			}
		}
		d.removedImg = append(d.removedImg, img.id)
		delete(d.images, img.id)
		for ref, id := range d.tags {
			if id == img.id {
				delete(d.tags, ref)
			}
		}
		send(w, 200, []any{})

	case len(parts) >= 2 && parts[0] == "containers":
		c := d.find(parts[1])
		if c == nil {
			send(w, 404, map[string]any{"message": "no such container"})
			return
		}
		action := ""
		if len(parts) == 3 {
			action = parts[2]
		}
		switch {
		case r.Method == http.MethodDelete:
			if c.running && q.Get("force") != "true" {
				send(w, 409, map[string]any{"message": "running"})
				return
			}
			delete(d.containers, c.id)
			w.WriteHeader(204)
		case action == "json":
			status := "created"
			switch {
			case c.running:
				status = "running"
			case c.done:
				status = "exited"
			}
			send(w, 200, map[string]any{
				"Id": c.id, "Name": "/" + c.name, "Image": c.image,
				"State":  map[string]any{"Status": status, "Running": c.running, "ExitCode": c.exit},
				"Config": c.cfg, "HostConfig": c.hc, "NetworkSettings": map[string]any{"Networks": map[string]any{}}, "Mounts": []any{},
			})
		case action == "archive":
			if b, ok := tarOf(c.fs, q.Get("path")); ok {
				w.Header().Set("Content-Type", "application/x-tar")
				_, _ = w.Write(b)
				return
			}
			send(w, 404, map[string]any{"message": "Could not find the file"})
		case action == "start":
			if strings.HasPrefix(c.name, vulns.TempPatchPrefix) {
				script := ""
				if ep := toList(c.cfg["Entrypoint"]); len(ep) == 3 {
					script, _ = ep[2].(string)
				} else if cmd := toList(c.cfg["Cmd"]); len(cmd) == 3 {
					script, _ = cmd[2].(string)
				}
				code, logs := 0, []string(nil)
				if d.upgrade != nil {
					code, logs = d.upgrade(c, script)
				}
				c.exit, c.logs, c.done = code, logs, true
			} else {
				c.running = true
			}
			w.WriteHeader(204)
		case action == "stop":
			c.running = false
			c.done = true
			w.WriteHeader(204)
		case action == "rename":
			c.name = q.Get("name")
			w.WriteHeader(204)
		case action == "logs":
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			for _, l := range c.logs {
				fmt.Fprintln(w, "2026-10-28T10:00:00.000000000Z "+l)
			}
		default:
			w.WriteHeader(404)
		}

	case len(parts) == 3 && parts[0] == "networks":
		w.WriteHeader(200)

	default:
		d.t.Logf("route non simulée : %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}

// ---- Système Debian simulé ----

const osRelease = "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian\nVERSION_ID=\"12\"\n"

type pkg struct{ name, source, version string }

func dpkgStatus(pkgs []pkg) string {
	var b strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&b, "Package: %s\nStatus: install ok installed\nArchitecture: arm64\nVersion: %s\n", p.name, p.version)
		if p.source != p.name {
			fmt.Fprintf(&b, "Source: %s\n", p.source)
		}
		b.WriteString("Description: x\n\n")
	}
	return b.String()
}

func debianFS(pkgs []pkg) fakeFS {
	return fakeFS{
		"/etc/os-release":      {data: osRelease},
		"/bin/sh":              {link: "dash"},
		"/bin/dash":            {data: "ELF"},
		"/usr/bin/apt-get":     {data: "ELF"},
		"/var/lib/dpkg/status": {data: dpkgStatus(pkgs)},
	}
}

var basePkgs = []pkg{
	{"dpkg", "dpkg", "1.21.22"}, {"libssl3", "openssl", "3.0.11-1"}, {"openssl", "openssl", "3.0.11-1"},
	{"libc6", "glibc", "2.36-9"}, {"zlib1g", "zlib", "1.2.13"},
}

// aptUpgrade simule apt-get install --only-upgrade : les paquets nommés passent à la version du dépôt.
func aptUpgrade(repo map[string]string) func(c *fakeCtr, script string) (int, []string) {
	return func(c *fakeCtr, script string) (int, []string) {
		st := c.fs["/var/lib/dpkg/status"].data
		var out []string
		for name, v := range repo {
			if !strings.Contains(script, "'"+name+"'") {
				continue
			}
			lines := strings.Split(st, "\n")
			for i := 0; i < len(lines); i++ {
				if lines[i] == "Package: "+name {
					for j := i; j < len(lines) && lines[j] != ""; j++ {
						if strings.HasPrefix(lines[j], "Version: ") {
							lines[j] = "Version: " + v
						}
					}
				}
			}
			st = strings.Join(lines, "\n")
			out = append(out, "Setting up "+name+" ("+v+") ...")
		}
		c.fs["/var/lib/dpkg/status"] = fsNode{data: st}
		return 0, append(out, "vigie: ok")
	}
}

var baseConfig = map[string]any{
	"User": "app", "Env": []any{"PATH=/usr/bin", "MODE=prod"}, "Entrypoint": []any{"/app/start"}, "Cmd": []any{"--serve"},
	"WorkingDir": "/app", "ExposedPorts": map[string]any{"8080/tcp": map[string]any{}}, "StopSignal": "SIGINT",
	"Labels":      map[string]any{"org.opencontainers.image.version": "1.4.2", "org.opencontainers.image.source": "https://github.com/x/comet"},
	"Healthcheck": map[string]any{"Test": []any{"CMD", "true"}, "Interval": 30000000000},
}

// ---- Analyse des failles simulée ----

type rule struct{ id, sev, fixed string }

type fakeScanner struct {
	dk      *docker.Client
	d       *daemon
	mu      sync.Mutex
	invs    map[string]vulns.Inventory
	rules   map[string][]rule // paquet source → failles
	saved   []string
	rescans int
	delayed int             // analyses différées (après une nouvelle version installée)
	updates map[string]bool // conteneur → nouvelle version publiée
}

func (s *fakeScanner) Assess(_ context.Context, inv vulns.Inventory) ([]vulns.Vuln, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []vulns.Vuln
	for _, p := range inv.Packages {
		for _, r := range s.rules[p[1]] {
			if r.fixed == "" || p[2] < r.fixed {
				out = append(out, vulns.Vuln{ID: r.id, Severity: r.sev, Package: p[1], Version: p[2], Fixed: r.fixed, Ecosystem: p[0]})
			}
		}
	}
	return out, nil
}

func (s *fakeScanner) StoredInventory(_ context.Context, id string) (vulns.Inventory, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invs[id]
	return inv, ok
}

func (s *fakeScanner) SaveInventory(_ context.Context, id string, inv vulns.Inventory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invs[id] = inv
	s.saved = append(s.saved, id)
}

func (s *fakeScanner) Rescan() {
	s.mu.Lock()
	s.rescans++
	s.mu.Unlock()
}

func (s *fakeScanner) ImagesUpdated() {
	s.mu.Lock()
	s.delayed++
	s.mu.Unlock()
}

// Report regroupe les conteneurs par image ; inventaire lu dans un conteneur au besoin.
func (s *fakeScanner) Report(ctx context.Context) vulns.Report {
	list, _ := s.dk.List(ctx)
	byImage := map[string]*vulns.Image{}
	var order []string
	for _, c := range list {
		if vulns.Temporary(c.Name()) {
			continue
		}
		img := byImage[c.ImageID]
		if img == nil {
			img = &vulns.Image{ID: c.ImageID, Ref: c.Image}
			byImage[c.ImageID] = img
			order = append(order, c.ImageID)
		}
		img.Containers = append(img.Containers, vulns.ContainerRef{ID: c.ID, Name: c.Name(), State: c.State})
		s.mu.Lock()
		_, ok := s.invs[c.ImageID]
		s.mu.Unlock()
		if !ok {
			inv := vulns.ReadInventory(ctx, s.dk, c.ID, nil, time.Now())
			s.mu.Lock()
			s.invs[c.ImageID] = inv
			s.mu.Unlock()
		}
	}
	sort.Strings(order)
	rep := vulns.Report{LastScan: 1}
	for _, id := range order {
		img := byImage[id]
		sort.Slice(img.Containers, func(i, j int) bool {
			ri, rj := img.Containers[i].State == "running", img.Containers[j].State == "running"
			if ri != rj {
				return ri
			}
			return img.Containers[i].Name < img.Containers[j].Name
		})
		inv, _ := s.StoredInventory(ctx, id)
		img.OS, img.Ecosystem = inv.OS, inv.Eco
		all, _ := s.Assess(ctx, inv)
		img.Vulns, img.Counts = all, vulns.CountVulns(all)
		fixes := map[string]*vulns.Fix{}
		for _, v := range all {
			if v.Fixed == "" {
				continue
			}
			f := fixes[v.Package]
			if f == nil {
				f = &vulns.Fix{Ecosystem: v.Ecosystem, Package: v.Package, Version: v.Version, Fixed: v.Fixed}
				fixes[v.Package] = f
			}
			f.Counts = f.Counts.Plus(vulns.CountVulns([]vulns.Vuln{v}))
		}
		for _, f := range fixes {
			img.Fixes = append(img.Fixes, *f)
		}
		sort.Slice(img.Fixes, func(i, j int) bool { return img.Fixes[i].Package < img.Fixes[j].Package })
		for _, c := range img.Containers {
			img.UpdateAvailable = img.UpdateAvailable || s.updates[c.Name]
		}
		rep.Images = append(rep.Images, *img)
	}
	return rep
}

// ---- Moniteur simulé ----

type fakeMonitor struct {
	d         *daemon
	self      string
	mu        sync.Mutex
	stopped   map[string]bool
	ignored   map[string]bool
	transient map[string]int
	notes     []string
}

func (m *fakeMonitor) Containers() []monitor.Container {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []monitor.Container
	for _, c := range m.d.containers {
		state := "exited"
		if c.running {
			state = "running"
		}
		ref, _ := c.cfg["Image"].(string)
		out = append(out, monitor.Container{
			ID: c.id, Name: c.name, State: state, ImageRef: ref, Self: c.id == m.self,
			StoppedByUser: m.stopped[c.name] && !c.running, Ignored: m.ignored[c.name],
		})
	}
	return out
}

func (m *fakeMonitor) IsStoppedByUser(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopped[name]
}

func (m *fakeMonitor) NoteAction(name, action string) {
	m.mu.Lock()
	m.notes = append(m.notes, name+":"+action)
	m.mu.Unlock()
}

func (m *fakeMonitor) SetTransient(name string, on bool) {
	m.mu.Lock()
	if on {
		m.transient[name]++
	} else {
		m.transient[name]--
	}
	m.mu.Unlock()
}

// ---- Environnement complet ----

const cometRef = "ghcr.io/x/comet:latest"

type env struct {
	d   *daemon
	dk  *docker.Client
	st  *store.Store
	upd *updater.Manager
	vs  *fakeScanner
	mon *fakeMonitor
	set settings.Settings
	smu sync.Mutex
	s   *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, dk := newDaemon(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{d: d, dk: dk, st: st}
	e.set = settings.Settings{VulnScan: true, VulnFixLevel: "high", AutoUpdateTime: "04:00"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.upd = updater.New(dk, log, updater.Hooks{}, updater.Options{
		Enabled: true, SelfID: "self-id", PruneImages: func() bool { return true }, Store: st,
	})
	e.upd.SetTimings(30*time.Millisecond, 10*time.Millisecond, 5*time.Millisecond)
	e.vs = &fakeScanner{dk: dk, d: d, invs: map[string]vulns.Inventory{}, updates: map[string]bool{},
		rules: map[string][]rule{"openssl": {{"V-SSL", "critical", "3.0.15-1"}}, "glibc": {{"V-LIBC", "medium", ""}}}}
	e.mon = &fakeMonitor{d: d, self: "self-id", stopped: map[string]bool{}, ignored: map[string]bool{}, transient: map[string]int{}}
	e.s = New(context.Background(), dk, e.upd, e.vs, e.mon, e.settings, st, log, Options{SelfID: "self-id"})
	e.s.b.poll = 2 * time.Millisecond
	e.s.retry = 20 * time.Millisecond

	d.addImage("sha256:base", []string{cometRef}, []string{"ghcr.io/x/comet@sha256:aaa"}, baseConfig, debianFS(basePkgs))
	d.publish(cometRef, "sha256:base")
	d.upgrade = aptUpgrade(map[string]string{"libssl3": "3.0.15-1", "openssl": "3.0.15-1", "zlib1g": "1.3.1"})
	return e
}

func (e *env) settings() settings.Settings {
	e.smu.Lock()
	defer e.smu.Unlock()
	return e.set
}

func (e *env) configure(fn func(*settings.Settings)) {
	e.smu.Lock()
	fn(&e.set)
	e.smu.Unlock()
}

// wait attend la fin de l'opération en cours et la renvoie.
func (e *env) wait(t *testing.T) updater.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if jobs := e.upd.Jobs(); len(jobs) > 0 && jobs[0].Done {
			return jobs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("opération sans fin")
	return updater.Job{}
}
