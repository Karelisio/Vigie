package vulnfix

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

// Les arguments passés entre apostrophes reviennent intacts, quoi qu'ils contiennent.
func TestShellQuoteRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh absent")
	}
	tricky := []string{"libssl3", "a'b", "$(id)", "`id`", "x; rm -rf /", "un espace", "ligne\nsuivante", `"'"`, "", "--", "*"}
	var quoted []string
	for _, s := range tricky {
		quoted = append(quoted, shellQuote(s))
	}
	out, err := exec.Command(sh, "-c", `for a in `+strings.Join(quoted, " ")+`; do printf '%s\0' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if !reflect.DeepEqual(got, tricky) {
		t.Fatalf("%q\nattendu %q", got, tricky)
	}
}

func TestValidNames(t *testing.T) {
	for _, n := range []string{"libssl3", "g++-12", "libstdc++6", "lib.foo+bar", "libc6:arm64", "python3.11-minimal"} {
		if !validName("apt", n) {
			t.Errorf("apt : %q refusé", n)
		}
	}
	for _, n := range []string{"", "a", "Libssl", "-o", "--allow-unauthenticated", "foo;rm", "$(id)", "a b", "x'y", "libc6:", "libc6:arm 64", "p\n"} {
		if validName("apt", n) {
			t.Errorf("apt : %q accepté", n)
		}
	}
	for _, n := range []string{"busybox", "py3-PyYAML", "so:libc.musl", "libcrypto3", "ca-certificates_bundle"} {
		want := n != "so:libc.musl"
		if validName("apk", n) != want {
			t.Errorf("apk : %q valide=%v", n, !want)
		}
	}
	for _, n := range []string{"-U", "a;b", "$(x)", "x y"} {
		if validName("apk", n) {
			t.Errorf("apk : %q accepté", n)
		}
	}
}

// Les paquets binaires viennent des paquets sources vulnérables (champ Source de dpkg).
func TestSelectBinariesFromSources(t *testing.T) {
	bins := []vulns.Binary{
		{Name: "dpkg", Source: "dpkg", Version: "1.21.22", Arch: "amd64"},
		{Name: "libssl3", Source: "openssl", Version: "3.0.11-1", Arch: "amd64"},
		{Name: "openssl", Source: "openssl", Version: "3.0.11-1", Arch: "amd64"},
		{Name: "libc6", Source: "glibc", Version: "2.36-9", Arch: "amd64"},
		{Name: "libc6", Source: "glibc", Version: "2.36-9", Arch: "i386"},
		{Name: "libc-bin", Source: "glibc", Version: "2.36-9", Arch: "amd64"},
		{Name: "tzdata", Source: "tzdata", Version: "2024a", Arch: "all"},
		{Name: "evil;rm", Source: "openssl", Version: "1", Arch: "amd64"},
		{Name: "zlib1g", Source: "zlib", Version: "1:1.2.13", Arch: "amd64"},
	}
	args, rejected := selectBinaries("apt", bins, map[string]bool{"openssl": true, "glibc": true}, []string{"zlib1g", "absent-pkg"})
	want := []string{"libc-bin", "libc6", "libc6:i386", "libssl3", "openssl", "zlib1g"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("%v", args)
	}
	if !reflect.DeepEqual(rejected, []string{"evil;rm"}) {
		t.Fatalf("écartés : %v", rejected)
	}
	// Alpine : paquets d'origine, sans architecture.
	abins := []vulns.Binary{{Name: "busybox", Source: "busybox", Arch: "aarch64"}, {Name: "ssl_client", Source: "busybox", Arch: "aarch64"}, {Name: "musl", Source: "musl"}}
	args, _ = selectBinaries("apk", abins, map[string]bool{"busybox": true}, nil)
	if !reflect.DeepEqual(args, []string{"busybox", "ssl_client"}) {
		t.Fatalf("%v", args)
	}
	if got := nativeArch([]vulns.Binary{{Name: "a", Arch: "arm64"}, {Name: "b", Arch: "arm64"}, {Name: "c", Arch: "armhf"}}); got != "arm64" {
		t.Fatalf("architecture native %q", got)
	}
}

func TestUpgradeScripts(t *testing.T) {
	apt := upgradeScript("apt", []string{"libssl3", "libc6:i386"})
	for _, want := range []string{
		"set -eu", "export DEBIAN_FRONTEND=noninteractive", "apt-get update -q",
		"apt-get install -y -q --only-upgrade --no-install-recommends --no-remove -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold 'libssl3' 'libc6:i386'\n",
		"apt-get clean", `if [ "$keep" = 0 ]; then rm -rf /var/lib/apt/lists/*; fi`,
	} {
		if !strings.Contains(apt, want) {
			t.Errorf("script apt sans %q :\n%s", want, apt)
		}
	}
	apk := upgradeScript("apk", []string{"busybox", "ssl_client"})
	if !strings.Contains(apk, "apk upgrade --no-cache 'busybox' 'ssl_client'\n") || strings.Contains(apk, "apt-get") {
		t.Fatalf("script apk :\n%s", apk)
	}
	// Syntaxe valide pour sh.
	if sh, err := exec.LookPath("sh"); err == nil {
		for _, s := range []string{apt, apk} {
			if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
				t.Fatalf("syntaxe : %v %s", err, out)
			}
		}
	}
}

func TestDiffAndMergeChanges(t *testing.T) {
	before := []vulns.Binary{{Name: "a", Version: "1"}, {Name: "b", Version: "1"}, {Name: "c", Version: "1"}}
	after := []vulns.Binary{{Name: "a", Version: "2"}, {Name: "b", Version: "1"}, {Name: "d", Version: "1"}}
	got := diffBins(before, after)
	want := []images.PackageChange{{Name: "a", From: "1", To: "2"}, {Name: "c", From: "1"}, {Name: "d", To: "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if len(diffBins(before, before)) != 0 {
		t.Fatal("rien n'a changé")
	}
	merged := mergeChanges([]images.PackageChange{{Name: "a", From: "1", To: "2"}, {Name: "z", From: "1", To: "2"}},
		[]images.PackageChange{{Name: "a", From: "2", To: "3"}, {Name: "b", From: "1", To: "2"}})
	if !reflect.DeepEqual(merged, []images.PackageChange{{Name: "a", From: "1", To: "3"}, {Name: "b", From: "1", To: "2"}, {Name: "z", From: "1", To: "2"}}) {
		t.Fatalf("%+v", merged)
	}
	if d := describeChanges(want, 2); d != "a 1 → 2, c (retiré), …" {
		t.Fatalf("%q", d)
	}
}
