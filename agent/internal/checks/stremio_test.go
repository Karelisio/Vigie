package checks

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/store"
)

// fakeStremio simule plusieurs addons : un par préfixe de chemin.
func fakeStremio(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/ok/manifest.json":                        `{"id":"org.test","resources":["catalog","stream"],"types":["movie","series"]}`,
		"/ok/stream/movie/tt0111161.json":          `{"streams":[{"url":"https://cdn.example/1.mkv"},{"infoHash":"abc"},{"name":"[erreur]","externalUrl":"stremio:///"}]}`,
		"/ok/stream/series/tt0903747:1:1.json":     `{"streams":[{"url":"https://cdn.example/e.mkv"}]}`,
		"/obj/manifest.json":                       `{"resources":[{"name":"stream","types":["movie"],"idPrefixes":["tt"]}]}`,
		"/obj/stream/movie/tt0111161.json":         `{"streams":[]}`,
		"/meta/manifest.json":                      `{"resources":["catalog","meta"]}`,
		"/broken/manifest.json":                    `{"resources":["stream"]}`,
		"/broken/stream/movie/tt0111161.json":      `{"streams":[`,
		"/cfg/abc%2Fdef/manifest.json":             `{"resources":["stream"]}`,
		"/cfg/abc%2Fdef/stream/movie/tt1.json":     `{"streams":[{"url":"https://cdn.example/x.mkv"}]}`,
		"/html/manifest.json":                      `<!DOCTYPE html><html><head><title>Connexion</title></head></html>`,
		"/series-only/manifest.json":               `{"resources":["stream"]}`,
		"/series-only/stream/movie/tt0111161.json": `{"err":"type non pris en charge"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(body, "<") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRunner(st *store.Store) *Runner {
	return NewRunner(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", 2)
}

func TestStremioProbe(t *testing.T) {
	srv := fakeStremio(t)
	r := testRunner(nil)
	cases := []struct {
		path, id string
		links    int
		err      string
	}{
		{"/ok/manifest.json", "tt0111161", 2, ""},
		{"/ok/manifest.json", "tt0903747:1:1", 1, ""},
		{"/cfg/abc%2Fdef/manifest.json", "tt1", 1, ""},
		{"/obj/manifest.json", "tt0111161", 0, "aucun lien pour tt0111161"},
		{"/series-only/manifest.json", "tt0111161", 0, "aucun lien pour tt0111161"},
		{"/meta/manifest.json", "tt0111161", 0, "manifest sans ressource « stream »"},
		{"/broken/manifest.json", "tt0111161", 0, "réponse JSON invalide (liens)"},
		{"/html/manifest.json", "tt0111161", 0, "réponse JSON invalide (manifest) : page HTML « Connexion »"},
		{"/absent/manifest.json", "tt0111161", 0, "manifest : code 404"},
	}
	for _, c := range cases {
		check := store.Check{Name: "addon", URL: srv.URL + c.path, Kind: KindStremio, StremioID: c.id}
		if err := Validate(&check); err != nil {
			t.Fatalf("%s : %v", c.path, err)
		}
		res := r.probe(context.Background(), check)
		if res.Links != c.links || res.Error != c.err || res.OK != (c.err == "") {
			t.Errorf("%s %s : liens %d, erreur %q, ok %v ; attendu %d, %q", c.path, c.id, res.Links, res.Error, res.OK, c.links, c.err)
		}
	}
}

func TestStremioIgnoresKeywordAndCode(t *testing.T) {
	srv := fakeStremio(t)
	check := store.Check{Name: "addon", URL: srv.URL + "/ok/manifest.json", Kind: KindStremio, ExpectedStatus: 418, Keyword: "introuvable"}
	if err := Validate(&check); err != nil {
		t.Fatal(err)
	}
	if res := testRunner(nil).probe(context.Background(), check); !res.OK || res.Status != 200 {
		t.Fatalf("résultat %+v", res)
	}
}

func TestValidateStremio(t *testing.T) {
	c := store.Check{Name: "Comet", URL: "https://comet.example.com/cfg/manifest.json", Kind: KindStremio, IntervalSec: 60, TimeoutMS: 10000}
	if err := Validate(&c); err != nil {
		t.Fatal(err)
	}
	if c.StremioID != DefaultStremioID || c.IntervalSec != 300 || c.TimeoutMS != 30000 {
		t.Fatalf("normalisation inattendue : %+v", c)
	}
	if streamURL(c.URL+"?x=1", "tt0903747:1:1") != "https://comet.example.com/cfg/stream/series/tt0903747:1:1.json" {
		t.Error("URL de recherche de liens")
	}
	bad := []store.Check{
		{Name: "x", URL: "https://comet.example.com/", Kind: KindStremio},
		{Name: "x", URL: "https://comet.example.com/manifest.json", Kind: KindStremio, StremioID: "../../admin"},
		{Name: "x", URL: "https://comet.example.com/manifest.json", Kind: KindStremio, StremioID: "tt1:2"},
		{Name: "x", URL: "https://comet.example.com/manifest.json", Kind: "ftp"},
	}
	for _, b := range bad {
		if Validate(&b) == nil {
			t.Errorf("accepté : %+v", b)
		}
	}
}

func TestStremioResultStored(t *testing.T) {
	srv := fakeStremio(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	c := store.Check{Name: "AIOStreams", URL: srv.URL + "/ok/manifest.json", Kind: KindStremio, Enabled: true}
	if err := Validate(&c); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	r := testRunner(st)
	if err := r.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := r.RunNow(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != KindStremio || s.StremioID != DefaultStremioID || s.Last == nil || s.Last.Links != 2 || len(s.Recent) != 1 {
		t.Fatalf("état %+v", s)
	}
	recent, err := st.RecentResults(ctx, c.ID, 5)
	if err != nil || len(recent) != 1 || recent[0].Links != 2 {
		t.Fatalf("résultats enregistrés %+v (%v)", recent, err)
	}
}
