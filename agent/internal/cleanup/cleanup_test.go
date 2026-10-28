package cleanup

import (
	"encoding/json"
	"testing"

	"github.com/karelisio/vigie/agent/internal/docker"
)

func TestEstimate(t *testing.T) {
	var du docker.DiskUsage
	_ = json.Unmarshal([]byte(`{
		"Images": [
			{"Id": "a", "Size": 500, "SharedSize": 100, "Containers": 0},
			{"Id": "b", "Size": 900, "SharedSize": 0, "Containers": 2},
			{"Id": "c", "Size": 300, "SharedSize": -1, "Containers": 0}
		],
		"Volumes": [
			{"Name": "x", "Labels": {"com.docker.volume.anonymous": ""}, "UsageData": {"Size": 40, "RefCount": 0}},
			{"Name": "data", "Labels": null, "UsageData": {"Size": 9000, "RefCount": 0}},
			{"Name": "y", "Labels": {"com.docker.volume.anonymous": ""}, "UsageData": {"Size": 70, "RefCount": 1}}
		],
		"BuildCache": [{"Size": 10, "InUse": false}, {"Size": 20, "InUse": true}]
	}`), &du)
	p := Estimate(&du, true)
	if p.Images != (Category{2, 700}) {
		t.Errorf("images = %+v", p.Images)
	}
	// Le volume nommé « data » n'est jamais compté, même inutilisé.
	if p.Volumes != (Category{1, 40}) {
		t.Errorf("volumes = %+v", p.Volumes)
	}
	if p.BuildCache != (Category{1, 10}) {
		t.Errorf("build = %+v", p.BuildCache)
	}
	if q := Estimate(&du, false); q.Volumes.Count != 0 {
		t.Error("volumes proposés sur un Docker trop ancien")
	}
}

func TestHumanBytes(t *testing.T) {
	for b, want := range map[int64]string{512: "512 o", 1536: "1,5 Ko", 3 << 30: "3 Go", 13_312_000_000: "12,4 Go"} {
		if got := HumanBytes(b); got != want {
			t.Errorf("HumanBytes(%d) = %q, attendu %q", b, got, want)
		}
	}
}
