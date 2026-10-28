package backup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDocker simule le démon Docker pour les sauvegardes et les restaurations : liste, inspection,
// archive (lecture et dépôt), arrêt, démarrage, création et suppression des conteneurs auxiliaires,
// logs et occupation disque. Les auxiliaires exécutent réellement RunRestoreHelper sur un dossier.
type fakeDocker struct {
	mu          sync.Mutex
	containers  []*fakeCtr
	files       map[string]map[string]string // chemin (montage) → fichiers de l'archive
	dirs        map[string]string            // volume ou chemin sur l'hôte → vrai dossier (ou fichier) de test
	failPath    map[string]bool              // archives en erreur
	failStart   bool                         // démarrage refusé
	failCreate  bool                         // création refusée
	exitOnStart map[string]bool              // conteneurs qui s'arrêtent aussitôt démarrés
	volumes     map[string]int64             // taille des volumes (/system/df)
	events      []string                     // « stop sonarr », « archive sonarr /config », « start sonarr »
	created     []map[string]any             // corps des créations de conteneurs
	tmp         string
	seq         int
}

type fakeCtr struct {
	ID, Name    string
	Running     bool
	StopTimeout int
	Mounts      []map[string]any
	Entrypoint  []string

	// Auxiliaire de restauration.
	Helper bool
	Spec   map[string]any
	Status string // created, exited
	Exit   int
	Staged []byte
	Logs   string
}

func newFakeDocker(t *testing.T) (*fakeDocker, *httptest.Server) {
	f := &fakeDocker{
		files: map[string]map[string]string{}, dirs: map[string]string{}, failPath: map[string]bool{},
		exitOnStart: map[string]bool{}, volumes: map[string]int64{}, tmp: t.TempDir(),
	}
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

// helpers renvoie les auxiliaires encore présents.
func (f *fakeDocker) helpers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.containers {
		if c.Helper {
			n++
		}
	}
	return n
}

func dockerJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// mountSource renvoie la clé de f.dirs du montage de c sur dest.
func mountSource(mounts []map[string]any, dest string) string {
	for _, m := range mounts {
		if m["Destination"] == dest {
			if m["Type"] == "volume" {
				return m["Name"].(string)
			}
			return m["Source"].(string)
		}
	}
	return ""
}

func (f *fakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/containers/json":
		var out []map[string]any
		for _, c := range f.containers {
			labels := map[string]string{}
			if c.Helper {
				labels["vigie.helper"] = "true"
			}
			out = append(out, map[string]any{"Id": c.ID, "Names": []string{"/" + c.Name}, "Labels": labels})
		}
		dockerJSON(w, 200, out)
	case r.URL.Path == "/system/df":
		var vols []map[string]any
		for name, size := range f.volumes {
			vols = append(vols, map[string]any{"Name": name, "UsageData": map[string]any{"Size": size, "RefCount": 1}})
		}
		dockerJSON(w, 200, map[string]any{"Volumes": vols})
	case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
		f.create(w, r)
	case len(parts) == 2 && parts[0] == "containers" && r.Method == http.MethodDelete:
		c := f.find(parts[1])
		if c == nil {
			dockerJSON(w, 404, map[string]any{"message": "no such container"})
			return
		}
		q := r.URL.Query()
		f.events = append(f.events, fmt.Sprintf("remove %s force=%s v=%s", f.label(c), q.Get("force"), q.Get("v")))
		for i, x := range f.containers {
			if x == c {
				f.containers = append(f.containers[:i], f.containers[i+1:]...)
				break
			}
		}
		w.WriteHeader(204)
	case len(parts) == 3 && parts[0] == "containers":
		c := f.find(parts[1])
		if c == nil {
			dockerJSON(w, 404, map[string]any{"message": "no such container"})
			return
		}
		f.container(w, r, c, parts[2])
	default:
		w.WriteHeader(404)
	}
}

// label désigne un conteneur dans le journal : son nom, ou « auxiliaire <élément> ».
func (f *fakeDocker) label(c *fakeCtr) string {
	if c.Helper {
		return "auxiliaire " + f.helperSource(c)
	}
	return c.Name
}

func (f *fakeDocker) helperSource(c *fakeCtr) string {
	hc, _ := c.Spec["HostConfig"].(map[string]any)
	mounts, _ := hc["Mounts"].([]any)
	for _, m := range mounts {
		if mm := m.(map[string]any); mm["Target"] == HelperTarget {
			return mm["Source"].(string)
		}
	}
	return ""
}

func (f *fakeDocker) create(w http.ResponseWriter, r *http.Request) {
	var spec map[string]any
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		dockerJSON(w, 400, map[string]any{"message": err.Error()})
		return
	}
	f.created = append(f.created, spec)
	if f.failCreate {
		dockerJSON(w, 500, map[string]any{"message": "création impossible"})
		return
	}
	f.seq++
	c := &fakeCtr{ID: fmt.Sprintf("helper%d", f.seq), Name: r.URL.Query().Get("name"), Helper: true, Spec: spec, Status: "created"}
	f.containers = append(f.containers, c)
	f.events = append(f.events, "create "+f.label(c))
	dockerJSON(w, 201, map[string]any{"Id": c.ID})
}

func (f *fakeDocker) container(w http.ResponseWriter, r *http.Request, c *fakeCtr, op string) {
	switch op {
	case "json":
		cfg := map[string]any{"Image": "img"}
		if c.StopTimeout > 0 {
			cfg["StopTimeout"] = c.StopTimeout
		}
		if len(c.Entrypoint) > 0 {
			cfg["Entrypoint"] = c.Entrypoint
		}
		state := map[string]any{"Running": c.Running}
		if c.Helper {
			state["Status"], state["ExitCode"] = c.Status, c.Exit
		}
		dockerJSON(w, 200, map[string]any{
			"Id": c.ID, "Name": "/" + c.Name, "Image": "sha256:vigie", "State": state,
			"Config": cfg, "HostConfig": map[string]any{}, "Mounts": c.Mounts,
		})
	case "stop":
		f.events = append(f.events, "stop "+c.Name+" t="+r.URL.Query().Get("t"))
		c.Running = false
		w.WriteHeader(204)
	case "start":
		if c.Helper {
			f.runHelper(c)
			w.WriteHeader(204)
			return
		}
		f.events = append(f.events, "start "+c.Name)
		if f.failStart {
			dockerJSON(w, 500, map[string]any{"message": "port déjà utilisé"})
			return
		}
		c.Running = !f.exitOnStart[c.Name]
		w.WriteHeader(204)
	case "logs":
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		io.WriteString(w, c.Logs)
	case "archive":
		p := r.URL.Query().Get("path")
		if r.Method == http.MethodPut {
			f.put(w, r, c, p)
			return
		}
		f.events = append(f.events, "archive "+c.Name+" "+p)
		if f.failPath[p] {
			dockerJSON(w, 500, map[string]any{"message": "archive impossible"})
			return
		}
		if dir, ok := f.dirs[mountSource(c.Mounts, p)]; ok {
			w.Header().Set("Content-Type", "application/x-tar")
			if err := tarPath(w, dir, path.Base(p)); err != nil {
				panic(err)
			}
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
}

// put reçoit le dépôt de la copie dans un auxiliaire créé (une archive tar d'un seul fichier).
func (f *fakeDocker) put(w http.ResponseWriter, r *http.Request, c *fakeCtr, dir string) {
	tr := tar.NewReader(r.Body)
	h, err := tr.Next()
	if err != nil || dir != "/" || c.Status != "created" || "/"+h.Name != helperArchive {
		dockerJSON(w, 400, map[string]any{"message": fmt.Sprintf("dépôt inattendu : %v %s", err, dir)})
		return
	}
	c.Staged, err = io.ReadAll(tr)
	if err == nil {
		_, err = tr.Next()
	}
	if err != io.EOF {
		dockerJSON(w, 400, map[string]any{"message": fmt.Sprintf("archive de dépôt : %v", err)})
		return
	}
	f.events = append(f.events, fmt.Sprintf("dépôt %s", f.label(c)))
	w.WriteHeader(200)
}

// runHelper exécute réellement l'auxiliaire sur le dossier de test de l'élément.
func (f *fakeDocker) runHelper(c *fakeCtr) {
	src := f.helperSource(c)
	archive := filepath.Join(f.tmp, c.ID+".tar.gz")
	if err := os.WriteFile(archive, c.Staged, 0o600); err != nil {
		panic(err)
	}
	var out bytes.Buffer
	c.Exit = RunRestoreHelper(&out, archive, f.dirs[src])
	c.Logs, c.Status = out.String(), "exited"
	f.events = append(f.events, fmt.Sprintf("exécution %s : code %d", f.label(c), c.Exit))
}

// tarPath écrit une archive tar de src (dossier ou fichier) sous le nom base, comme GET archive.
func tarPath(w io.Writer, src, base string) error {
	tw := tar.NewWriter(w)
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		link := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		h.Name = path.Join(base, filepath.ToSlash(rel))
		if fi.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
