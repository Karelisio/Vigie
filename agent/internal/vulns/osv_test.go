package vulns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() { osvRetryDelay = 10 * time.Millisecond }

// fakeOSV imite api.osv.dev : querybatch (avec pagination) et détail des failles.
type fakeOSV struct {
	mu       sync.Mutex
	records  map[string]map[string]any // fiche complète par ID
	affects  map[string][]string       // « écosystème|nom|version » → IDs
	pageSize int                       // > 0 : résultats paginés par requête
	fail     int                       // nombre de réponses 500 à renvoyer
	batches  []int                     // nombre de requêtes de chaque appel à querybatch
	details  []string
	srv      *httptest.Server
}

func newFakeOSV(t *testing.T) *fakeOSV {
	f := &fakeOSV{records: map[string]map[string]any{}, affects: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// add enregistre une fiche et les paquets qu'elle touche (« écosystème|nom|version »).
func (f *fakeOSV) add(rec map[string]any, pkgs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := rec["id"].(string)
	if _, ok := rec["modified"]; !ok {
		rec["modified"] = "2026-10-01T00:00:00Z"
	}
	f.records[id] = rec
	for _, p := range pkgs {
		f.affects[p] = append(f.affects[p], id)
	}
}

func (f *fakeOSV) counts() (batches int, queries int, details int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.batches {
		queries += n
	}
	return len(f.batches), queries, len(f.details)
}

func (f *fakeOSV) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("User-Agent") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "Vigie-agent/") {
		http.Error(w, "agent inconnu", http.StatusBadRequest)
		return
	}
	if f.fail > 0 {
		f.fail--
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":13,"message":"internal error"}`))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/querybatch":
		var body struct {
			Queries []osvQuery `json:"queries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Queries) > batchSize {
			http.Error(w, "requête invalide", http.StatusBadRequest)
			return
		}
		f.batches = append(f.batches, len(body.Queries))
		type res struct {
			Vulns []osvRef `json:"vulns,omitempty"`
			Next  string   `json:"next_page_token,omitempty"`
		}
		out := struct {
			Results []res `json:"results"`
		}{Results: make([]res, len(body.Queries))}
		for i, q := range body.Queries {
			ids := f.affects[q.Package.Ecosystem+"|"+q.Package.Name+"|"+q.Version]
			start, _ := strconv.Atoi(q.PageToken)
			end := len(ids)
			if f.pageSize > 0 && end-start > f.pageSize {
				end = start + f.pageSize
				out.Results[i].Next = strconv.Itoa(end)
			}
			for _, id := range ids[start:end] {
				out.Results[i].Vulns = append(out.Results[i].Vulns, osvRef{ID: id, Modified: f.records[id]["modified"].(string)})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/vulns/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/vulns/")
		f.details = append(f.details, id)
		rec, ok := f.records[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"Bug not found."}`))
			return
		}
		_ = json.NewEncoder(w).Encode(rec)
	default:
		http.NotFound(w, r)
	}
}

func q(eco, name, version string) osvQuery {
	var o osvQuery
	o.Package.Ecosystem, o.Package.Name, o.Version = eco, name, version
	return o
}

func TestQueryBatchSplitsAndPaginates(t *testing.T) {
	f := newFakeOSV(t)
	f.pageSize = 2
	for i := range 5 {
		f.add(map[string]any{"id": fmt.Sprintf("GHSA-%d", i)}, "npm|lodash|4.17.20")
	}
	f.add(map[string]any{"id": "GHSA-x"}, "npm|p1400|1.0.0")
	var qs []osvQuery
	for i := range 1500 {
		qs = append(qs, q("npm", fmt.Sprintf("p%d", i), "1.0.0"))
	}
	qs[3] = q("npm", "lodash", "4.17.20")
	var progress []int
	c := newOSVClient(f.srv.URL, "test")
	out, err := c.queryBatch(context.Background(), qs, func(done int) { progress = append(progress, done) })
	if err != nil {
		t.Fatal(err)
	}
	if len(out[3]) != 5 || out[3][4].ID != "GHSA-4" || len(out[1400]) != 1 || len(out[0]) != 0 {
		t.Fatalf("résultats : %v / %v", out[3], out[1400])
	}
	// Lot 1 : 1000 requêtes, puis 2 pages pour la seule requête paginée ; lot 2 : 500.
	if fmt.Sprint(f.batches) != "[1000 1 1 500]" || fmt.Sprint(progress) != "[1000 1500]" {
		t.Fatalf("appels %v, progression %v", f.batches, progress)
	}
}

func TestOSVErrorsAreExplained(t *testing.T) {
	f := newFakeOSV(t)
	c := newOSVClient(f.srv.URL, "test")
	f.fail = 1 // erreur passagère : second essai réussi
	if _, err := c.queryBatch(context.Background(), []osvQuery{q("npm", "a", "1.0.0")}, nil); err != nil {
		t.Fatalf("nouvel essai attendu : %v", err)
	}
	f.fail = 2
	_, err := c.queryBatch(context.Background(), []osvQuery{q("npm", "a", "1.0.0")}, nil)
	if err == nil || err.Error() != "OSV a répondu 500 : internal error" {
		t.Fatalf("erreur : %v", err)
	}
	if _, err := c.vuln(context.Background(), "GHSA-absente"); err != errOSVNotFound {
		t.Fatalf("404 : %v", err)
	}
	down := newOSVClient("http://127.0.0.1:1", "test")
	if _, err := down.vuln(context.Background(), "X"); err == nil || !strings.HasPrefix(err.Error(), "OSV injoignable") {
		t.Fatalf("injoignable : %v", err)
	}
}

// Fiche GitHub (npm) : deux plages, gravité CVSS v3 et database_specific.
const ghsaLodash = `{
  "id": "GHSA-35jh-r3h4-6jhm", "summary": "Command Injection in lodash", "modified": "2024-01-01T00:00:00Z",
  "aliases": ["CVE-2021-23337"],
  "severity": [{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:H"}],
  "affected": [
    {"package": {"ecosystem": "npm", "name": "lodash"}, "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}, {"fixed": "4.17.21"}]}]},
    {"package": {"ecosystem": "npm", "name": "lodash-es"}, "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}, {"fixed": "4.17.21"}]}]},
    {"package": {"ecosystem": "npm", "name": "lodash.template"}, "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}, {"last_affected": "4.5.0"}]}]},
    {"package": {"ecosystem": "PyPI", "name": "ignored"}, "versions": ["1", "2"]}
  ],
  "database_specific": {"severity": "HIGH", "cwe_ids": ["CWE-77"]}
}`

// Fiche Debian (tracker) : corrigée en 12, pas encore en 11 ; gravité absente, urgence Debian.
const debianCVE = `{
  "id": "DEBIAN-CVE-2024-2511", "upstream": ["CVE-2024-2511"], "modified": "2024-05-01T00:00:00Z",
  "details": "Issue summary: Some non-default TLS server configurations can cause unbounded memory growth. Impact summary: an attacker may cause a Denial of Service.",
  "affected": [
    {"package": {"ecosystem": "Debian:11", "name": "openssl"}, "ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "0"}]}], "ecosystem_specific": {"urgency": "low"}},
    {"package": {"ecosystem": "Debian:12", "name": "openssl"}, "ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "0"}, {"fixed": "3.0.13-1~deb12u1"}]}], "ecosystem_specific": {"urgency": "low"}}
  ]
}`

func decodeVuln(t *testing.T, raw string) *osvVuln {
	t.Helper()
	var v osvVuln
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestReduceAndFixedVersion(t *testing.T) {
	r := reduce(decodeVuln(t, ghsaLodash))
	if r.Severity != "high" || r.Score != 7.2 || r.Summary != "Command Injection in lodash" || fmt.Sprint(r.CVEs) != "[CVE-2021-23337]" {
		t.Fatalf("%+v", r)
	}
	if len(r.Affected) != 3 {
		t.Fatalf("écosystèmes conservés : %+v", r.Affected)
	}
	for _, c := range []struct{ name, installed, want string }{
		{"lodash", "4.17.20", "4.17.21"},
		{"lodash-es", "4.17.15", "4.17.21"},
		{"lodash.template", "4.5.0", ""}, // vulnérable jusqu'à la dernière version : pas de correction
		{"autre", "1.0.0", ""},
	} {
		if got := r.fixedFor("npm", c.name, c.installed); got != c.want {
			t.Errorf("%s %s : %q, attendu %q", c.name, c.installed, got, c.want)
		}
	}

	d := reduce(decodeVuln(t, debianCVE))
	if d.Severity != "unknown" || d.Urgency != "low" || fmt.Sprint(d.CVEs) != "[CVE-2024-2511]" {
		t.Fatalf("%+v", d)
	}
	if d.Summary != "Issue summary: Some non-default TLS server configurations can cause unbounded memory growth." {
		t.Fatalf("résumé : %q", d.Summary)
	}
	if got := d.fixedFor("Debian:12", "openssl", "3.0.11-1~deb12u2"); got != "3.0.13-1~deb12u1" {
		t.Fatalf("Debian 12 : %q", got)
	}
	if got := d.fixedFor("Debian:11", "openssl", "1.1.1w-0+deb11u1"); got != "" {
		t.Fatalf("Debian 11 non corrigée : %q", got)
	}
}

// Plusieurs plages : la version corrigée est celle de la plage qui contient la version installée.
func TestFixedForPicksContainingRange(t *testing.T) {
	r := record{Affected: []affected{{Eco: "npm", Name: "semver", Ranges: [][]event{
		{{Introduced: "0"}, {Fixed: "5.7.2"}, {Introduced: "6.0.0"}, {Fixed: "6.3.1"}, {Introduced: "7.0.0"}, {Fixed: "7.5.2"}},
	}}}}
	for installed, want := range map[string]string{"5.7.1": "5.7.2", "6.1.0": "6.3.1", "7.5.1": "7.5.2", "7.0.0-rc.1": "7.5.2"} {
		if got := r.fixedFor("npm", "semver", installed); got != want {
			t.Errorf("%s : %q, attendu %q", installed, got, want)
		}
	}
	// Plage introuvable (données incohérentes) : plus petite version corrigée supérieure.
	if got := r.fixedFor("npm", "semver", "6.0.0-beta"); got != "6.3.1" {
		t.Errorf("repli : %q", got)
	}
	// Écosystème sans version dans la fiche (« Alpine ») accepté pour « Alpine:v3.20 ».
	a := record{Affected: []affected{{Eco: "Alpine", Name: "busybox", Ranges: [][]event{{{Introduced: "0"}, {Fixed: "1.36.1-r30"}}}}}}
	if got := a.fixedFor("Alpine:v3.20", "busybox", "1.36.1-r29"); got != "1.36.1-r30" {
		t.Errorf("Alpine : %q", got)
	}
}

func TestLevels(t *testing.T) {
	for raw, want := range map[string]string{`"CRITICAL"`: "critical", `"HIGH"`: "high", `"MODERATE"`: "medium", `"Low"`: "low", `"unimportant"`: "low", `""`: "unknown", `3`: "unknown", ``: "unknown"} {
		if got := levelOf(json.RawMessage(raw)); got != want {
			t.Errorf("%s : %s, attendu %s", raw, got, want)
		}
	}
	for score, want := range map[float64]string{10: "critical", 9: "critical", 8.9: "high", 7: "high", 6.9: "medium", 4: "medium", 3.9: "low", 0: "low"} {
		if got := levelFromScore(score); got != want {
			t.Errorf("%v : %s, attendu %s", score, got, want)
		}
	}
	if s := summarize("", strings.Repeat("a", 300)); len([]rune(s)) != maxSummaryRune || !strings.HasSuffix(s, "…") {
		t.Fatalf("résumé tronqué : %d", len([]rune(s)))
	}
}
