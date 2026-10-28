package alerts

import (
	"context"
	"testing"

	"github.com/karelisio/vigie/agent/internal/logwatch"
)

func TestNotifyLog(t *testing.T) {
	e, st := newEngine(t)
	e.NotifyLog(logwatch.Hit{RuleID: "r1", Container: "comet", ContainerID: "abc", Pattern: "rate limit", Line: "429 rate limit"})
	e.NotifyLog(logwatch.Hit{RuleID: "r1", Container: "comet", ContainerID: "abc", Pattern: "rate limit", Line: "429 rate limit", Others: 4})
	list, _ := st.Alerts(context.Background(), 10)
	if len(list) != 2 {
		t.Fatalf("2 alertes attendues, obtenu %+v", list)
	}
	a := list[1]
	if a.Kind != "log" || a.Severity != "warning" || a.Target != "comet" || a.Title != "comet : « rate limit »" || a.Body != "429 rate limit" || a.Resolved {
		t.Errorf("alerte %+v", a)
	}
	if list[0].Body != "429 rate limit\n+4 autres occurrences" {
		t.Errorf("corps %q", list[0].Body)
	}
}
