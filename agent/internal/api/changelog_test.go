package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestImageSource(t *testing.T) {
	for _, c := range []struct{ label, ref, want string }{
		{"https://github.com/Viren070/AIOStreams", "ghcr.io/viren070/aiostreams:latest", "Viren070/AIOStreams"},
		{"https://github.com/g0ldyy/comet.git", "g0ldyy/comet", "g0ldyy/comet"},
		{"git@github.com:linuxserver/docker-sonarr.git", "lscr.io/linuxserver/sonarr", "linuxserver/docker-sonarr"},
		{"https://www.GitHub.com/owner/repo/tree/main#readme", "", "owner/repo"},
		{"github.com/owner/repo", "", "owner/repo"},
		// Label absent ou hors de GitHub : référence ghcr.io.
		{"", "ghcr.io/karelisio/vigie-agent:0.3.0", "karelisio/vigie-agent"},
		{"https://gitlab.com/x/y", "ghcr.io/owner/app/worker@sha256:abc", "owner/app"},
		{"", "redis:7", ""},
		{"https://gitlab.com/x/y", "registry.gitlab.com/x/y", ""},
		{"https://github.com.evil.example/x/y", "", ""},
		{"https://github.com/owner", "", ""},
		{"https://github.com/../..", "", ""},
	} {
		got, why := imageSource(c.label, c.ref)
		if got != c.want || (got == "") == (why == "") {
			t.Errorf("imageSource(%q, %q) = %q, %q ; attendu %q", c.label, c.ref, got, why, c.want)
		}
	}
	if _, why := imageSource("https://gitlab.com/x/y", "redis"); !strings.Contains(why, "pas sur GitHub") {
		t.Errorf("raison = %q", why)
	}
	if _, why := imageSource("", "redis"); !strings.Contains(why, labelSource) {
		t.Errorf("raison = %q", why)
	}
}

func TestCurrentVersion(t *testing.T) {
	for _, c := range []struct{ label, ref, want string }{
		{"1.4.2", "g0ldyy/comet:latest", "1.4.2"},
		{"", "nginx:1.27-alpine", "1.27-alpine"},
		{"", "localhost:5000/app", ""},
		{"", "ghcr.io/x/y:latest", ""},
		{"latest", "ghcr.io/x/y", ""},
		{"", "nginx@sha256:abc", ""},
	} {
		if got := currentVersion(c.label, c.ref); got != c.want {
			t.Errorf("currentVersion(%q, %q) = %q, attendu %q", c.label, c.ref, got, c.want)
		}
	}
}

func TestMarkNewer(t *testing.T) {
	day := func(d int) int64 { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC).UnixMilli() }
	list := []release{{Tag: "v1.3.0", PublishedAt: day(20)}, {Tag: "v1.2.0", PublishedAt: day(10)}, {Tag: "v1.1.0", PublishedAt: day(1)}}
	newer := func(rs []release) string {
		var out []string
		for _, r := range rs {
			if r.Newer {
				out = append(out, r.Tag)
			}
		}
		return strings.Join(out, ",")
	}
	// Version connue, avec ou sans « v ».
	if got := newer(markNewer(list, "1.2.0", time.Time{})); got != "v1.3.0" {
		t.Errorf("version 1.2.0 : %s", got)
	}
	if got := newer(markNewer(list, "v1.1.0", time.Time{})); got != "v1.3.0,v1.2.0" {
		t.Errorf("version v1.1.0 : %s", got)
	}
	// Version absente de la liste : date de construction de l'image (la release v1.2.0, publiée
	// juste après la construction de son image, n'est pas une nouveauté).
	built := time.UnixMilli(day(10)).Add(-20 * time.Minute)
	if got := newer(markNewer(list, "main", built)); got != "v1.3.0" {
		t.Errorf("d'après la date : %s", got)
	}
	if got := newer(markNewer(list, "", time.Time{})); got != "" {
		t.Errorf("sans repère : %s", got)
	}
	if list[0].Newer {
		t.Error("la liste en cache a été modifiée")
	}
}

func TestReleaseCache(t *testing.T) {
	var calls atomic.Int32
	status := http.StatusOK
	long := strings.Repeat("é", maxBody+50)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/repos/owner/repo/releases" || r.URL.Query().Get("per_page") != "10" {
			t.Errorf("requête %s", r.URL)
		}
		if r.Header.Get("User-Agent") != "vigie-agent/0.4.0" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("en-têtes %v", r.Header)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			fmt.Fprintf(w, `[
				{"tag_name": "v2.0.0", "name": "Brouillon", "draft": true, "published_at": null, "body": "x"},
				{"tag_name": "v1.3.0", "name": "Comet 1.3", "html_url": "https://github.com/owner/repo/releases/tag/v1.3.0",
				 "published_at": "2026-10-20T08:00:00Z", "body": "## Nouveautés\r\n<!-- interne -->\r\n- **Rapide**"},
				{"tag_name": "v1.2.0", "published_at": "2026-10-10T08:00:00Z", "body": %q}
			]`, long)
		}
	}))
	defer srv.Close()

	c := newReleaseCache(srv.URL)
	list, err := c.get(t.Context(), "owner/repo", "0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Tag != "v1.3.0" || list[0].URL == "" || list[0].PublishedAt != time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("releases = %+v", list)
	}
	if list[0].Body != "## Nouveautés\n\n- **Rapide**" {
		t.Errorf("corps = %q", list[0].Body)
	}
	if n := utf8.RuneCountInString(list[1].Body); n != maxBody || !strings.HasSuffix(list[1].Body, "…") {
		t.Errorf("corps tronqué à %d caractères", n)
	}
	// Deuxième demande (casse différente) : servie par le cache.
	if _, err := c.get(t.Context(), "Owner/Repo", "0.4.0"); err != nil || calls.Load() != 1 {
		t.Fatalf("appels = %d, err = %v", calls.Load(), err)
	}

	for code, want := range map[int]error{http.StatusForbidden: errRateLimited, http.StatusTooManyRequests: errRateLimited} {
		status = code
		c := newReleaseCache(srv.URL)
		if _, err := c.get(t.Context(), "owner/repo", "0.4.0"); !errors.Is(err, want) {
			t.Errorf("code %d : %v", code, err)
		}
	}
	status = http.StatusNotFound
	if list, err := newReleaseCache(srv.URL).get(t.Context(), "owner/repo", "0.4.0"); err != nil || len(list) != 0 {
		t.Errorf("404 : %v, %v", list, err)
	}
}
