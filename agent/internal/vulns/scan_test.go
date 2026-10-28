package vulns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	imgDebian = "sha256:aaaa"
	imgAlpine = "sha256:bbbb"
	imgEmpty  = "sha256:cccc"
	imgNew    = "sha256:dddd"
)

func debianFS() fakeFS {
	return fakeFS{
		"/etc/os-release":      {link: "../usr/lib/os-release"},
		"/usr/lib/os-release":  {data: debianOSRelease},
		"/var/lib/dpkg/status": {data: dpkgStatus},
	}
}

func alpineFS() fakeFS {
	return fakeFS{
		"/etc/os-release":                          {data: "ID=alpine\nVERSION_ID=3.20.3\nPRETTY_NAME=\"Alpine Linux v3.20\"\n"},
		"/lib/apk/db/installed":                    {data: "P:busybox\nV:1.36.1-r29\no:busybox\n\nP:musl\nV:1.2.5-r0\n"},
		"/srv/app/node_modules/.package-lock.json": {data: lockV3},
	}
}

// seedOSV : openssl critique corrigée (et son bulletin DSA redondant), glibc sans gravité ni correction
// (gravité de la fiche CVE), busybox critique en CVSS v4, lodash élevée corrigée.
func seedOSV(f *fakeOSV) {
	sev3 := func(v string) []map[string]any { return []map[string]any{{"type": "CVSS_V3", "score": v}} }
	rng := func(eco, name, fixed string) []map[string]any {
		events := []map[string]any{{"introduced": "0"}}
		if fixed != "" {
			events = append(events, map[string]any{"fixed": fixed})
		}
		return []map[string]any{{"package": map[string]any{"ecosystem": eco, "name": name}, "ranges": []map[string]any{{"type": "ECOSYSTEM", "events": events}}}}
	}
	f.add(map[string]any{"id": "DEBIAN-CVE-2024-0001", "upstream": []string{"CVE-2024-0001"}, "details": "Débordement dans openssl.",
		"severity": sev3("CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"), "affected": rng("Debian:12", "openssl", "3.0.15-1~deb12u1")},
		"Debian:12|openssl|3.0.11-1~deb12u2")
	f.add(map[string]any{"id": "DSA-5900-1", "upstream": []string{"CVE-2024-0001"}, "summary": "openssl security update",
		"affected": rng("Debian:12", "openssl", "3.0.15-1~deb12u1")}, "Debian:12|openssl|3.0.11-1~deb12u2")
	f.add(map[string]any{"id": "DEBIAN-CVE-2024-0002", "upstream": []string{"CVE-2024-0002"}, "details": "glibc : fuite.",
		"affected": rng("Debian:12", "glibc", "")}, "Debian:12|glibc|2.36-9+deb12u4")
	f.add(map[string]any{"id": "CVE-2024-0002", "severity": sev3("CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H")})
	f.add(map[string]any{"id": "ALPINE-CVE-2024-0003", "upstream": []string{"CVE-2024-0003"}, "summary": "busybox awk",
		"severity": []map[string]any{{"type": "CVSS_V4", "score": "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"}},
		"affected": rng("Alpine:v3.20", "busybox", "1.36.1-r30")}, "Alpine:v3.20|busybox|1.36.1-r29")
	f.add(map[string]any{"id": "GHSA-35jh-r3h4-6jhm", "aliases": []string{"CVE-2021-23337"}, "summary": "Command Injection in lodash",
		"severity": sev3("CVSS:3.1/AV:N/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:H"), "affected": rng("npm", "lodash", "4.17.21")}, "npm|lodash|4.17.20")
}

type testEnv struct {
	dk      *fakeDocker
	osv     *fakeOSV
	st      *store.Store
	set     settings.Settings
	setMu   sync.Mutex
	reports []Report
	s       *Scanner
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &testEnv{st: st, osv: newFakeOSV(t)}
	e.set = settings.Settings{VulnScan: true, VulnAlert: "critical", AutoUpdateTime: "04:00"}
	seedOSV(e.osv)
	e.dk = newFakeDocker(t,
		fakeContainer{id: "c1", name: "comet", image: "ghcr.io/g0ldyy/comet:latest", imageID: imgDebian, state: "running", fs: debianFS()},
		fakeContainer{id: "c2", name: "comet-worker", image: imgDebian, imageID: imgDebian, state: "exited", fs: debianFS()},
		fakeContainer{id: "c3", name: "aiostreams", image: "ghcr.io/viren070/aiostreams:latest", imageID: imgAlpine, state: "running", fs: alpineFS()},
		fakeContainer{id: "c4", name: "static", image: "ghcr.io/x/static:1", imageID: imgEmpty, state: "running", fs: fakeFS{}},
	)
	e.dk.workDirs[imgAlpine] = "/srv/app"
	e.s = e.newScanner(t)
	return e
}

func (e *testEnv) newScanner(t *testing.T) *Scanner {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := New(ctx, e.dk.client(t), e.st, func() settings.Settings {
		e.setMu.Lock()
		defer e.setMu.Unlock()
		return e.set
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		Version: "test", OSVURL: e.osv.srv.URL,
		UpdateAvailable: func(name string) bool { return name == "aiostreams" },
	})
	s.OnReport = func(r Report) {
		e.setMu.Lock()
		e.reports = append(e.reports, r)
		e.setMu.Unlock()
	}
	return s
}

// scanNow lance une analyse et attend sa fin.
func (e *testEnv) scanNow(t *testing.T) Report {
	t.Helper()
	if err := e.s.Start("test"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.s.Scanning() {
		if time.Now().After(deadline) {
			t.Fatal("analyse trop longue")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return e.s.Report(context.Background())
}

func findImage(t *testing.T, r Report, id string) Image {
	t.Helper()
	for _, img := range r.Images {
		if img.ID == id {
			return img
		}
	}
	t.Fatalf("image %s absente : %+v", id, r.Images)
	return Image{}
}

func TestScanBuildsReport(t *testing.T) {
	e := newEnv(t)
	if r := e.s.Report(context.Background()); r.Images == nil || r.LastScan != 0 || !r.Enabled {
		t.Fatalf("avant la première analyse : %+v", r)
	}
	r := e.scanNow(t)
	if r.Error != "" || r.LastScan == 0 || r.Scanning || !r.Enabled || r.NextScan == 0 {
		t.Fatalf("état : %+v", r)
	}
	// Tri : l'image Alpine (2 failles corrigeables) passe avant Debian (1), puis l'image vide.
	if len(r.Images) != 3 || r.Images[0].ID != imgAlpine || r.Images[1].ID != imgDebian || r.Images[2].ID != imgEmpty {
		t.Fatalf("images : %+v", r.Images)
	}
	deb := findImage(t, r, imgDebian)
	if deb.Ref != "ghcr.io/g0ldyy/comet:latest" || deb.OS != "Debian GNU/Linux 12 (bookworm)" || deb.Ecosystem != "Debian:12" || deb.Packages != 5 {
		t.Fatalf("image Debian : %+v", deb)
	}
	if len(deb.Containers) != 2 || deb.Containers[0].Name != "comet" || deb.Containers[1].Name != "comet-worker" || deb.UpdateAvailable {
		t.Fatalf("conteneurs : %+v", deb.Containers)
	}
	if deb.Counts != (Counts{Critical: 1, High: 1, Fixable: 1}) || len(deb.Vulns) != 2 {
		t.Fatalf("failles Debian (bulletin DSA redondant retiré) : %+v %+v", deb.Counts, deb.Vulns)
	}
	v := deb.Vulns[0]
	want := Vuln{ID: "DEBIAN-CVE-2024-0001", Aliases: []string{"CVE-2024-0001"}, Summary: "Débordement dans openssl.", Severity: "critical", Score: 9.8,
		Package: "openssl", Version: "3.0.11-1~deb12u2", Fixed: "3.0.15-1~deb12u1", Ecosystem: "Debian:12", URL: "https://osv.dev/vulnerability/DEBIAN-CVE-2024-0001"}
	if fmt.Sprint(v) != fmt.Sprint(want) {
		t.Fatalf("faille :\n%+v\nattendu\n%+v", v, want)
	}
	// Gravité absente : celle de la fiche CVE (NVD).
	if g := deb.Vulns[1]; g.ID != "DEBIAN-CVE-2024-0002" || g.Severity != "high" || g.Score != 7.5 || g.Fixed != "" {
		t.Fatalf("glibc : %+v", g)
	}
	alp := findImage(t, r, imgAlpine)
	if !alp.UpdateAvailable || alp.Ecosystem != "Alpine:v3.20" || alp.Counts != (Counts{Critical: 1, High: 1, Fixable: 2}) {
		t.Fatalf("image Alpine : %+v", alp)
	}
	if alp.Vulns[0].ID != "ALPINE-CVE-2024-0003" || alp.Vulns[0].Score != 9.3 || alp.Vulns[1].Package != "lodash" || alp.Vulns[1].Fixed != "4.17.21" {
		t.Fatalf("failles Alpine : %+v", alp.Vulns)
	}
	if empty := findImage(t, r, imgEmpty); empty.OS != "" || empty.Packages != 0 || len(empty.Vulns) != 0 || empty.Vulns == nil {
		t.Fatalf("image vide : %+v", empty)
	}
	if r.Totals != (Totals{Counts: Counts{Critical: 2, High: 2, Fixable: 3}, Images: 3, Vulnerable: 2}) {
		t.Fatalf("totaux : %+v", r.Totals)
	}
	if len(e.reports) != 1 || len(e.reports[0].Images) != 3 {
		t.Fatalf("OnReport : %d", len(e.reports))
	}
	// Le JSON de l'API : champs attendus par l'appli.
	b, _ := json.Marshal(r)
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"enabled", "scanning", "last_scan", "next_scan", "totals", "images", "duration"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("champ %q absent", k)
		}
	}
}

func TestScanUsesCaches(t *testing.T) {
	e := newEnv(t)
	e.scanNow(t)
	archives := e.dk.archiveCalls()
	_, _, details := e.osv.counts()
	if details != 7 { // 6 fiches + la fiche CVE de glibc
		t.Fatalf("%d fiches lues", details)
	}

	// Deuxième analyse : inventaires (par ID d'image) et fiches (même date de modification) en cache.
	e.scanNow(t)
	if got := e.dk.archiveCalls(); got != archives {
		t.Fatalf("inventaires relus : %d archives au lieu de %d", got, archives)
	}
	if _, _, d := e.osv.counts(); d != details {
		t.Fatalf("fiches relues : %d", d)
	}

	// Fiche modifiée dans OSV : relue.
	e.osv.mu.Lock()
	e.osv.records["GHSA-35jh-r3h4-6jhm"]["modified"] = "2026-10-20T00:00:00Z"
	e.osv.mu.Unlock()
	e.scanNow(t)
	if _, _, d := e.osv.counts(); d != details+1 {
		t.Fatalf("fiche modifiée : %d lectures", d-details)
	}

	// Nouvelle image pour comet : seule elle est lue, l'ancienne image (encore utilisée par comet-worker) reste.
	e.dk.set(
		fakeContainer{id: "c5", name: "comet", image: "ghcr.io/g0ldyy/comet:latest", imageID: imgNew, state: "running", fs: debianFS()},
		fakeContainer{id: "c3", name: "aiostreams", image: "ghcr.io/viren070/aiostreams:latest", imageID: imgAlpine, state: "running", fs: alpineFS()},
	)
	before := e.dk.archiveCalls()
	r := e.scanNow(t)
	// os-release (lien puis cible), dpkg status, status.d, puis les deux package-lock de /app (absents).
	if got := e.dk.archiveCalls() - before; got != 6 {
		t.Fatalf("%d archives lues pour la nouvelle image", got)
	}
	if len(r.Images) != 2 || findImage(t, r, imgNew).Containers[0].Name != "comet" {
		t.Fatalf("%+v", r.Images)
	}
	if e.st.VulnInventory(context.Background(), imgDebian) != "" || e.st.VulnInventory(context.Background(), imgNew) == "" {
		t.Fatal("inventaire de l'ancienne image non supprimé")
	}

	// Redémarrage de l'agent : dernier résultat relu en base.
	again := e.newScanner(t).Report(context.Background())
	if again.LastScan != r.LastScan || len(again.Images) != 2 {
		t.Fatalf("après redémarrage : %+v", again)
	}
}

func TestScanDisabledAndErrors(t *testing.T) {
	e := newEnv(t)
	e.set.VulnScan = false
	if err := e.s.Start("test"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("désactivée : %v", err)
	}
	if b, q, d := e.osv.counts(); b+q+d != 0 {
		t.Fatal("OSV interrogé alors que l'analyse est désactivée")
	}
	if r := e.s.Report(context.Background()); r.Enabled || r.NextScan != 0 {
		t.Fatalf("%+v", r)
	}

	e.set.VulnScan = true
	e.osv.mu.Lock()
	e.osv.fail = 2
	e.osv.mu.Unlock()
	r := e.scanNow(t)
	if r.Error != "OSV a répondu 500 : internal error" || r.LastScan != 0 || r.ErrorAt == 0 || len(e.reports) != 0 {
		t.Fatalf("échec : %+v", r)
	}
	// Un conteneur supprimé : son image disparaît du résultat sans nouvelle analyse.
	r = e.scanNow(t)
	if r.Error != "" || len(r.Images) != 3 {
		t.Fatalf("%+v", r)
	}
	e.dk.set(fakeContainer{id: "c3", name: "aiostreams", image: "x", imageID: imgAlpine, state: "running", fs: alpineFS()})
	if r = e.s.Report(context.Background()); len(r.Images) != 1 || r.Totals.Images != 1 || r.Totals.Critical != 1 {
		t.Fatalf("%+v", r)
	}
}

// Après une mise à jour d'image : une analyse, regroupée si plusieurs mises à jour se suivent,
// et relancée si une analyse était déjà en cours.
func TestScanAfterImageUpdates(t *testing.T) {
	e := newEnv(t)
	updateDelay = 20 * time.Millisecond
	t.Cleanup(func() { updateDelay = 90 * time.Second })
	e.s.ImagesUpdated()
	e.s.ImagesUpdated()
	waitFor(t, func() bool { r := e.s.Report(context.Background()); return r.LastScan != 0 && !r.Scanning })
	if b, _, _ := e.osv.counts(); b != 1 {
		t.Fatalf("%d analyses au lieu d'une", b)
	}

	e.s.mu.Lock()
	e.s.scanning = true // analyse en cours (simulée)
	e.s.mu.Unlock()
	e.s.ImagesUpdated()
	waitFor(t, func() bool { e.s.mu.Lock(); defer e.s.mu.Unlock(); return e.s.again })
	e.s.mu.Lock()
	e.s.scanning = false
	e.s.again = false
	e.s.mu.Unlock()
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("délai dépassé")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartIsExclusive(t *testing.T) {
	e := newEnv(t)
	e.s.mu.Lock()
	e.s.scanning = true
	e.s.mu.Unlock()
	if err := e.s.Start("test"); !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	if r := e.s.Report(context.Background()); !r.Scanning || r.Progress == nil {
		t.Fatalf("%+v", r)
	}
}

func TestNightlySlot(t *testing.T) {
	loc := time.FixedZone("Paris", 3600)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 28, h, m, 0, 0, loc) }
	if _, due := nightlySlot(at(4, 50), "04:00", ""); due {
		t.Fatal("avant 05:00 : rien")
	}
	slot, due := nightlySlot(at(5, 10), "04:00", "")
	if !due || slot != at(5, 0) {
		t.Fatalf("05:10 : %v %v", slot, due)
	}
	if _, due := nightlySlot(at(5, 10), "04:00", "2026-10-28"); due {
		t.Fatal("déjà lancée aujourd'hui")
	}
	// Mises à jour à 23:30 : analyse à 00:30 le lendemain.
	slot, due = nightlySlot(at(0, 40), "23:30", "2026-10-27")
	if !due || slot != at(0, 30) {
		t.Fatalf("après minuit : %v %v", slot, due)
	}
	if next := nextSlot(at(12, 0), "04:00", "2026-10-28"); next != time.Date(2026, 10, 29, 5, 0, 0, 0, loc) {
		t.Fatalf("prochaine : %v", next)
	}
	if next := nextSlot(at(3, 0), "04:00", "2026-10-27"); next != at(5, 0) {
		t.Fatalf("prochaine : %v", next)
	}
}

func TestDropCoveredAdvisories(t *testing.T) {
	recs := map[string]*record{
		"DEBIAN-CVE-1": {ID: "DEBIAN-CVE-1", CVEs: []string{"CVE-1"}},
		"DEBIAN-CVE-2": {ID: "DEBIAN-CVE-2", CVEs: []string{"CVE-2"}},
		"DSA-1":        {ID: "DSA-1", CVEs: []string{"CVE-1", "CVE-2"}},
		"DSA-2":        {ID: "DSA-2", CVEs: []string{"CVE-1", "CVE-3"}}, // CVE-3 sans fiche propre : gardé
		"GHSA-1":       {ID: "GHSA-1", CVEs: []string{"CVE-1"}},         // autre paquet : gardé
		"OSV-1":        {ID: "OSV-1"},
	}
	v := func(id, pkg string) Vuln { return Vuln{ID: id, Package: pkg, Ecosystem: "Debian:12", Version: "1"} }
	got := dropCoveredAdvisories([]Vuln{v("DEBIAN-CVE-1", "a"), v("DEBIAN-CVE-2", "a"), v("DSA-1", "a"), v("DSA-2", "a"), v("GHSA-1", "b"), v("OSV-1", "a")}, recs)
	var ids []string
	for _, x := range got {
		ids = append(ids, x.ID)
	}
	if fmt.Sprint(ids) != "[DEBIAN-CVE-1 DEBIAN-CVE-2 DSA-2 GHSA-1 OSV-1]" {
		t.Fatal(ids)
	}
}
