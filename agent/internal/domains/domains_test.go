package domains

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func TestRegistrable(t *testing.T) {
	cases := map[string]string{
		"aiostreams.15122000.xyz":  "15122000.xyz",
		"15122000.xyz":             "15122000.xyz",
		"Comet.15122000.XYZ.":      "15122000.xyz",
		"a.b.example.co.uk":        "example.co.uk",
		"www.example.com":          "example.com",
		"127.0.0.1":                "",
		"::1":                      "",
		"localhost":                "",
		"vigie.localhost":          "",
		"nas.local":                "",
		"router.lan":               "",
		"app.internal":             "",
		"co.uk":                    "",
		"xyz":                      "",
		"moi.duckdns.org":          "", // suffixe privé : l'expiration ne dépend pas de l'utilisateur
		"user.github.io":           "",
		"exemple.inconnu-tld-test": "",
		"münchen.de":               "",
		"":                         "",
	}
	for host, want := range cases {
		got, ok := Registrable(host)
		if got != want || ok != (want != "") {
			t.Errorf("Registrable(%q) = %q, %v ; attendu %q", host, got, ok, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	want := time.Date(2027, 3, 14, 9, 26, 53, 0, time.UTC)
	for _, v := range []string{
		"2027-03-14T09:26:53Z",
		"2027-03-14T09:26:53.000Z",
		"2027-03-14T09:26:53.123456Z",
		"2027-03-14t09:26:53z",
		"2027-03-14T10:26:53+01:00",
		"2027-03-14T10:26:53+0100",
		"2027-03-14T09:26:53",
		"2027-03-14 09:26:53",
		"2027-03-14 11:26:53+02:00",
	} {
		got, ok := parseDate(v)
		if !ok || !got.Truncate(time.Second).Equal(want) {
			t.Errorf("parseDate(%q) = %v, %v", v, got, ok)
		}
	}
	if got, ok := parseDate("2027-03-14"); !ok || !got.Equal(time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date seule : %v %v", got, ok)
	}
	for _, v := range []string{"", "demain", "0000-00-00", "14/03/2027"} {
		if _, ok := parseDate(v); ok {
			t.Errorf("parseDate(%q) accepté", v)
		}
	}
}

// fakeRDAP simule l'amorçage IANA, le registre de .xyz et .uk, et rdap.org pour les autres TLD.
type fakeRDAP struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	// expires : date d'expiration renvoyée pour 15122000.xyz (modifiable pendant le test).
	expires string
}

func (f *fakeRDAP) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

func newFakeRDAP(t *testing.T) *fakeRDAP {
	f := &fakeRDAP{expires: "2027-03-14T09:26:53Z"}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		expires := f.expires
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/rdap+json")
		switch r.URL.Path {
		case "/dns.json":
			base := "https://" + strings.TrimPrefix(f.URL, "http://")
			// Le faux registre est en HTTP : seule l'entrée HTTPS est retenue, réécrite ensuite par le test.
			_, _ = io.WriteString(w, `{"version":"1.0","services":[
				[["xyz","club"],["http://ignored.example/","`+base+`/xyz/"]],
				[["uk"],["`+base+`/uk"]],
				[["org"],["http://seulement-http.example/"]]]}`)
		case "/xyz/domain/15122000.xyz":
			_, _ = io.WriteString(w, `{"objectClassName":"domain","ldhName":"15122000.XYZ",
				"status":["client transfer prohibited","Active"],
				"events":[{"eventAction":"registration","eventDate":"2023-03-14T09:26:53.0Z"},
					{"eventAction":"expiration","eventDate":"`+expires+`"},
					{"eventAction":"last changed","eventDate":"2026-02-01 08:00:00"},
					{"eventAction":"last update of RDAP database","eventDate":"2026-10-28T10:00:00Z"}],
				"entities":[{"objectClassName":"entity","roles":["registrant"],"vcardArray":["vcard",[["fn",{},"text","Moi"]]]},
					{"objectClassName":"entity","roles":["registrar"],"handle":"1068",
					 "vcardArray":["vcard",[["version",{},"text","4.0"],["fn",{},"text","NameCheap, Inc."]]]}]}`)
		case "/uk/domain/example.co.uk":
			// Registre qui ne publie pas d'expiration ; bureau d'enregistrement imbriqué, sans « fn ».
			_, _ = io.WriteString(w, `{"objectClassName":"domain","status":["active"],
				"events":[{"eventAction":"registration","eventDate":"2001-05-06"}],
				"entities":[{"roles":["registrant"],"entities":[{"roles":["registrar"],
					"vcardArray":["vcard",[["org",{},"text","Registrar Ltd"]]]}]}]}`)
		case "/fallback/example.net":
			_, _ = io.WriteString(w, `{"objectClassName":"domain","events":[{"eventAction":"expiration","eventDate":"2028-01-02T03:04:05+0000"}]}`)
		case "/fallback/absent.net":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errorCode":404,"title":"Not Found"}`)
		case "/fallback/limite.net":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/fallback/casse.net":
			_, _ = io.WriteString(w, `<html>pas du JSON</html>`)
		case "/fallback/enorme.net":
			_, _ = io.WriteString(w, `{"objectClassName":"domain","port43":"`+strings.Repeat("x", maxBody)+`"}`)
		case "/fallback/autre.net":
			_, _ = io.WriteString(w, `{"objectClassName":"entity"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

type env struct {
	m   *Manager
	st  *store.Store
	f   *fakeRDAP
	man []string
}

func newEnv(t *testing.T, manual ...string) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, f: newFakeRDAP(t), man: manual}
	e.m = e.manager(t)
	return e
}

// manager crée un gestionnaire sur la base de l'environnement (comme après un redémarrage de l'agent).
func (e *env) manager(t *testing.T) *Manager {
	m := New(context.Background(), e.st, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		PublicURL:    "https://vigie.15122000.xyz",
		Manual:       func() []string { return e.man },
		BootstrapURL: e.f.URL + "/dns.json",
		FallbackURL:  e.f.URL + "/fallback/",
	})
	// Le faux registre parle HTTP : les adresses HTTPS de l'amorçage sont ramenées vers lui.
	m.http.Transport = rewriteHTTPS{}
	return m
}

type rewriteHTTPS struct{}

func (rewriteHTTPS) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "https" {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
	}
	return http.DefaultTransport.RoundTrip(r)
}

func byDomain(list []Status) map[string]Status {
	out := map[string]Status{}
	for _, s := range list {
		out[s.Domain] = s
	}
	return out
}

func TestTrackedDomains(t *testing.T) {
	e := newEnv(t, "example.co.uk", "WWW.15122000.xyz", "moi.duckdns.org")
	ctx := context.Background()
	for _, c := range []store.Check{
		{Name: "AIOStreams", URL: "https://aiostreams.15122000.xyz/manifest.json"},
		{Name: "Comet", URL: "https://comet.15122000.xyz/configure"},
		{Name: "Local", URL: "http://127.0.0.1:8080/health"},
		{Name: "LAN", URL: "http://nas.local:5000"},
		{Name: "Autre", URL: "https://api.example.net/status"},
	} {
		c.ExpectedStatus, c.IntervalSec, c.TimeoutMS, c.Enabled, c.Kind = 200, 60, 1000, true, "http"
		if err := e.st.CreateCheck(ctx, &c); err != nil {
			t.Fatal(err)
		}
	}
	list := e.m.State(ctx)
	got := byDomain(list)
	if len(list) != 4 {
		t.Fatalf("domaines : %+v", list)
	}
	x := got["15122000.xyz"]
	if x.Source != SourceManual || strings.Join(x.Services, ",") != "AIOStreams,Comet,Vigie" || x.CheckedAt != 0 || x.DaysLeft != nil {
		t.Errorf("15122000.xyz : %+v", x)
	}
	if n := got["example.net"]; n.Source != SourceAuto || strings.Join(n.Services, ",") != "Autre" {
		t.Errorf("example.net : %+v", n)
	}
	if u := got["example.co.uk"]; u.Source != SourceManual || len(u.Services) != 0 {
		t.Errorf("example.co.uk : %+v", u)
	}
	if d := got["moi.duckdns.org"]; d.Source != SourceManual || !strings.Contains(d.Error, "suffixe privé") {
		t.Errorf("domaine invérifiable : %+v", d)
	}
	// Ordre alphabétique et JSON attendu par l'appli.
	if list[0].Domain != "15122000.xyz" || list[3].Domain != "moi.duckdns.org" {
		t.Errorf("ordre : %v", list)
	}
	raw, _ := json.Marshal(x)
	if !strings.Contains(string(raw), `"days_left":null`) || !strings.Contains(string(raw), `"status":[]`) {
		t.Errorf("JSON : %s", raw)
	}
}

func TestLookupFormatsAndErrors(t *testing.T) {
	e := newEnv(t, "15122000.xyz", "example.co.uk", "example.net", "absent.net", "limite.net", "casse.net", "enorme.net", "autre.net", "example.org")
	var notified []Status
	e.m.OnCheck = func(l []Status) { notified = l }
	got := byDomain(e.m.Check(context.Background()))
	if len(notified) != len(got) {
		t.Fatalf("OnCheck non appelé : %d", len(notified))
	}

	x := got["15122000.xyz"]
	if x.Expires != time.Date(2027, 3, 14, 9, 26, 53, 0, time.UTC).UnixMilli() || x.Registrar != "NameCheap, Inc." ||
		x.Registered != time.Date(2023, 3, 14, 9, 26, 53, 0, time.UTC).UnixMilli() ||
		x.LastChanged != time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC).UnixMilli() ||
		strings.Join(x.Status, "|") != "client transfer prohibited|active" || x.Error != "" || x.CheckedAt == 0 || x.DaysLeft == nil {
		t.Errorf("15122000.xyz : %+v", x)
	}
	if want := DaysLeft(x.Expires, time.Now()); *x.DaysLeft != want {
		t.Errorf("jours restants : %d, attendu %d", *x.DaysLeft, want)
	}
	uk := got["example.co.uk"]
	if uk.Expires != 0 || uk.DaysLeft != nil || uk.Error != "" || uk.Note == "" || uk.Registrar != "Registrar Ltd" || uk.Registered == 0 {
		t.Errorf("expiration non publiée : %+v", uk)
	}
	if n := got["example.net"]; n.Expires != time.Date(2028, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli() || n.Error != "" {
		t.Errorf("TLD absent de l'amorçage (repli) : %+v", n)
	}
	for d, want := range map[string]string{
		"absent.net":  "domaine introuvable chez le registre",
		"limite.net":  "le registre limite les requêtes",
		"casse.net":   "réponse du registre illisible",
		"enorme.net":  "réponse du registre trop volumineuse",
		"autre.net":   "réponse RDAP inattendue",
		"example.org": "le registre répond par une erreur (code 500)", // .org : amorçage en HTTP seulement, ignoré → repli
	} {
		if s := got[d]; !strings.Contains(s.Error, want) || s.CheckedAt == 0 || s.Note != "" {
			t.Errorf("%s : %+v (attendu %q)", d, s, want)
		}
	}
	if e.f.count("/fallback/example.org") != 1 || e.f.count("/xyz/") != 1 || e.f.count("/uk/") != 1 {
		t.Errorf("requêtes : %v", e.f.paths)
	}
}

func TestCacheAndSchedule(t *testing.T) {
	e := newEnv(t, "example.co.uk") // 15122000.xyz vient de l'adresse publique de l'agent
	ctx := context.Background()
	first := byDomain(e.m.Check(ctx))
	if first["15122000.xyz"].Expires == 0 || e.f.count("/dns.json") != 1 || e.f.count("/xyz/") != 1 {
		t.Fatalf("première vérification : %+v %v", first, e.f.paths)
	}
	// Passage horaire : résultat de moins d'un jour, rien n'est interrogé ; à la demande, pas plus d'une fois par minute.
	e.m.refresh(ctx, false)
	e.m.Check(ctx)
	if e.f.count("/xyz/") != 1 {
		t.Fatalf("registre interrogé trop souvent : %v", e.f.paths)
	}

	// Redémarrage : résultats et amorçage relus en base.
	again := e.manager(t)
	if s := byDomain(again.State(ctx))["15122000.xyz"]; s.Expires != first["15122000.xyz"].Expires || s.Registrar == "" {
		t.Fatalf("cache perdu : %+v", s)
	}
	// Résultat vieux de plus d'un jour : nouvelle vérification, sans recharger l'amorçage (7 jours).
	again.mu.Lock()
	r := again.results["15122000.xyz"]
	r.CheckedAt = time.Now().Add(-25 * time.Hour).UnixMilli()
	again.results["15122000.xyz"] = r
	again.mu.Unlock()
	e.f.mu.Lock()
	e.f.expires = "2028-03-14T09:26:53Z"
	e.f.mu.Unlock()
	s := byDomain(again.refresh(ctx, false))["15122000.xyz"]
	if e.f.count("/xyz/") != 2 || e.f.count("/uk/") != 1 || e.f.count("/dns.json") != 1 ||
		s.Expires != time.Date(2028, 3, 14, 9, 26, 53, 0, time.UTC).UnixMilli() {
		t.Fatalf("nouvelle vérification : %+v %v", s, e.f.paths)
	}

	// Domaine retiré des réglages : oublié.
	e.man = nil
	if list := again.Check(ctx); len(list) != 1 || list[0].Domain != "15122000.xyz" || list[0].Source != SourceAuto {
		t.Fatalf("après retrait : %+v", list)
	}
	again.mu.Lock()
	_, kept := again.results["example.co.uk"]
	again.mu.Unlock()
	var saved map[string]result
	_ = json.Unmarshal([]byte(e.st.Get(ctx, stateKey)), &saved)
	if kept || len(saved) != 1 {
		t.Fatalf("résultat du domaine retiré conservé : %v", saved)
	}
}

func TestErrorKeepsLastAnswer(t *testing.T) {
	e := newEnv(t, "15122000.xyz")
	ctx := context.Background()
	ok := e.m.Check(ctx)[0]
	e.m.mu.Lock()
	r := e.m.results["15122000.xyz"]
	r.CheckedAt = time.Now().Add(-25 * time.Hour).UnixMilli()
	e.m.results["15122000.xyz"] = r
	e.m.mu.Unlock()
	e.f.Close() // registre injoignable
	s := e.m.refresh(ctx, false)[0]
	if s.Expires != ok.Expires || s.Registrar != ok.Registrar || !strings.Contains(s.Error, "registre injoignable") {
		t.Fatalf("après erreur : %+v", s)
	}
	// Nouvel essai 6 h après une erreur, pas avant.
	before := s.CheckedAt
	if s := e.m.refresh(ctx, false)[0]; s.CheckedAt != before {
		t.Fatalf("nouvel essai trop tôt : %+v", s)
	}
}
