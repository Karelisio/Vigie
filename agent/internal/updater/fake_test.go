package updater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDocker simule le démon Docker juste assez pour exercer une mise à jour complète.
type fakeDocker struct {
	t   *testing.T
	mu  sync.Mutex
	api string
	seq int

	containers map[string]*fakeCtr
	tags       map[string]string         // référence → ID d'image locale
	remote     map[string]string         // référence → ID publié sur le registre
	imageCfg   map[string]map[string]any // ID d'image → Config
	crashing   map[string]bool           // images qui plantent au démarrage
	failCreate map[string]bool           // noms dont la création échoue
	removedImg []string
	connected  []string
	created    []map[string]any

	digests  map[string][]string // ID d'image → RepoDigests
	registry map[string]string   // « dépôt@sha256:… » publié → ID d'image
	deleted  map[string]bool     // images supprimées du disque
	pulled   []string            // références téléchargées
}

type fakeCtr struct {
	ID, Name, Image string
	Config          map[string]any
	HostConfig      map[string]any
	Networks        map[string]map[string]any
	Running         bool
	ExitCode        int
}

func newFake(t *testing.T) (*fakeDocker, *httptest.Server) {
	f := &fakeDocker{
		t: t, api: "1.47",
		containers: map[string]*fakeCtr{}, tags: map[string]string{}, remote: map[string]string{},
		imageCfg: map[string]map[string]any{}, crashing: map[string]bool{}, failCreate: map[string]bool{},
		digests: map[string][]string{}, registry: map[string]string{}, deleted: map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeDocker) addImage(ref, id string, cfg map[string]any) {
	f.tags[ref] = id
	f.imageCfg[id] = cfg
}

func (f *fakeDocker) addContainer(c *fakeCtr) *fakeCtr {
	if c.Networks == nil {
		c.Networks = map[string]map[string]any{}
	}
	f.containers[c.ID] = c
	return c
}

func (f *fakeDocker) find(ref string) *fakeCtr {
	for _, c := range f.containers {
		if c.ID == ref || c.Name == ref {
			return c
		}
	}
	return nil
}

func (f *fakeDocker) byName(name string) *fakeCtr {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.find(name)
}

// resolve renvoie l'ID d'une image locale désignée par référence, ID ou « dépôt@sha256:… ».
func (f *fakeDocker) resolve(ref string) string {
	id := f.tags[ref]
	if id == "" {
		if _, ok := f.imageCfg[ref]; ok {
			id = ref
		}
	}
	if id == "" && strings.Contains(ref, "@") {
		for img, list := range f.digests {
			for _, d := range list {
				if d == ref {
					id = img
				}
			}
		}
	}
	if f.deleted[id] {
		return ""
	}
	return id
}

// tagOf renvoie l'image sur laquelle pointe une référence locale.
func (f *fakeDocker) tagOf(ref string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tags[ref]
}

func (f *fakeDocker) removed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted[id]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	q := r.URL.Query()
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/version":
		writeJSON(w, 200, map[string]any{"ApiVersion": f.api, "Version": "fake"})

	case p == "/containers/json":
		var out []map[string]any
		for _, c := range f.containers {
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}})
		}
		writeJSON(w, 200, out)

	case p == "/containers/create" && r.Method == http.MethodPost:
		name := q.Get("name")
		if f.find(name) != nil {
			writeJSON(w, 409, map[string]any{"message": "name already in use"})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		if hcfg, _ := body["HostConfig"].(map[string]any); hcfg != nil {
			if mode, _ := hcfg["NetworkMode"].(string); strings.HasPrefix(mode, "container:") && body["Hostname"] != nil {
				writeJSON(w, 400, map[string]any{"message": "conflicting options: hostname and the network mode"})
				return
			}
		}
		if f.failCreate[name] {
			writeJSON(w, 500, map[string]any{"message": "simulated failure"})
			return
		}
		ref, _ := body["Image"].(string)
		img := f.resolve(ref)
		if img == "" {
			writeJSON(w, 404, map[string]any{"message": "no such image " + ref})
			return
		}
		f.seq++
		id := fmt.Sprintf("%064d", f.seq+1000)
		hc, _ := body["HostConfig"].(map[string]any)
		delete(body, "HostConfig")
		nets := map[string]map[string]any{}
		if nc, ok := body["NetworkingConfig"].(map[string]any); ok {
			if eps, ok := nc["EndpointsConfig"].(map[string]any); ok {
				for n, ep := range eps {
					nets[n], _ = ep.(map[string]any)
				}
			}
		}
		delete(body, "NetworkingConfig")
		f.containers[id] = &fakeCtr{ID: id, Name: name, Image: img, Config: body, HostConfig: hc, Networks: nets}
		writeJSON(w, 201, map[string]any{"Id": id})

	case strings.HasPrefix(p, "/images/create"):
		ref := q.Get("fromImage") + ":" + q.Get("tag")
		byDigest := strings.HasPrefix(q.Get("tag"), "sha256:")
		if byDigest {
			ref = q.Get("fromImage") + "@" + q.Get("tag")
		}
		id, ok := f.remote[ref]
		if byDigest {
			id, ok = f.registry[ref]
		}
		f.pulled = append(f.pulled, ref)
		if !ok {
			writeJSON(w, 200, map[string]any{"error": "manifest unknown"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"Pulling fs layer","id":"l1"}`)
		fmt.Fprintln(w, `{"status":"Downloading","id":"l1","progressDetail":{"current":50,"total":100}}`)
		fmt.Fprintln(w, `{"status":"Pull complete","id":"l1"}`)
		delete(f.deleted, id)
		if !byDigest {
			f.tags[ref] = id
		}

	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/tag") && r.Method == http.MethodPost:
		id := f.resolve(strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/tag"))
		if id == "" {
			writeJSON(w, 404, map[string]any{"message": "no such image"})
			return
		}
		f.tags[q.Get("repo")+":"+q.Get("tag")] = id
		w.WriteHeader(201)

	case strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json"):
		id := f.resolve(strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json"))
		if id == "" {
			writeJSON(w, 404, map[string]any{"message": "no such image"})
			return
		}
		writeJSON(w, 200, map[string]any{"Id": id, "Config": f.imageCfg[id], "RepoDigests": f.digests[id], "Created": "2026-09-01T10:00:00.123456789Z"})

	case strings.HasPrefix(p, "/images/") && r.Method == http.MethodDelete:
		id := f.resolve(strings.TrimPrefix(p, "/images/"))
		for _, c := range f.containers {
			if c.Image == id {
				writeJSON(w, 409, map[string]any{"message": "image is being used by running container"})
				return
			}
		}
		f.removedImg = append(f.removedImg, strings.TrimPrefix(p, "/images/"))
		f.deleted[id] = true
		for ref, tid := range f.tags {
			if tid == id {
				delete(f.tags, ref)
			}
		}
		writeJSON(w, 200, []any{})

	case len(parts) == 3 && parts[0] == "networks" && parts[2] == "connect":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.connected = append(f.connected, parts[1])
		if c := f.find(body["Container"].(string)); c != nil {
			ep, _ := body["EndpointConfig"].(map[string]any)
			c.Networks[parts[1]] = ep
		}
		w.WriteHeader(200)

	case len(parts) >= 2 && parts[0] == "containers":
		c := f.find(parts[1])
		if c == nil {
			writeJSON(w, 404, map[string]any{"message": "no such container"})
			return
		}
		action := ""
		if len(parts) == 3 {
			action = parts[2]
		}
		switch {
		case r.Method == http.MethodDelete:
			if c.Running && q.Get("force") != "true" {
				writeJSON(w, 409, map[string]any{"message": "container is running"})
				return
			}
			delete(f.containers, c.ID)
			w.WriteHeader(204)
		case action == "json":
			cfg := map[string]any{}
			for k, v := range c.Config {
				cfg[k] = v
			}
			nets := map[string]any{}
			for n, ep := range c.Networks {
				nets[n] = ep
			}
			writeJSON(w, 200, map[string]any{
				"Id": c.ID, "Name": "/" + c.Name, "Image": c.Image,
				"State":           map[string]any{"Running": c.Running, "ExitCode": c.ExitCode},
				"Config":          cfg,
				"HostConfig":      c.HostConfig,
				"NetworkSettings": map[string]any{"Networks": nets},
				"Mounts":          []any{},
			})
		case action == "stop":
			c.Running = false
			w.WriteHeader(204)
		case action == "start":
			if mode, _ := c.HostConfig["NetworkMode"].(string); strings.HasPrefix(mode, "container:") {
				if p := f.find(strings.TrimPrefix(mode, "container:")); p == nil || !p.Running {
					writeJSON(w, 409, map[string]any{"message": "network provider not running"})
					return
				}
			}
			if f.crashing[c.Image] {
				c.Running, c.ExitCode = false, 1
			} else {
				c.Running = true
			}
			w.WriteHeader(204)
		case action == "rename":
			if f.find(q.Get("name")) != nil {
				writeJSON(w, 409, map[string]any{"message": "name in use"})
				return
			}
			c.Name = q.Get("name")
			w.WriteHeader(204)
		case action == "logs":
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			fmt.Fprintln(w, "2026-10-28T10:00:00.000000000Z boom: config invalide")
		default:
			w.WriteHeader(404)
		}
	default:
		f.t.Logf("route non simulée : %s %s", r.Method, p)
		w.WriteHeader(404)
	}
}
