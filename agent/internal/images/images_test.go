package images

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Ref{
		"redis:7-alpine":                     {"registry-1.docker.io", "library/redis", "7-alpine"},
		"g0ldyy/comet":                       {"registry-1.docker.io", "g0ldyy/comet", "latest"},
		"ghcr.io/viren070/aiostreams:latest": {"ghcr.io", "viren070/aiostreams", "latest"},
		"lscr.io/linuxserver/prowlarr":       {"lscr.io", "linuxserver/prowlarr", "latest"},
		"docker.io/library/nginx:1.27":       {"registry-1.docker.io", "library/nginx", "1.27"},
		"registry.local:5000/app:v2":         {"registry.local:5000", "app", "v2"},
	}
	for in, want := range cases {
		got, ok := Parse(in)
		if !ok || got != want {
			t.Errorf("%s : %+v (%v), attendu %+v", in, got, ok, want)
		}
	}
	for _, in := range []string{"redis@sha256:abc", "sha256:deadbeef", "localhost:5000/x"} {
		if _, ok := Parse(in); ok {
			t.Errorf("%s ne devrait pas être vérifiable", in)
		}
	}
}

func TestUpdateAvailable(t *testing.T) {
	local := []string{"redis@sha256:aaa"}
	if upd, known := UpdateAvailable("redis:7", "sha256:aaa", local); upd || !known {
		t.Error("même digest : pas de mise à jour attendue")
	}
	if upd, known := UpdateAvailable("redis:7", "sha256:bbb", local); !upd || !known {
		t.Error("digest différent : mise à jour attendue")
	}
	if _, known := UpdateAvailable("ghcr.io/x/y:latest", "sha256:bbb", []string{}); known {
		t.Error("image locale sans digest : état inconnu attendu")
	}
	if upd, known := UpdateAvailable("ghcr.io/x/y:latest", "sha256:ccc", []string{"ghcr.io/x/y@sha256:ccc"}); upd || !known {
		t.Error("GHCR à jour mal détecté")
	}
}
