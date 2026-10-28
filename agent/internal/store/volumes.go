package store

import (
	"context"
	"time"
)

// VolumeRetention : durée de conservation de l'historique des volumes.
const VolumeRetention = 30 * 24 * time.Hour

// SizePoint est une mesure de taille (octets) à un instant (ms).
type SizePoint struct {
	TS   int64 `json:"ts"`
	Size int64 `json:"size"`
}

// InsertVolumeSizes enregistre la taille de chaque volume mesurée à l'instant ts (ms).
func (s *Store) InsertVolumeSizes(ctx context.Context, ts int64, sizes map[string]int64) error {
	if len(sizes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO volume_sizes (name, ts, size) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for name, size := range sizes {
		if _, err := st.ExecContext(ctx, name, ts, size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// VolumeHistory renvoie les mesures depuis since, par volume, de la plus ancienne à la plus récente.
func (s *Store) VolumeHistory(ctx context.Context, since time.Time) (map[string][]SizePoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, ts, size FROM volume_sizes WHERE ts >= ? ORDER BY name, ts`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SizePoint{}
	for rows.Next() {
		var name string
		var p SizePoint
		if err := rows.Scan(&name, &p.TS, &p.Size); err != nil {
			return nil, err
		}
		out[name] = append(out[name], p)
	}
	return out, rows.Err()
}

// PruneVolumeSizes supprime les mesures antérieures à la durée de conservation.
func (s *Store) PruneVolumeSizes(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM volume_sizes WHERE ts < ?`, now.Add(-VolumeRetention).UnixMilli())
	return err
}
