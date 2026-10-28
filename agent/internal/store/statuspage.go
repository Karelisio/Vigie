package store

import (
	"context"
	"database/sql"
	"time"
)

// RunPoint est un résultat de check réduit à son instant et à sa réussite.
type RunPoint struct {
	TS int64
	OK bool
}

// CheckRuns renvoie les résultats d'un check depuis since, du plus ancien au plus récent (8 jours conservés).
func (s *Store) CheckRuns(ctx context.Context, id int64, since time.Time) ([]RunPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, ok FROM check_results WHERE check_id = ? AND ts >= ? ORDER BY ts`, id, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunPoint{}
	for rows.Next() {
		var p RunPoint
		if err := rows.Scan(&p.TS, &p.OK); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DayPresence compte, pour un jour (minuit UTC), les tranches de 15 min mesurées par l'agent (Measured)
// et celles où le conteneur tournait (Running).
type DayPresence struct {
	Measured int
	Running  int
}

// ContainerPresence renvoie la présence quotidienne d'un conteneur depuis since (8 jours d'historique agrégé),
// par jour (ms Unix, minuit UTC). Le décompte commence à sa première mesure en marche : avant, il n'existait
// peut-être pas encore. Un jour sans mesure est absent ; un conteneur jamais vu en marche n'a aucune donnée.
func (s *Store) ContainerPresence(ctx context.Context, name string, since time.Time) (map[int64]DayPresence, error) {
	const day = int64(24 * time.Hour / time.Millisecond)
	out := map[int64]DayPresence{}
	var first sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM ctr_agg WHERE name = ? AND ts >= ?`, name, since.UnixMilli()).Scan(&first); err != nil {
		return nil, err
	}
	if !first.Valid {
		return out, nil
	}
	from := first.Int64
	rows, err := s.db.QueryContext(ctx, `SELECT (ts / ?) * ?, COUNT(*) FROM sys_agg WHERE ts >= ? GROUP BY 1`, day, day, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d int64
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out[d] = DayPresence{Measured: n}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT (ts / ?) * ?, COUNT(*) FROM ctr_agg WHERE name = ? AND ts >= ? GROUP BY 1`, day, day, name, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d int64
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		if p, ok := out[d]; ok {
			p.Running = min(n, p.Measured)
			out[d] = p
		}
	}
	return out, rows.Err()
}
