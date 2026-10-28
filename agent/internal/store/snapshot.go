package store

import (
	"context"
	"database/sql"
)

// Snapshot écrit dans path une copie cohérente de la base (VACUUM INTO, sans bloquer les écritures
// en cours), d'où sont effacées les clés kv indiquées : les secrets ne quittent pas l'agent.
func (s *Store) Snapshot(ctx context.Context, path string, without ...string) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return err
	}
	// secure_delete : les valeurs supprimées sont réécrites avec des zéros, pas seulement libérées.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=secure_delete(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	for _, k := range without {
		if _, err := db.ExecContext(ctx, `DELETE FROM kv WHERE k = ?`, k); err != nil {
			return err
		}
	}
	return db.Close()
}
