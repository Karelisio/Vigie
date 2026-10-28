package vulns

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const debianOSRelease = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
VERSION_CODENAME=bookworm
ID=debian
HOME_URL="https://www.debian.org/"
`

const dpkgStatus = `Package: libssl3
Status: install ok installed
Priority: optional
Installed-Size: 6160
Architecture: arm64
Multi-Arch: same
Source: openssl
Version: 3.0.11-1~deb12u2
Depends: libc6 (>= 2.34)
Description: Secure Sockets Layer toolkit - shared libraries
 This package is part of the OpenSSL project's implementation of the SSL
 and TLS cryptographic protocols for secure communication over the
 Internet.

Package: openssl
Status: install ok installed
Source: openssl
Version: 3.0.11-1~deb12u2

Package: libc6
Status: install ok installed
Source: glibc
Version: 2.36-9+deb12u4

Package: zlib1g
Status: install ok installed
Source: zlib (1:1.2.13.dfsg-1)
Version: 1:1.2.13.dfsg-1+b1

Package: oldlib
Status: deinstall ok config-files
Version: 1.0-1

Package: base-files
Status: install ok installed
Version: 12.4+deb12u5
`

func TestParseOSRelease(t *testing.T) {
	o := parseOSRelease(strings.NewReader(debianOSRelease))
	if o.ID != "debian" || o.VersionID != "12" || o.PrettyName != "Debian GNU/Linux 12 (bookworm)" {
		t.Fatalf("%+v", o)
	}
	o = parseOSRelease(strings.NewReader("NAME='Alpine Linux'\nID=alpine\nVERSION_ID=3.20.3\n# commentaire\nPRETTY_NAME=\"Alpine Linux v3.20\"\n"))
	if o.ID != "alpine" || o.VersionID != "3.20.3" || o.Name() != "Alpine Linux v3.20" {
		t.Fatalf("%+v", o)
	}
	if n := (OSInfo{ID: "wolfi", VersionID: "20230201"}).Name(); n != "wolfi 20230201" {
		t.Fatal(n)
	}
}

func TestOSEcosystem(t *testing.T) {
	for _, c := range []struct {
		o    OSInfo
		want string
	}{
		{OSInfo{ID: "debian", VersionID: "12"}, "Debian:12"},
		{OSInfo{ID: "debian", VersionID: "11"}, "Debian:11"},
		{OSInfo{ID: "debian"}, ""}, // testing / sid
		{OSInfo{ID: "ubuntu", VersionID: "22.04", Version: "22.04.4 LTS (Jammy Jellyfish)"}, "Ubuntu:22.04:LTS"},
		{OSInfo{ID: "ubuntu", VersionID: "24.10", Version: "24.10 (Oracular Oriole)"}, "Ubuntu:24.10"},
		{OSInfo{ID: "ubuntu", VersionID: "24.04"}, "Ubuntu:24.04:LTS"},
		{OSInfo{ID: "ubuntu", VersionID: "23.04"}, "Ubuntu:23.04"},
		{OSInfo{ID: "alpine", VersionID: "3.20.3"}, "Alpine:v3.20"},
		{OSInfo{ID: "alpine", VersionID: "3.19"}, "Alpine:v3.19"},
		{OSInfo{ID: "alpine", VersionID: "3.21.0_alpha20240807"}, "Alpine:v3.21"},
		{OSInfo{ID: "alpine", VersionID: "edge"}, ""},
		{OSInfo{ID: "fedora", VersionID: "40"}, ""},
		{OSInfo{}, ""},
	} {
		if got := osEcosystem(c.o); got != c.want {
			t.Errorf("%+v : %q, attendu %q", c.o, got, c.want)
		}
	}
}

func collect() (*[]Package, func(Package)) {
	var list []Package
	return &list, func(p Package) { list = append(list, p) }
}

func TestDpkgPackagesUseSource(t *testing.T) {
	list, add := collect()
	n, err := dpkgPackages(strings.NewReader(dpkgStatus), "Debian:12", add)
	if err != nil || n != 5 {
		t.Fatalf("%d paquets (%v)", n, err)
	}
	want := []Package{
		{"Debian:12", "openssl", "3.0.11-1~deb12u2"},
		{"Debian:12", "openssl", "3.0.11-1~deb12u2"},
		{"Debian:12", "glibc", "2.36-9+deb12u4"},
		{"Debian:12", "zlib", "1:1.2.13.dfsg-1"}, // version source, sans le +b1 de la recompilation
		{"Debian:12", "base-files", "12.4+deb12u5"},
	}
	if !reflect.DeepEqual(*list, want) {
		t.Fatalf("%+v", *list)
	}
}

func TestApkPackagesUseOrigin(t *testing.T) {
	db := "C:Q1abc=\nP:libcrypto3\nV:3.3.2-r0\nA:aarch64\no:openssl\nT:Crypto library\n\nP:busybox\nV:1.36.1-r29\n\nP:musl\nV:1.2.5-r0\no:musl\n"
	list, add := collect()
	n, err := apkPackages(strings.NewReader(db), "Alpine:v3.20", add)
	if err != nil || n != 3 {
		t.Fatalf("%d (%v)", n, err)
	}
	want := []Package{{"Alpine:v3.20", "openssl", "3.3.2-r0"}, {"Alpine:v3.20", "busybox", "1.36.1-r29"}, {"Alpine:v3.20", "musl", "1.2.5-r0"}}
	if !reflect.DeepEqual(*list, want) {
		t.Fatalf("%+v", *list)
	}
}

const lockV3 = `{
  "name": "aiostreams", "version": "1.0.0", "lockfileVersion": 3, "requires": true,
  "packages": {
    "": {"name": "aiostreams", "version": "1.0.0", "dependencies": {"express": "^4.18.2"}},
    "node_modules/express": {"version": "4.18.2", "resolved": "https://registry.npmjs.org/express/-/express-4.18.2.tgz", "integrity": "sha512-x", "dependencies": {"body-parser": "1.20.1"}},
    "node_modules/express/node_modules/debug": {"version": "2.6.9"},
    "node_modules/@scope/pkg": {"version": "2.0.0-beta.1"},
    "node_modules/my-alias": {"name": "lodash", "version": "4.17.20"},
    "node_modules/jest": {"version": "29.7.0", "dev": true},
    "node_modules/local": {"resolved": "packages/local", "link": true},
    "node_modules/from-git": {"version": "git+ssh://git@github.com/x/y.git#abc"},
    "packages/local": {"name": "local", "version": "0.0.1"}
  }
}`

const lockV1 = `{
  "name": "old", "lockfileVersion": 1,
  "dependencies": {
    "minimist": {"version": "1.2.5", "integrity": "x"},
    "mocha": {"version": "8.0.0", "dev": true, "dependencies": {"debug": {"version": "4.1.1", "dev": true}}},
    "request": {"version": "2.88.2", "requires": {"qs": "~6.5.2"}, "dependencies": {"qs": {"version": "6.5.2"}}}
  }
}`

func TestNpmPackages(t *testing.T) {
	list, add := collect()
	n, err := npmPackages(strings.NewReader(lockV3), false, add)
	if err != nil || n != 4 {
		t.Fatalf("%d (%v) %+v", n, err, *list)
	}
	want := []Package{{"npm", "express", "4.18.2"}, {"npm", "debug", "2.6.9"}, {"npm", "@scope/pkg", "2.0.0-beta.1"}, {"npm", "lodash", "4.17.20"}}
	if !reflect.DeepEqual(*list, want) {
		t.Fatalf("%+v", *list)
	}
	// Fichier caché de node_modules : les dépendances de développement présentes comptent.
	list, add = collect()
	if n, err := npmPackages(strings.NewReader(lockV3), true, add); err != nil || n != 5 {
		t.Fatalf("%d (%v)", n, err)
	}
	// lockfileVersion 1 : arbre imbriqué.
	list, add = collect()
	n, err = npmPackages(strings.NewReader(lockV1), false, add)
	if err != nil || n != 3 {
		t.Fatalf("%d (%v) %+v", n, err, *list)
	}
	want = []Package{{"npm", "minimist", "1.2.5"}, {"npm", "qs", "6.5.2"}, {"npm", "request", "2.88.2"}}
	if !reflect.DeepEqual(*list, want) {
		t.Fatalf("%+v", *list)
	}
	if _, err := npmPackages(strings.NewReader(`{"packages": [1, 2]}`), false, add); err == nil {
		t.Fatal("erreur attendue pour un JSON inattendu")
	}
}

func TestReadInventoryDebianDistrolessAndNpm(t *testing.T) {
	ar := &fakeArchiver{fs: map[string]fakeFS{
		"debian": {
			"/etc/os-release":                      {link: "../usr/lib/os-release"},
			"/usr/lib/os-release":                  {data: debianOSRelease},
			"/var/lib/dpkg/status":                 {data: dpkgStatus},
			"/app/node_modules/.package-lock.json": {data: lockV3},
			"/app/package-lock.json":               {data: lockV1}, // ignoré : le fichier caché suffit
		},
		"distroless": {
			"/etc/os-release":                        {data: debianOSRelease},
			"/var/lib/dpkg/status.d":                 {dir: true},
			"/var/lib/dpkg/status.d/libc6":           {data: "Package: libc6\nVersion: 2.36-9+deb12u7\nSource: glibc\nArchitecture: arm64\n"},
			"/var/lib/dpkg/status.d/libc6.md5sums":   {data: "abc  lib/ld.so\n"},
			"/var/lib/dpkg/status.d/tzdata":          {data: "Package: tzdata\nVersion: 2024a-0+deb12u1\n"},
			"/var/lib/dpkg/status.d/libssl3":         {data: "Package: libssl3\nVersion: 3.0.13-1~deb12u1\nSource: openssl\n"},
			"/srv/package-lock.json":                 {data: lockV1},
			"/var/lib/dpkg/status.d/sub/ignored.md5": {data: ""},
		},
	}}
	now := time.UnixMilli(1_800_000_000_000)
	inv := readInventory(context.Background(), ar, "debian", []string{"/app", "/app"}, now)
	if inv.Error != "" || inv.OS != "Debian GNU/Linux 12 (bookworm)" || inv.Eco != "Debian:12" || inv.ReadAt != now.UnixMilli() {
		t.Fatalf("%+v", inv)
	}
	// node_modules/.package-lock.json : jest (dev) est installé, donc compté.
	if inv.Count != 5+5 || len(inv.Packages) != 4+5 {
		t.Fatalf("%d paquets, %d requêtes : %v", inv.Count, len(inv.Packages), inv.Packages)
	}
	if inv.Packages[0] != [3]string{"Debian:12", "base-files", "12.4+deb12u5"} || inv.Packages[len(inv.Packages)-1] != [3]string{"npm", "lodash", "4.17.20"} {
		t.Fatalf("tri : %v", inv.Packages)
	}
	for _, c := range ar.calls {
		if c == "debian:/app/package-lock.json" {
			t.Fatal("package-lock.json lu alors que node_modules/.package-lock.json existe")
		}
	}

	inv = readInventory(context.Background(), ar, "distroless", []string{"/srv"}, now)
	want := [][3]string{
		{"Debian:12", "glibc", "2.36-9+deb12u7"}, {"Debian:12", "openssl", "3.0.13-1~deb12u1"}, {"Debian:12", "tzdata", "2024a-0+deb12u1"},
		{"npm", "minimist", "1.2.5"}, {"npm", "qs", "6.5.2"}, {"npm", "request", "2.88.2"},
	}
	if inv.Error != "" || !reflect.DeepEqual(inv.Packages, want) || inv.Count != 6 {
		t.Fatalf("%+v", inv)
	}
}

func TestReadInventoryAlpineAndLimits(t *testing.T) {
	ar := &fakeArchiver{fs: map[string]fakeFS{
		"alpine": {
			"/etc/os-release":       {data: "ID=alpine\nVERSION_ID=3.20.3\nPRETTY_NAME=\"Alpine Linux v3.20\"\n"},
			"/lib/apk/db/installed": {data: "P:busybox\nV:1.36.1-r29\no:busybox\n\nP:ssl_client\nV:1.36.1-r29\no:busybox\n"},
		},
		"loop": {
			"/etc/os-release":     {link: "/usr/lib/os-release"},
			"/usr/lib/os-release": {link: "/etc/os-release"},
		},
		"huge": {
			"/etc/os-release": {data: strings.Repeat("#", maxOSRelease+1)},
		},
		"scratch": {},
	}}
	inv := readInventory(context.Background(), ar, "alpine", nil, time.Now())
	if inv.Eco != "Alpine:v3.20" || inv.Count != 2 || !reflect.DeepEqual(inv.Packages, [][3]string{{"Alpine:v3.20", "busybox", "1.36.1-r29"}}) {
		t.Fatalf("%+v", inv)
	}
	inv = readInventory(context.Background(), ar, "loop", nil, time.Now())
	if !strings.Contains(inv.Error, "trop de liens symboliques") {
		t.Fatalf("boucle de liens : %+v", inv)
	}
	inv = readInventory(context.Background(), ar, "huge", nil, time.Now())
	if !strings.Contains(inv.Error, "trop volumineux") {
		t.Fatalf("taille maximale : %+v", inv)
	}
	inv = readInventory(context.Background(), ar, "scratch", []string{"/"}, time.Now())
	if inv.Error != "" || inv.OS != "" || len(inv.Packages) != 0 {
		t.Fatalf("image vide : %+v", inv)
	}
}

func TestInventoryKeepsAtMostMaxPackages(t *testing.T) {
	var b strings.Builder
	for i := range maxPackages + 10 {
		fmt.Fprintf(&b, "P:pkg%d\nV:1.0-r0\n\n", i)
	}
	ar := &fakeArchiver{fs: map[string]fakeFS{"big": {
		"/etc/os-release":       {data: "ID=alpine\nVERSION_ID=3.20.3\n"},
		"/lib/apk/db/installed": {data: b.String()},
	}}}
	inv := readInventory(context.Background(), ar, "big", nil, time.Now())
	if len(inv.Packages) != maxPackages || !inv.Truncated {
		t.Fatalf("%d paquets, tronqué %v", len(inv.Packages), inv.Truncated)
	}
}
