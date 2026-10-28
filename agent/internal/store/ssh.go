package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Connexions SSH : événements lus dans le journal d'authentification de l'hôte (30 jours), cache des pays
// des adresses (30 jours) et adresses depuis lesquelles une connexion a déjà réussi (1 an).
var sshMigrations = []string{
	`CREATE TABLE IF NOT EXISTS ssh_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL, user TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '', method TEXT NOT NULL DEFAULT '', n INTEGER NOT NULL DEFAULT 1, new_ip INTEGER NOT NULL DEFAULT 0
)`,
	`CREATE INDEX IF NOT EXISTS ssh_events_ts ON ssh_events (ts)`,
	`CREATE INDEX IF NOT EXISTS ssh_events_kind_ts ON ssh_events (kind, ts)`,
	`CREATE TABLE IF NOT EXISTS ssh_geo (ip TEXT PRIMARY KEY, country TEXT NOT NULL DEFAULT '', ts INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS ssh_known (ip TEXT PRIMARY KEY, first INTEGER NOT NULL, last INTEGER NOT NULL)`,
}

// Ajoutées à la suite des migrations communes (tables créées si absentes).
func init() { migrations = append(migrations, sshMigrations...) }

// Types d'événements SSH.
const (
	SSHFail     = "fail"     // échec d'authentification d'un utilisateur existant
	SSHInvalid  = "invalid"  // tentative sur un utilisateur inexistant
	SSHAccepted = "accepted" // connexion réussie
	SSHBan      = "ban"      // adresse bannie par fail2ban
)

// SSHEvent est un événement du journal ; Count regroupe les tentatives identiques rapprochées.
type SSHEvent struct {
	TS     int64  `json:"ts"` // ms
	Kind   string `json:"kind"`
	User   string `json:"user,omitempty"`
	IP     string `json:"ip"`
	Method string `json:"method,omitempty"` // publickey, password, keyboard-interactive… ou prison fail2ban
	Count  int    `json:"count"`
	NewIP  bool   `json:"new_ip,omitempty"` // connexion réussie depuis une adresse jamais vue
}

// AddSSHEvents enregistre des événements et, dans la même transaction, les positions de lecture
// (kv) : après un arrêt brutal, rien n'est compté deux fois. Les adresses des connexions réussies
// rejoignent les adresses connues.
func (s *Store) AddSSHEvents(ctx context.Context, events []SSHEvent, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO ssh_events (ts, kind, user, ip, method, n, new_ip) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, e := range events {
		if e.Count < 1 {
			e.Count = 1
		}
		if _, err := ins.ExecContext(ctx, e.TS, e.Kind, e.User, e.IP, e.Method, e.Count, e.NewIP); err != nil {
			return err
		}
		if e.Kind == SSHAccepted && e.IP != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO ssh_known (ip, first, last) VALUES (?, ?, ?)
				ON CONFLICT(ip) DO UPDATE SET last = MAX(last, excluded.last)`, e.IP, e.TS, e.TS); err != nil {
				return err
			}
		}
	}
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearSSHEvents efface les événements (première lecture du journal reprise depuis le début).
func (s *Store) ClearSSHEvents(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ssh_events`)
	return err
}

// SSHKnownIP indique si une connexion a déjà réussi depuis cette adresse.
func (s *Store) SSHKnownIP(ctx context.Context, ip string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ssh_known WHERE ip = ?`, ip).Scan(&n)
	return n > 0, err
}

// SSHStats : compteurs des 24 dernières heures et des 7 derniers jours.
type SSHStats struct {
	Failed24h  int   `json:"failed_24h"`
	Failed7d   int   `json:"failed_7d"`
	Accepted7d int   `json:"accepted_7d"`
	Bans7d     int   `json:"bans_7d"`
	IPs7d      int   `json:"ips_7d"`   // adresses distinctes à l'origine d'échecs
	Users7d    int   `json:"users_7d"` // utilisateurs distincts essayés
	Since      int64 `json:"since"`    // plus ancien événement conservé (ms), 0 sans événement
}

func (s *Store) SSHStats(ctx context.Context, now time.Time) (SSHStats, error) {
	var out SSHStats
	day, week := now.Add(-24*time.Hour).UnixMilli(), now.Add(-7*24*time.Hour).UnixMilli()
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN kind IN ('fail', 'invalid') AND ts >= ? THEN n END), 0),
		COALESCE(SUM(CASE WHEN kind IN ('fail', 'invalid') THEN n END), 0),
		COALESCE(SUM(CASE WHEN kind = 'accepted' THEN n END), 0),
		COALESCE(SUM(CASE WHEN kind = 'ban' THEN n END), 0),
		COUNT(DISTINCT CASE WHEN kind IN ('fail', 'invalid') THEN ip END),
		COUNT(DISTINCT CASE WHEN kind IN ('fail', 'invalid') THEN user END)
		FROM ssh_events WHERE ts >= ?`, day, week).
		Scan(&out.Failed24h, &out.Failed7d, &out.Accepted7d, &out.Bans7d, &out.IPs7d, &out.Users7d)
	if err != nil {
		return out, err
	}
	var since sql.NullInt64
	_ = s.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM ssh_events`).Scan(&since)
	out.Since = since.Int64
	return out, nil
}

// SSHIPCount : adresse à l'origine d'échecs.
type SSHIPCount struct {
	IP       string `json:"ip"`
	Count    int    `json:"count"`
	Last     int64  `json:"last"`
	Banned   bool   `json:"banned"`   // bannie par fail2ban sur la période
	Accepted bool   `json:"accepted"` // une connexion a aussi réussi depuis cette adresse
}

// SSHTopIPs classe les adresses par nombre d'échecs depuis from.
func (s *Store) SSHTopIPs(ctx context.Context, from time.Time, limit int) ([]SSHIPCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip,
		SUM(CASE WHEN kind IN ('fail', 'invalid') THEN n ELSE 0 END) AS c,
		MAX(CASE WHEN kind IN ('fail', 'invalid') THEN ts END),
		MAX(kind = 'ban'), MAX(kind = 'accepted')
		FROM ssh_events WHERE ts >= ? AND ip != '' GROUP BY ip HAVING c > 0 ORDER BY c DESC, 3 DESC LIMIT ?`,
		from.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHIPCount{}
	for rows.Next() {
		var c SSHIPCount
		if err := rows.Scan(&c.IP, &c.Count, &c.Last, &c.Banned, &c.Accepted); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SSHUserCount : utilisateur essayé.
type SSHUserCount struct {
	User    string `json:"user"`
	Count   int    `json:"count"`
	Invalid bool   `json:"invalid"` // inexistant sur le serveur
}

// SSHTopUsers classe les utilisateurs essayés depuis from.
func (s *Store) SSHTopUsers(ctx context.Context, from time.Time, limit int) ([]SSHUserCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user, SUM(n) AS c, MIN(kind = 'invalid')
		FROM ssh_events WHERE ts >= ? AND kind IN ('fail', 'invalid') GROUP BY user ORDER BY c DESC, user LIMIT ?`,
		from.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHUserCount{}
	for rows.Next() {
		var c SSHUserCount
		if err := rows.Scan(&c.User, &c.Count, &c.Invalid); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SSHList renvoie les derniers événements (tous types si kinds est vide), du plus récent au plus ancien.
func (s *Store) SSHList(ctx context.Context, from time.Time, limit int, kinds ...string) ([]SSHEvent, error) {
	q := `SELECT ts, kind, user, ip, method, n, new_ip FROM ssh_events WHERE ts >= ?`
	args := []any{from.UnixMilli()}
	if len(kinds) > 0 {
		q += ` AND kind IN (?` + strings.Repeat(`, ?`, len(kinds)-1) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SSHEvent{}
	for rows.Next() {
		var e SSHEvent
		if err := rows.Scan(&e.TS, &e.Kind, &e.User, &e.IP, &e.Method, &e.Count, &e.NewIP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SSHFailSeries compte les échecs par tranche de bucket sur n tranches finissant à end (exclu).
func (s *Store) SSHFailSeries(ctx context.Context, end time.Time, bucket time.Duration, n int) ([]int, error) {
	from := end.Add(-time.Duration(n) * bucket)
	out := make([]int, n)
	rows, err := s.db.QueryContext(ctx, `SELECT (ts - ?) / ?, SUM(n) FROM ssh_events
		WHERE ts >= ? AND ts < ? AND kind IN ('fail', 'invalid') GROUP BY 1`,
		from.UnixMilli(), bucket.Milliseconds(), from.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i, c int
		if err := rows.Scan(&i, &c); err != nil {
			return nil, err
		}
		if i >= 0 && i < n {
			out[i] = c
		}
	}
	return out, rows.Err()
}

// SSHFailuresFrom compte les échecs d'une adresse depuis from.
func (s *Store) SSHFailuresFrom(ctx context.Context, ip string, from time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(n), 0) FROM ssh_events WHERE ip = ? AND ts >= ? AND kind IN ('fail', 'invalid')`,
		ip, from.UnixMilli()).Scan(&n)
	return n, err
}

// SSHWeek résume une période pour le rapport hebdomadaire.
type SSHWeek struct {
	Failed   int
	Accepted int
	NewIPs   int
	Bans     int
}

func (s *Store) SSHWeek(ctx context.Context, from time.Time) (SSHWeek, error) {
	var w SSHWeek
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN kind IN ('fail', 'invalid') THEN n END), 0),
		COALESCE(SUM(CASE WHEN kind = 'accepted' THEN n END), 0),
		COUNT(DISTINCT CASE WHEN kind = 'accepted' AND new_ip = 1 THEN ip END),
		COALESCE(SUM(CASE WHEN kind = 'ban' THEN n END), 0)
		FROM ssh_events WHERE ts >= ?`, from.UnixMilli()).Scan(&w.Failed, &w.Accepted, &w.NewIPs, &w.Bans)
	return w, err
}

// GeoCountries renvoie les pays en cache (code ISO, "" si inconnu) des adresses demandées : une entrée
// plus vieille que maxAge (ou, pour un pays inconnu, que retryAfter) est ignorée.
func (s *Store) GeoCountries(ctx context.Context, ips []string, now time.Time, maxAge, retryAfter time.Duration) (map[string]string, error) {
	out := map[string]string{}
	for start := 0; start < len(ips); start += 200 {
		chunk := ips[start:min(len(ips), start+200)]
		args := make([]any, 0, len(chunk))
		for _, ip := range chunk {
			args = append(args, ip)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT ip, country, ts FROM ssh_geo WHERE ip IN (?`+strings.Repeat(`, ?`, len(chunk)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ip, c string
			var ts int64
			if err := rows.Scan(&ip, &c, &ts); err != nil {
				rows.Close()
				return nil, err
			}
			age := now.Sub(time.UnixMilli(ts))
			if (c != "" && age < maxAge) || (c == "" && age < retryAfter) {
				out[ip] = c
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PutGeo met en cache des pays (code ISO, "" si le service ne le connaît pas).
func (s *Store) PutGeo(ctx context.Context, countries map[string]string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for ip, c := range countries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ssh_geo (ip, country, ts) VALUES (?, ?, ?)
			ON CONFLICT(ip) DO UPDATE SET country = excluded.country, ts = excluded.ts`, ip, c, now.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneSSH applique les durées de rétention : événements et pays 30 jours, adresses connues 1 an.
func (s *Store) PruneSSH(ctx context.Context, now time.Time) error {
	for _, st := range []struct {
		q   string
		age time.Duration
	}{
		{`DELETE FROM ssh_events WHERE ts < ?`, 30 * 24 * time.Hour},
		{`DELETE FROM ssh_geo WHERE ts < ?`, 30 * 24 * time.Hour},
		{`DELETE FROM ssh_known WHERE last < ?`, 365 * 24 * time.Hour},
	} {
		if _, err := s.db.ExecContext(ctx, st.q, now.Add(-st.age).UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}
