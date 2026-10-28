package checks

import (
	"testing"

	"github.com/karelisio/vigie/agent/internal/store"
)

func TestValidate(t *testing.T) {
	c := store.Check{Name: " Comet ", URL: "https://comet.example.com/manifest.json", IntervalSec: 5}
	if err := Validate(&c); err != nil {
		t.Fatal(err)
	}
	if c.Name != "Comet" || c.ExpectedStatus != 200 || c.IntervalSec != 15 || c.TimeoutMS != 10000 {
		t.Fatalf("normalisation inattendue : %+v", c)
	}
	for _, u := range []string{"", "ftp://x", "https://", "not a url"} {
		bad := store.Check{Name: "x", URL: u}
		if Validate(&bad) == nil {
			t.Errorf("URL %q acceptée", u)
		}
	}
}

func TestDescribeBody(t *testing.T) {
	cases := []struct {
		ct, body, want string
	}{
		{"text/html; charset=utf-8", "<!DOCTYPE html><html><head><title>AIOMetadata</title></head>", "page HTML « AIOMetadata »"},
		{"", "<html><body>x</body></html>", "page HTML"},
		{"application/json", `{"name":"x"}`, "JSON sans ce mot-clé"},
		{"", "", "vide"},
		{"text/plain", "ok", "text/plain"},
	}
	for _, c := range cases {
		if got := describeBody(c.ct, []byte(c.body)); got != c.want {
			t.Errorf("%q : %q, attendu %q", c.body, got, c.want)
		}
	}
}
