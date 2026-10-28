package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Check struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	ExpectedStatus int    `json:"expected_status"`
	Keyword        string `json:"keyword"`
	IntervalSec    int    `json:"interval_sec"`
	TimeoutMS      int    `json:"timeout_ms"`
	Enabled        bool   `json:"enabled"`
	Position       int    `json:"position"`
	CreatedAt      int64  `json:"created_at"`
	// Kind vaut « http » (page web) ou « stremio » (addon testé avec une vraie recherche de liens).
	Kind      string `json:"kind"`
	StremioID string `json:"stremio_id"` // contenu de test (IMDb) du mode stremio
}

type CheckResult struct {
	CheckID   int64  `json:"-"`
	TS        int64  `json:"ts"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	// Expiration du certificat TLS (ms Unix), 0 si non HTTPS.
	CertExpires int64 `json:"cert_expires,omitempty"`
	// Nombre de liens renvoyés par l'addon (mode stremio).
	Links int `json:"links,omitempty"`
}

var ErrNoRows = sql.ErrNoRows

const checkCols = `id, name, url, expected_status, keyword, interval_sec, timeout_ms, enabled, position, created_at, kind, stremio_id`

func scanCheck(sc interface{ Scan(...any) error }) (Check, error) {
	var c Check
	err := sc.Scan(&c.ID, &c.Name, &c.URL, &c.ExpectedStatus, &c.Keyword, &c.IntervalSec, &c.TimeoutMS, &c.Enabled, &c.Position, &c.CreatedAt, &c.Kind, &c.StremioID)
	return c, err
}

func (s *Store) Checks(ctx context.Context) ([]Check, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+checkCols+` FROM checks ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Check(ctx context.Context, id int64) (Check, error) {
	return scanCheck(s.db.QueryRowContext(ctx, `SELECT `+checkCols+` FROM checks WHERE id = ?`, id))
}

func (s *Store) CheckByName(ctx context.Context, name string) (Check, error) {
	return scanCheck(s.db.QueryRowContext(ctx, `SELECT `+checkCols+` FROM checks WHERE name = ?`, name))
}

func (s *Store) CreateCheck(ctx context.Context, c *Check) error {
	c.CreatedAt = time.Now().UnixMilli()
	if c.Position == 0 {
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM checks`).Scan(&c.Position)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO checks (name, url, expected_status, keyword, interval_sec, timeout_ms, enabled, position, created_at, kind, stremio_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.Name, c.URL, c.ExpectedStatus, c.Keyword, c.IntervalSec, c.TimeoutMS, c.Enabled, c.Position, c.CreatedAt, c.Kind, c.StremioID)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateCheck(ctx context.Context, c *Check) error {
	res, err := s.db.ExecContext(ctx, `UPDATE checks SET name = ?, url = ?, expected_status = ?, keyword = ?, interval_sec = ?, timeout_ms = ?, enabled = ?, position = ?,
		kind = ?, stremio_id = ? WHERE id = ?`, c.Name, c.URL, c.ExpectedStatus, c.Keyword, c.IntervalSec, c.TimeoutMS, c.Enabled, c.Position, c.Kind, c.StremioID, c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoRows
	}
	return nil
}

func (s *Store) DeleteCheck(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoRows
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM check_results WHERE check_id = ?`, id)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM check_daily WHERE check_id = ?`, id)
	return nil
}

func (s *Store) AddResult(ctx context.Context, r CheckResult) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO check_results (check_id, ts, ok, status, latency_ms, error, cert_expires, links) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.CheckID, r.TS, r.OK, r.Status, r.LatencyMS, r.Error, r.CertExpires, r.Links)
	if err != nil {
		return err
	}
	day := time.UnixMilli(r.TS).Truncate(24 * time.Hour).UnixMilli()
	okInt := 0
	if r.OK {
		okInt = 1
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO check_daily (check_id, day, total, ok, latency) VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(check_id, day) DO UPDATE SET total = total + 1, ok = ok + excluded.ok,
		latency = (COALESCE(latency, 0) * total + excluded.latency) / (total + 1)`, r.CheckID, day, okInt, float64(r.LatencyMS))
	return err
}

// RecentResults renvoie les n derniers résultats, du plus ancien au plus récent.
func (s *Store) RecentResults(ctx context.Context, id int64, n int) ([]CheckResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, ok, status, latency_ms, COALESCE(error, ''), COALESCE(cert_expires, 0), COALESCE(links, 0) FROM
		(SELECT * FROM check_results WHERE check_id = ? ORDER BY ts DESC LIMIT ?) ORDER BY ts`, id, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckResult{}
	for rows.Next() {
		r := CheckResult{CheckID: id}
		if err := rows.Scan(&r.TS, &r.OK, &r.Status, &r.LatencyMS, &r.Error, &r.CertExpires, &r.Links); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type UptimeBucket struct {
	TS      int64    `json:"ts"`
	Total   int      `json:"total"`
	OK      int      `json:"ok"`
	Latency *float64 `json:"latency"`
}

// UptimeBuckets découpe la période en n tranches (les tranches vides ont total = 0).
func (s *Store) UptimeBuckets(ctx context.Context, id int64, span time.Duration, n int) ([]UptimeBucket, error) {
	if n < 1 {
		n = 1
	}
	now := time.Now()
	if span > 8*24*time.Hour {
		return s.dailyBuckets(ctx, id, span)
	}
	size := span.Milliseconds() / int64(n)
	start := now.UnixMilli() - size*int64(n)
	out := make([]UptimeBucket, n)
	for i := range out {
		out[i].TS = start + int64(i)*size
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (ts - ?) / ? AS b, COUNT(*), SUM(ok), AVG(CASE WHEN ok = 1 THEN latency_ms END)
		FROM check_results WHERE check_id = ? AND ts >= ? GROUP BY b`, start, size, id, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b int64
		var total, ok int
		var lat sql.NullFloat64
		if err := rows.Scan(&b, &total, &ok, &lat); err != nil {
			return nil, err
		}
		if b >= 0 && b < int64(n) {
			out[b].Total, out[b].OK = total, ok
			if lat.Valid {
				v := round1(lat.Float64)
				out[b].Latency = &v
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) dailyBuckets(ctx context.Context, id int64, span time.Duration) ([]UptimeBucket, error) {
	days := int(span / (24 * time.Hour))
	today := time.Now().Truncate(24 * time.Hour)
	out := make([]UptimeBucket, days)
	index := map[int64]int{}
	for i := range out {
		out[i].TS = today.Add(-time.Duration(days-1-i) * 24 * time.Hour).UnixMilli()
		index[out[i].TS] = i
	}
	rows, err := s.db.QueryContext(ctx, `SELECT day, total, ok, latency FROM check_daily WHERE check_id = ? AND day >= ?`, id, out[0].TS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day int64
		var total, ok int
		var lat sql.NullFloat64
		if err := rows.Scan(&day, &total, &ok, &lat); err != nil {
			return nil, err
		}
		if i, found := index[day]; found {
			out[i].Total, out[i].OK = total, ok
			if lat.Valid {
				v := round1(lat.Float64)
				out[i].Latency = &v
			}
		}
	}
	return out, rows.Err()
}

// Uptime renvoie le pourcentage de disponibilité et la latence moyenne depuis since.
func (s *Store) Uptime(ctx context.Context, id int64, since time.Time) (uptime float64, latency float64, err error) {
	var total, ok sql.NullInt64
	var lat sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(ok), AVG(CASE WHEN ok = 1 THEN latency_ms END) FROM check_results WHERE check_id = ? AND ts >= ?`,
		id, since.UnixMilli()).Scan(&total, &ok, &lat)
	if err != nil || total.Int64 == 0 {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return -1, 0, err
	}
	return round2(100 * float64(ok.Int64) / float64(total.Int64)), round1(lat.Float64), nil
}
