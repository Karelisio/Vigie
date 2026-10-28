package cleanup

import (
	"encoding/json"
	"testing"

	"github.com/karelisio/vigie/agent/internal/docker"
)

func TestAmongCountsKeptImages(t *testing.T) {
	var du docker.DiskUsage
	_ = json.Unmarshal([]byte(`{"Images": [
		{"Id": "sha256:old", "Size": 500, "SharedSize": 100, "Containers": 0},
		{"Id": "sha256:used", "Size": 900, "SharedSize": 0, "Containers": 1},
		{"Id": "sha256:junk", "Size": 300, "SharedSize": 0, "Containers": 0}
	]}`), &du)
	// Une image gardée mais encore utilisée n'est pas supprimée par le nettoyage : pas comptée.
	if got := Among(&du, []string{"sha256:old", "sha256:used", "sha256:old", "sha256:absente"}); got != (Category{1, 400}) {
		t.Errorf("images gardées = %+v", got)
	}
	if got := Among(&du, nil); got != (Category{}) {
		t.Errorf("sans point de retour = %+v", got)
	}
}
