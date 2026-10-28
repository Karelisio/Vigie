package vulns

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/images"
)

// Inventaire 0.7.0 : paquets binaires avec leur paquet source (correction des failles) et outils.
func TestInventoryBinariesAndTools(t *testing.T) {
	ar := &fakeArchiver{fs: map[string]fakeFS{
		"debian": {
			"/etc/os-release":      {data: debianOSRelease},
			"/bin/sh":              {link: "dash"},
			"/bin/dash":            {data: "ELF"},
			"/usr/bin/apt-get":     {link: "/usr/bin/apt-real"},
			"/usr/bin/apt-real":    {data: "ELF"},
			"/var/lib/dpkg/status": {data: dpkgStatus},
		},
		"distroless": {
			"/etc/os-release":              {data: debianOSRelease},
			"/var/lib/dpkg/status.d":       {dir: true},
			"/var/lib/dpkg/status.d/libc6": {data: "Package: libc6\nVersion: 2.36-9\nSource: glibc\n"},
		},
		"alpine": {
			"/etc/os-release":       {data: "ID=alpine\nVERSION_ID=3.20.3\n"},
			"/bin/sh":               {link: "/bin/busybox"},
			"/bin/busybox":          {data: "ELF"},
			"/sbin/apk":             {data: "ELF"},
			"/lib/apk/db/installed": {data: "P:libcrypto3\nV:3.3.2-r0\nA:aarch64\no:openssl\n\nP:busybox\nV:1.36.1-r29\nA:aarch64\n"},
		},
	}}
	inv := readInventory(context.Background(), ar, "debian", nil, time.Now())
	if inv.Error != "" || !reflect.DeepEqual(inv.Tools, []string{"sh", "apt-get"}) {
		t.Fatalf("%+v", inv)
	}
	want := []Binary{
		{"base-files", "base-files", "12.4+deb12u5", ""}, {"libc6", "glibc", "2.36-9+deb12u4", ""},
		{"libssl3", "openssl", "3.0.11-1~deb12u2", "arm64"}, {"openssl", "openssl", "3.0.11-1~deb12u2", ""},
		{"zlib1g", "zlib", "1:1.2.13.dfsg-1+b1", ""},
	}
	if got := inv.Binaries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if !inv.HasTool("apt-get") || inv.HasTool("apk") {
		t.Fatal("outils")
	}
	inv = readInventory(context.Background(), ar, "distroless", nil, time.Now())
	if len(inv.Tools) != 0 || len(inv.Bins) != 0 || len(inv.Packages) != 1 {
		t.Fatalf("distroless : %+v", inv)
	}
	inv = readInventory(context.Background(), ar, "alpine", nil, time.Now())
	if !reflect.DeepEqual(inv.Tools, []string{"sh", "apk"}) ||
		!reflect.DeepEqual(inv.Binaries(), []Binary{{"busybox", "busybox", "1.36.1-r29", "aarch64"}, {"libcrypto3", "openssl", "3.3.2-r0", "aarch64"}}) {
		t.Fatalf("alpine : %+v", inv)
	}
}

func TestFixesAndLevels(t *testing.T) {
	all := []Vuln{
		{ID: "a", Severity: "high", Package: "openssl", Version: "1", Fixed: "2", Ecosystem: "Debian:12"},
		{ID: "b", Severity: "critical", Package: "openssl", Version: "1", Fixed: "3", Ecosystem: "Debian:12"},
		{ID: "c", Severity: "medium", Package: "zlib", Version: "1", Fixed: "1.1", Ecosystem: "Debian:12"},
		{ID: "d", Severity: "critical", Package: "glibc", Version: "1", Ecosystem: "Debian:12"},
		{ID: "e", Severity: "high", Package: "lodash", Version: "4.17.20", Fixed: "4.17.21", Ecosystem: "npm"},
	}
	got := fixesOf(all)
	if len(got) != 3 || got[0].Package != "openssl" || got[0].Fixed != "3" || got[0].Counts.Critical != 1 || got[0].Counts.High != 1 ||
		got[1].Package != "lodash" || got[2].Package != "zlib" {
		t.Fatalf("%+v", got)
	}
	if !SystemPackages("Ubuntu:22.04:LTS") || !SystemPackages("Alpine:v3.20") || SystemPackages("npm") {
		t.Fatal("écosystèmes système")
	}
	c := Counts{Critical: 1, High: 2, Medium: 3, Low: 4, Unknown: 5}
	for level, want := range map[string]int{"critical": 1, "high": 3, "medium": 6, "low": 15, "bizarre": 0} {
		if n := c.CountAtLeast(level); n != want {
			t.Errorf("%s : %d au lieu de %d", level, n, want)
		}
	}
	if !AtLeast("unknown", "low") || AtLeast("unknown", "medium") || !AtLeast("critical", "high") || AtLeast("medium", "high") {
		t.Fatal("niveaux")
	}
}

// Après l'analyse de la nuit (ou à sa place si une analyse vient d'avoir lieu), OnNightly est appelé :
// c'est le signal de la correction automatique.
func TestNightlyHookAfterNightScan(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.s.OnNightly = func() { calls.Add(1) }
	now := time.Date(2026, 10, 28, 5, 2, 0, 0, time.Local) // créneau : 04:00 + 1 h
	e.s.now = func() time.Time { return now }
	e.s.tick(now)
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 1 || e.s.report.LastScan == 0 {
		t.Fatalf("après l'analyse de nuit : %d appel(s)", calls.Load())
	}
	// Nuit suivante, analyse récente (mise à jour d'image dans la soirée) : pas de nouvelle analyse,
	// mais la correction a lieu.
	now = now.Add(24 * time.Hour)
	e.s.mu.Lock()
	e.s.report.LastScan = now.Add(-time.Hour).UnixMilli()
	e.s.mu.Unlock()
	scans := e.dk.archiveCalls()
	e.s.tick(now)
	for calls.Load() == 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 2 || e.dk.archiveCalls() != scans || e.s.Scanning() {
		t.Fatalf("analyse récente : %d appel(s)", calls.Load())
	}
}

// Résultat enregistré par un agent 0.6 (sans paquets corrigeables) : nouvelle analyse peu après le démarrage.
func TestOutdatedReportRescanned(t *testing.T) {
	e := newEnv(t)
	old := `{"last_scan":` + strconv.FormatInt(time.Now().Add(-time.Hour).UnixMilli(), 10) +
		`,"images":[{"id":"sha256:aaaa","counts":{"critical":1,"fixable":1},"vulns":[]}]}`
	if err := e.st.Set(context.Background(), reportKey, old); err != nil {
		t.Fatal(err)
	}
	s := e.newScanner(t)
	if !s.outdated {
		t.Fatal("résultat ancien non repéré")
	}
	s.tick(time.Now()) // avant le délai de démarrage : rien
	if s.Scanning() {
		t.Fatal("analyse trop tôt")
	}
	s.tick(time.Now().Add(startupDelay + time.Minute))
	if !s.Scanning() && s.report.LastScan == 0 {
		t.Fatal("pas de nouvelle analyse")
	}
	for s.Scanning() {
		time.Sleep(5 * time.Millisecond)
	}
	if r := s.Report(context.Background()); len(r.Images) == 0 || r.Images[0].Fixes == nil {
		t.Fatalf("%+v", r.Images)
	}
}

// Conteneurs temporaires de la correction : jamais analysés. Image corrigée par Vigie : signalée.
func TestTemporaryContainersAndPatchedImages(t *testing.T) {
	list := groupByImage(nil)
	if len(list) != 0 {
		t.Fatal()
	}
	if !Temporary(TempPatchPrefix+"123") || !Temporary(TempAnalyzePrefix+"1") || Temporary("comet") {
		t.Fatal("conteneurs temporaires")
	}
	e := newEnv(t)
	e.dk.set(
		fakeContainer{id: "c1", name: "comet", image: "ghcr.io/g0ldyy/comet:latest", imageID: imgDebian, state: "running", fs: debianFS()},
		fakeContainer{id: "tmp", name: TempPatchPrefix + "1", image: imgDebian, imageID: imgAlpine, state: "exited", fs: alpineFS()},
	)
	p := &images.Patched{At: 1_800_000_000_000, BaseImage: "sha256:origine", Ref: "ghcr.io/g0ldyy/comet:latest"}
	e.dk.labels = map[string]map[string]string{imgDebian: p.Labels()}
	r := e.scanNow(t)
	if len(r.Images) != 1 || r.Images[0].Patched == nil || r.Images[0].Patched.BaseImage != "sha256:origine" {
		t.Fatalf("%+v", r.Images)
	}
	if fx := r.Images[0].Fixes; len(fx) != 1 || fx[0].Package != "openssl" || !strings.HasPrefix(fx[0].Fixed, "3.0.15") {
		t.Fatalf("paquets corrigeables : %+v", fx)
	}
}
