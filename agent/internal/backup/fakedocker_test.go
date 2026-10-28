package backup

import (
	"archive/tar"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDocker simule le démon Docker pour les sauvegardes : liste, inspection, archive, arrêt,
// démarrage et occupation disque.
type fakeDocker struct {
	mu         sync.Mutex
	containers []*fakeCtr
	files      map[string]map[string]string // chemin (montage) → fichiers de l'archive
	failPath   map[string]bool              // archives en erreur
	failStart  bool                         // démarrage refusé
	volumes    map[string]int64             // taille des volumes (/system/df)
	events     []string                     // « stop sonarr », « archive sonarr /config », « start sonarr »
}

type fakeCtr struct {
	ID, Name    string
	Running     bool
	StopTimeout int
	Mounts      []map[string]any
}

func newFakeDocker(t *testing.T) (*fakeDocker, *httptest.Server) {
	f := &fakeDocker{files: map[string]map[string]string{}, failPath: map[string]bool{}, volumes: map[string]int64{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func volumeMount(name, dest string) map[string]any {
	return map[string]any{"Type": "volume", "Name": name, "Source": "/var/lib/docker/volumes/" + name + "/_data", "Destination": dest, "RW": true}
}

func bindMount(src, dest string) map[string]any {
	return map[string]any{"Type": "bind", "Source": src, "Destination": dest, "RW": true}
}

func (f *fakeDocker) find(id string) *fakeCtr {
	for _, c := range f.containers {
		if c.ID == id || c.Name == id {
			return c
		}
	}
	return nil
}

func (f *fakeDocker) running(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.find(name).Running
}

func (f *fakeDocker) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.events, " | ")
}

func dockerJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/containers/json":
		var out []map[string]any
		for _, c := range f.containers {
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}})
		}
		dockerJSON(w, 200, out)
	case r.URL.Path == "/system/df":
		var vols []map[string]any
		for name, size := range f.volumes {
			vols = append(vols, map[string]any{"Name": name, "UsageData": map[string]any{"Size": size, "RefCount": 1}})
		}
		dockerJSON(w, 200, map[string]any{"Volumes": vols})
	case len(parts) == 3 && parts[0] == "containers":
		c := f.find(parts[1])
		if c == nil {
			dockerJSON(w, 404, map[string]any{"message": "no such container"})
			return
		}
		switch parts[2] {
		case "json":
			cfg := map[string]any{"Image": "img"}
			if c.StopTimeout > 0 {
				cfg["StopTimeout"] = c.StopTimeout
			}
			dockerJSON(w, 200, map[string]any{
				"Id": c.ID, "Name": "/" + c.Name, "State": map[string]any{"Running": c.Running},
				"Config": cfg, "HostConfig": map[string]any{}, "Mounts": c.Mounts,
			})
		case "stop":
			f.events = append(f.events, "stop "+c.Name+" t="+r.URL.Query().Get("t"))
			c.Running = false
			w.WriteHeader(204)
		case "start":
			f.events = append(f.events, "start "+c.Name)
			if f.failStart {
				dockerJSON(w, 500, map[string]any{"message": "port déjà utilisé"})
				return
			}
			c.Running = true
			w.WriteHeader(204)
		case "archive":
			p := r.URL.Query().Get("path")
			f.events = append(f.events, "archive "+c.Name+" "+p)
			if f.failPath[p] {
				dockerJSON(w, 500, map[string]any{"message": "archive impossible"})
				return
			}
			files, ok := f.files[p]
			if !ok {
				dockerJSON(w, 404, map[string]any{"message": "Could not find the file " + p})
				return
			}
			w.Header().Set("Content-Type", "application/x-tar")
			tw := tar.NewWriter(w)
			dir := strings.TrimPrefix(p, "/")
			for name, content := range files {
				_ = tw.WriteHeader(&tar.Header{Name: dir + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
				_, _ = tw.Write([]byte(content))
			}
			_ = tw.Close()
		default:
			w.WriteHeader(404)
		}
	default:
		w.WriteHeader(404)
	}
}
