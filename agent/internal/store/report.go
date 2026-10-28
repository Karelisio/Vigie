package store

import (
	"context"
	"time"
)

// ContainerIncidents : plantages et redémarrages d'un conteneur sur une période.
type ContainerIncidents struct {
	Name     string `json:"name"`
	Crashes  int    `json:"crashes"`
	Restarts int    `json:"restarts"`
}

// ContainerIncidents compte depuis since les plantages (arrêt sur un code d'erreur autre qu'un signal d'arrêt,
// ou manque de mémoire) et les redémarrages de chaque conteneur, du plus touché au moins touché.
func (s *Store) ContainerIncidents(ctx context.Context, since time.Time) ([]ContainerIncidents, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,
		SUM(action = 'oom' OR (action = 'die' AND CAST(COALESCE(detail, '0') AS INTEGER) NOT IN (0, 130, 137, 143))) AS crashes,
		SUM(action = 'restart') AS restarts
		FROM events WHERE ts >= ? AND action IN ('die', 'oom', 'restart')
		GROUP BY name HAVING crashes + restarts > 0 ORDER BY crashes DESC, restarts DESC, name`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContainerIncidents{}
	for rows.Next() {
		var c ContainerIncidents
		if err := rows.Scan(&c.Name, &c.Crashes, &c.Restarts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// KindCount : nombre d'incidents d'un type d'alerte.
type KindCount struct {
	Kind  string
	Count int
}

// Incidents : alertes non résolues d'avertissement ou critiques, hors résumé des heures calmes (qui les répète).
const incidentsWhere = `ts >= ? AND resolved = 0 AND severity IN ('warning', 'critical') AND kind != 'summary'`

// IncidentCounts compte les incidents par type depuis since, du plus fréquent au moins fréquent.
func (s *Store) IncidentCounts(ctx context.Context, since time.Time) ([]KindCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM alerts WHERE `+incidentsWhere+`
		GROUP BY kind ORDER BY 2 DESC, kind`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KindCount{}
	for rows.Next() {
		var k KindCount
		if err := rows.Scan(&k.Kind, &k.Count); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Incidents renvoie les derniers incidents depuis since, du plus récent au plus ancien.
func (s *Store) Incidents(ctx context.Context, since time.Time, limit int) ([]Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, kind, severity, target, title, body, resolved FROM alerts
		WHERE `+incidentsWhere+` ORDER BY id DESC LIMIT ?`, since.UnixMilli(), limit)
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
