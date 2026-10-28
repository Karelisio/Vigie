package vulns

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/karelisio/vigie/agent/internal/docker"
)

// node est un fichier (data), un lien symbolique (link) ou un dossier (dir) du conteneur simulé.
type node struct {
	data string
	link string
	dir  bool
}

// fakeFS imite GET /containers/{id}/archive : le dernier élément d'un lien n'est pas suivi.
type fakeFS map[string]node

func (fs fakeFS) tar(p string) ([]byte, bool) {
	n, ok := fs[p]
	if !ok {
		return nil, false
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	base := path.Base(p)
	switch {
	case n.link != "":
		_ = tw.WriteHeader(&tar.Header{Name: base, Typeflag: tar.TypeSymlink, Linkname: n.link, Mode: 0o777})
	case n.dir:
		_ = tw.WriteHeader(&tar.Header{Name: base + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		var children []string
		for k := range fs {
			if strings.HasPrefix(k, p+"/") {
				children = append(children, k)
			}
		}
		sort.Strings(children)
		for _, k := range children {
			c := fs[k]
			_ = tw.WriteHeader(&tar.Header{Name: base + "/" + strings.TrimPrefix(k, p+"/"), Typeflag: tar.TypeReg, Size: int64(len(c.data)), Mode: 0o644})
			_, _ = tw.Write([]byte(c.data))
		}
	default:
		_ = tw.WriteHeader(&tar.Header{Name: base, Typeflag: tar.TypeReg, Size: int64(len(n.data)), Mode: 0o644})
		_, _ = tw.Write([]byte(n.data))
	}
	_ = tw.Close()
	return buf.Bytes(), true
}

// fakeArchiver lit un fakeFS par conteneur, en mémoire.
type fakeArchiver struct {
	mu    sync.Mutex
	fs    map[string]fakeFS
	calls []string
}

func (f *fakeArchiver) Archive(_ context.Context, id, p string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.calls = append(f.calls, id+":"+p)
	f.mu.Unlock()
	b, ok := f.fs[id].tar(p)
	if !ok {
		return nil, &docker.APIError{Code: http.StatusNotFound, Message: "Could not find the file " + p}
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// fakeContainer décrit un conteneur du démon Docker simulé.
type fakeContainer struct {
	id, name, image, imageID, state string
	fs                              fakeFS
}

// fakeDocker est un démon Docker HTTP minimal : liste, inspection, archive et inspection d'image.
type fakeDocker struct {
	mu         sync.Mutex
	containers []fakeContainer
	workDirs   map[string]string            // ID d'image → WorkingDir
	labels     map[string]map[string]string // ID d'image → labels
	archives   int
	srv        *httptest.Server
}

func newFakeDocker(t *testing.T, list ...fakeContainer) *fakeDocker {
	f := &fakeDocker{containers: list, workDirs: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDocker) client(t *testing.T) *docker.Client {
	dk, err := docker.New("tcp://" + strings.TrimPrefix(f.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return dk
}

func (f *fakeDocker) set(list ...fakeContainer) {
	f.mu.Lock()
	f.containers = list
	f.mu.Unlock()
}

func (f *fakeDocker) archiveCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.archives
}

func (f *fakeDocker) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	send := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/containers/json":
		out := []map[string]any{}
		for _, c := range f.containers {
			out = append(out, map[string]any{"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image, "ImageID": c.imageID, "State": c.state})
		}
		send(200, out)
		return
	case len(parts) == 3 && parts[0] == "images":
		send(200, map[string]any{"Id": parts[1], "Config": map[string]any{"WorkingDir": f.workDirs[parts[1]], "Labels": f.labels[parts[1]]}})
		return
	case len(parts) == 3 && parts[0] == "containers":
		for _, c := range f.containers {
			if c.id != parts[1] {
				continue
			}
			switch parts[2] {
			case "json":
				send(200, map[string]any{"Id": c.id, "Name": "/" + c.name, "Image": c.imageID})
				return
			case "archive":
				f.archives++
				if b, ok := c.fs.tar(r.URL.Query().Get("path")); ok {
					w.Header().Set("Content-Type", "application/x-tar")
					_, _ = w.Write(b)
					return
				}
				send(404, map[string]string{"message": "Could not find the file"})
				return
			}
		}
		send(404, map[string]string{"message": "No such container: " + parts[1]})
		return
	}
	send(404, map[string]string{"message": "inconnu"})
}
