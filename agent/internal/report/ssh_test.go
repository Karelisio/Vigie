package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func TestBuildSSHLine(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Now()
	r, err := Build(ctx, st, Input{}, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if r.SSH != nil || strings.Contains(r.Text(), "SSH") {
		t.Fatalf("pas de ligne SSH sans événement : %+v", r.SSH)
	}
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	if err := st.AddSSHEvents(ctx, []store.SSHEvent{
		{TS: ago(time.Hour), Kind: store.SSHInvalid, User: "admin", IP: "192.0.2.1", Count: 1233},
		{TS: ago(2 * time.Hour), Kind: store.SSHFail, User: "root", IP: "192.0.2.2", Count: 1},
		{TS: ago(3 * time.Hour), Kind: store.SSHAccepted, User: "ubuntu", IP: "203.0.113.7", Count: 1, NewIP: true},
		{TS: ago(4 * time.Hour), Kind: store.SSHAccepted, User: "ubuntu", IP: "203.0.113.7", Count: 1},
		{TS: ago(5 * time.Hour), Kind: store.SSHBan, IP: "192.0.2.1", Count: 1},
		{TS: ago(9 * 24 * time.Hour), Kind: store.SSHFail, User: "root", IP: "192.0.2.9", Count: 50},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if r, err = Build(ctx, st, Input{}, now, 7); err != nil {
		t.Fatal(err)
	}
	want := "SSH : 1234 échecs, 2 connexions (1 depuis une nouvelle adresse), 1 bannissement"
	if !strings.Contains(r.Text(), want) {
		t.Fatalf("rapport :\n%s", r.Text())
	}
}
