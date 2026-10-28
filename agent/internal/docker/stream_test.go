package docker

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func frame(stream byte, s string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(s)))
	return append(h, s...)
}

func TestDemux(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frame(1, "2026-01-02T03:04:05.000000000Z \x1b[32mINFO\x1b[0m ok\n2026-01-02T03:04:06Z par"))
	buf.Write(frame(1, "tial\n"))
	buf.Write(frame(2, "2026-01-02T03:04:07Z ERROR boom\n"))
	var got []LogLine
	if err := demux(&buf, func(l LogLine) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("3 lignes attendues, %d obtenues : %+v", len(got), got)
	}
	if got[0].Line != "INFO ok" || got[1].Line != "partial" || got[2].Stream != "stderr" {
		t.Fatalf("lignes inattendues : %+v", got)
	}
	if got[0].TS != 1767323045000 {
		t.Fatalf("horodatage inattendu : %d", got[0].TS)
	}
}

func TestHealth(t *testing.T) {
	cases := map[string]string{
		"Up 2 hours (healthy)":       "healthy",
		"Up 1 second (unhealthy)":    "unhealthy",
		"Up 3 s (health: starting)":  "starting",
		"Exited (0) 2 minutes ago":   "",
	}
	for status, want := range cases {
		if got := (Container{Status: status}).Health(); got != want {
			t.Errorf("%q : %q, attendu %q", status, got, want)
		}
	}
}
