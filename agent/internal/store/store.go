// Package store gère la base SQLite : historique, checks, appareils, alertes et journal.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS sys_minute (
  ts INTEGER PRIMARY KEY, cpu REAL, mem REAL, disk REAL, rx REAL, tx REAL, load1 REAL, temp REAL
);
CREATE TABLE IF NOT EXISTS sys_agg (
  ts INTEGER PRIMARY KEY, cpu REAL, cpu_max REAL, mem REAL, mem_max REAL, disk REAL, rx REAL, tx REAL, load1 REAL, temp REAL
);
CREATE TABLE IF NOT EXISTS ctr_minute (
  name TEXT NOT NULL, ts INTEGER NOT NULL, cpu REAL, mem REAL, mem_bytes INTEGER, PRIMARY KEY (name, ts)
);
CREATE TABLE IF NOT EXISTS ctr_agg (
  name TEXT NOT NULL, ts INTEGER NOT NULL, cpu REAL, cpu_max REAL, mem REAL, mem_bytes INTEGER, PRIMARY KEY (name, ts)
);
CREATE TABLE IF NOT EXISTS checks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  expected_status INTEGER NOT NULL DEFAULT 200,
  keyword TEXT NOT NULL DEFAULT '',
  interval_sec INTEGER NOT NULL DEFAULT 60,
  timeout_ms INTEGER NOT NULL DEFAULT 10000,
  enabled INTEGER NOT NULL DEFAULT 1,
  position INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS check_results (
  check_id INTEGER NOT NULL, ts INTEGER NOT NULL, ok INTEGER NOT NULL, status INTEGER, latency_ms INTEGER, error TEXT,
  PRIMARY KEY (check_id, ts)
);
CREATE TABLE IF NOT EXISTS check_daily (
  check_id INTEGER NOT NULL, day INTEGER NOT NULL, total INTEGER NOT NULL, ok INTEGER NOT NULL, latency REAL,
  PRIMARY KEY (check_id, day)
);
CREATE TABLE IF NOT EXISTS devices (
  token TEXT PRIMARY KEY, name TEXT, created_at INTEGER, last_seen INTEGER
);
CREATE TABLE IF NOT EXISTS alerts (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT, severity TEXT, target TEXT,
  title TEXT, body TEXT, resolved INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS alerts_ts ON alerts (ts);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, ip TEXT, action TEXT, target TEXT, result TEXT, ua TEXT
);
CREATE INDEX IF NOT EXISTS audit_ts ON audit (ts);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT);
`

func Open(dir string) (*Store, error) {
	dsn := "file:" + filepath.Join(dir, "vigie.db") +
		"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schéma SQLite : %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Get(ctx context.Context, k string) string {
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT v FROM kv WHERE k = ?`, k).Scan(&v)
	return v
}

func (s *Store) Set(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// ---- Journal des actions ----

type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Target string `json:"target"`
	Result string `json:"result"`
	UA     string `json:"ua"`
}

func (s *Store) Audit(ctx context.Context, ip, action, target, result, ua string) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO audit (ts, ip, action, target, result, ua) VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().UnixMilli(), ip, action, target, result, ua)
}

func (s *Store) AuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, ip, action, target, result, ua FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.IP, &e.Action, &e.Target, &e.Result, &e.UA); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Alertes ----

type Alert struct {
	ID       int64  `json:"id"`
	TS       int64  `json:"ts"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Target   string `json:"target"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Resolved bool   `json:"resolved"`
}

func (s *Store) AddAlert(ctx context.Context, a *Alert) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO alerts (ts, kind, severity, target, title, body, resolved) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.TS, a.Kind, a.Severity, a.Target, a.Title, a.Body, a.Resolved)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) Alerts(ctx context.Context, limit int) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, kind, severity, target, title, body, resolved FROM alerts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.TS, &a.Kind, &a.Severity, &a.Target, &a.Title, &a.Body, &a.Resolved); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- Appareils (jetons FCM) ----

type Device struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
}

func (s *Store) UpsertDevice(ctx context.Context, token, name string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO devices (token, name, created_at, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT(token) DO UPDATE SET name = excluded.name, last_seen = excluded.last_seen`, token, name, now, now)
	return err
}

func (s *Store) DeleteDevice(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE token = ?`, token)
	return err
}

func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token, name, created_at, last_seen FROM devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.Token, &d.Name, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Cleanup applique les durées de rétention.
func (s *Store) Cleanup(ctx context.Context) error {
	now := time.Now()
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	day := 24 * time.Hour
	stmts := []struct {
		q   string
		arg int64
	}{
		{`DELETE FROM sys_minute WHERE ts < ?`, ms(26 * time.Hour)},
		{`DELETE FROM ctr_minute WHERE ts < ?`, ms(26 * time.Hour)},
		{`DELETE FROM sys_agg WHERE ts < ?`, ms(8 * day)},
		{`DELETE FROM ctr_agg WHERE ts < ?`, ms(8 * day)},
		{`DELETE FROM check_results WHERE ts < ?`, ms(8 * day)},
		{`DELETE FROM check_daily WHERE day < ?`, ms(91 * day)},
		{`DELETE FROM alerts WHERE ts < ?`, ms(30 * day)},
		{`DELETE FROM audit WHERE ts < ?`, ms(90 * day)},
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st.q, st.arg); err != nil {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM check_results WHERE check_id NOT IN (SELECT id FROM checks)`)
	return err
}
