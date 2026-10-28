package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Une base créée par l'agent 0.3 garde ses checks, qui deviennent des checks HTTP.
func TestMigrateChecksFrom03(t *testing.T) {
	dir := t.TempDir()
	old, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "vigie.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
CREATE TABLE checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, url TEXT NOT NULL,
  expected_status INTEGER NOT NULL DEFAULT 200, keyword TEXT NOT NULL DEFAULT '',
  interval_sec INTEGER NOT NULL DEFAULT 60, timeout_ms INTEGER NOT NULL DEFAULT 10000,
  enabled INTEGER NOT NULL DEFAULT 1, position INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
);
CREATE TABLE check_results (
  check_id INTEGER NOT NULL, ts INTEGER NOT NULL, ok INTEGER NOT NULL, status INTEGER, latency_ms INTEGER, error TEXT,
  cert_expires INTEGER, PRIMARY KEY (check_id, ts)
);
INSERT INTO checks (name, url, created_at) VALUES ('Comet', 'https://comet.example.com/manifest.json', 1);
INSERT INTO check_results (check_id, ts, ok, status, latency_ms) VALUES (1, 1000, 1, 200, 80);`)
	old.Close()
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	c, err := st.Check(ctx, 1)
	if err != nil || c.Kind != "http" || c.StremioID != "" {
		t.Fatalf("check migré %+v (%v)", c, err)
	}
	c.Kind, c.StremioID = "stremio", "tt0111161"
	if err := st.UpdateCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	if err := st.AddResult(ctx, CheckResult{CheckID: 1, TS: 2000, OK: true, Status: 200, Links: 12}); err != nil {
		t.Fatal(err)
	}
	c, _ = st.Check(ctx, 1)
	res, err := st.RecentResults(ctx, 1, 10)
	if err != nil || c.Kind != "stremio" || len(res) != 2 || res[0].Links != 0 || res[1].Links != 12 {
		t.Fatalf("check %+v, résultats %+v (%v)", c, res, err)
	}
}
