package store

// Failles connues : inventaire des paquets par image (une lecture par ID d'image) et fiches OSV en cache.

import (
	"context"
	"strings"
)

// VulnInventory renvoie l'inventaire enregistré d'une image (JSON), « » s'il n'y en a pas.
func (s *Store) VulnInventory(ctx context.Context, imageID string) string {
	var v string
	_ = s.db.QueryRowContext(ctx, `SELECT data FROM vuln_inventory WHERE image_id = ?`, imageID).Scan(&v)
	return v
}

func (s *Store) SetVulnInventory(ctx context.Context, imageID, data string, readAt int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO vuln_inventory (image_id, data, read_at) VALUES (?, ?, ?)
		ON CONFLICT(image_id) DO UPDATE SET data = excluded.data, read_at = excluded.read_at`, imageID, data, readAt)
	return err
}

// PruneVulnInventories supprime les inventaires des images qui ne sont plus utilisées.
func (s *Store) PruneVulnInventories(ctx context.Context, keep []string) error {
	if len(keep) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM vuln_inventory`)
		return err
	}
	args := make([]any, len(keep))
	for i, k := range keep {
		args[i] = k
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM vuln_inventory WHERE image_id NOT IN (?`+strings.Repeat(",?", len(keep)-1)+`)`, args...)
	return err
}

// VulnRecord est une fiche OSV en cache.
type VulnRecord struct {
	ID        string
	Modified  string
	Data      string
	FetchedAt int64
}

// VulnRecords renvoie les fiches en cache parmi ids.
func (s *Store) VulnRecords(ctx context.Context, ids []string) (map[string]VulnRecord, error) {
	out := map[string]VulnRecord{}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, modified, data, fetched_at FROM vuln_records WHERE id IN (?`+
			strings.Repeat(",?", len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r VulnRecord
			if err := rows.Scan(&r.ID, &r.Modified, &r.Data, &r.FetchedAt); err != nil {
				rows.Close()
				return nil, err
			}
			out[r.ID] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetVulnRecords enregistre des fiches en une transaction.
func (s *Store) SetVulnRecords(ctx context.Context, list []VulnRecord) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO vuln_records (id, modified, data, fetched_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET modified = excluded.modified, data = excluded.data, fetched_at = excluded.fetched_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range list {
		if _, err := stmt.ExecContext(ctx, r.ID, r.Modified, r.Data, r.FetchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneVulnRecords supprime les fiches récupérées avant before (ms).
func (s *Store) PruneVulnRecords(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vuln_records WHERE fetched_at < ?`, before)
	return err
}
