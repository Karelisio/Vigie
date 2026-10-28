package store

import (
	"context"
	"time"
)

// AggStep est la durée d'une tranche de l'historique agrégé (sys_agg, ctr_agg).
const AggStep = time.Duration(aggBucket) * time.Millisecond

// TrafficMonth : octets reçus et émis par l'hôte sur un mois (AAAA-MM, heure locale).
type TrafficMonth struct {
	Month string `json:"month"`
	Tx    int64  `json:"tx"`
	Rx    int64  `json:"rx"`
	// Début de la mesure dans le mois (ms), pour extrapoler un mois commencé en cours de route.
	Since int64 `json:"-"`
}

// AddTraffic ajoute des octets au mois ; ts (ms) marque le début de la mesure quand le mois est nouveau.
func (s *Store) AddTraffic(ctx context.Context, month string, rx, tx, ts int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO traffic (month, rx, tx, since) VALUES (?, ?, ?, ?)
		ON CONFLICT(month) DO UPDATE SET rx = rx + excluded.rx, tx = tx + excluded.tx`, month, rx, tx, ts)
	return err
}

// PruneTraffic supprime les mois antérieurs à oldest (AAAA-MM).
func (s *Store) PruneTraffic(ctx context.Context, oldest string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM traffic WHERE month < ?`, oldest)
	return err
}

// TrafficMonths renvoie les n derniers mois enregistrés, du plus ancien au plus récent.
func (s *Store) TrafficMonths(ctx context.Context, n int) ([]TrafficMonth, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT month, rx, tx, since FROM
		(SELECT * FROM traffic ORDER BY month DESC LIMIT ?) ORDER BY month`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrafficMonth{}
	for rows.Next() {
		var m TrafficMonth
		if err := rows.Scan(&m.Month, &m.Rx, &m.Tx, &m.Since); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
