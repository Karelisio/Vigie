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
