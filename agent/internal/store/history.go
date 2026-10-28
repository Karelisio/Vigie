package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SysSample struct {
	TS    int64    `json:"ts"`
	CPU   float64  `json:"cpu"`
	Mem   float64  `json:"mem"`
	Disk  float64  `json:"disk"`
	Rx    float64  `json:"rx"`
	Tx    float64  `json:"tx"`
	Load1 float64  `json:"load1"`
	Temp  *float64 `json:"temp,omitempty"`
}

type CtrSample struct {
	Name     string  `json:"-"`
	TS       int64   `json:"ts"`
	CPU      float64 `json:"cpu"`
	Mem      float64 `json:"mem"`
	MemBytes int64   `json:"mem_bytes"`
}

func (s *Store) InsertSys(ctx context.Context, p SysSample) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO sys_minute (ts, cpu, mem, disk, rx, tx, load1, temp) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.TS, p.CPU, p.Mem, p.Disk, p.Rx, p.Tx, p.Load1, p.Temp)
	return err
}

func (s *Store) InsertCtr(ctx context.Context, samples []CtrSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO ctr_minute (name, ts, cpu, mem, mem_bytes) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range samples {
		if _, err := st.ExecContext(ctx, p.Name, p.TS, p.CPU, p.Mem, p.MemBytes); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const aggBucket = int64(15 * 60 * 1000)

// Aggregate condense les échantillons minute en tranches de 15 min sur la période lookback.
func (s *Store) Aggregate(ctx context.Context, lookback time.Duration) error {
	now := time.Now().UnixMilli()
	end := now / aggBucket * aggBucket // tranche en cours exclue
	start := (now - lookback.Milliseconds()) / aggBucket * aggBucket
	if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO sys_agg (ts, cpu, cpu_max, mem, mem_max, disk, rx, tx, load1, temp)
		SELECT (ts / ?) * ?, avg(cpu), max(cpu), avg(mem), max(mem), avg(disk), avg(rx), avg(tx), avg(load1), avg(temp)
		FROM sys_minute WHERE ts >= ? AND ts < ? GROUP BY ts / ?`, aggBucket, aggBucket, start, end, aggBucket); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO ctr_agg (name, ts, cpu, cpu_max, mem, mem_bytes)
		SELECT name, (ts / ?) * ?, avg(cpu), max(cpu), avg(mem), CAST(avg(mem_bytes) AS INTEGER)
		FROM ctr_minute WHERE ts >= ? AND ts < ? GROUP BY name, ts / ?`, aggBucket, aggBucket, start, end, aggBucket)
	return err
}

// Range décrit une période d'historique : durée, table source et taille de tranche.
type Range struct {
	Span   time.Duration
	Bucket time.Duration
	Agg    bool
}

func ParseRange(v string) (Range, error) {
	switch v {
	case "1h":
		return Range{Span: time.Hour, Bucket: time.Minute}, nil
	case "6h":
		return Range{Span: 6 * time.Hour, Bucket: 2 * time.Minute}, nil
	case "", "24h":
		return Range{Span: 24 * time.Hour, Bucket: 5 * time.Minute}, nil
	case "7d":
		return Range{Span: 7 * 24 * time.Hour, Bucket: time.Hour, Agg: true}, nil
	}
	return Range{}, fmt.Errorf("période inconnue %q (1h, 6h, 24h, 7d)", v)
}

func (s *Store) SysHistory(ctx context.Context, r Range) ([]SysSample, error) {
	table := "sys_minute"
	if r.Agg {
		table = "sys_agg"
	}
	b := r.Bucket.Milliseconds()
	from := time.Now().Add(-r.Span).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT (ts / ?) * ? AS b, avg(cpu), avg(mem), avg(disk), avg(rx), avg(tx), avg(load1), avg(temp)
		FROM `+table+` WHERE ts >= ? GROUP BY b ORDER BY b`, b, b, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SysSample{}
	for rows.Next() {
		var p SysSample
		var temp sql.NullFloat64
		if err := rows.Scan(&p.TS, &p.CPU, &p.Mem, &p.Disk, &p.Rx, &p.Tx, &p.Load1, &temp); err != nil {
			return nil, err
		}
		if temp.Valid {
			t := round1(temp.Float64)
			p.Temp = &t
		}
		p.CPU, p.Mem, p.Disk, p.Load1 = round1(p.CPU), round1(p.Mem), round1(p.Disk), round2(p.Load1)
		p.Rx, p.Tx = float64(int64(p.Rx)), float64(int64(p.Tx))
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CtrHistory(ctx context.Context, name string, r Range) ([]CtrSample, error) {
	table := "ctr_minute"
	if r.Agg {
		table = "ctr_agg"
	}
	b := r.Bucket.Milliseconds()
	from := time.Now().Add(-r.Span).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT (ts / ?) * ? AS b, avg(cpu), avg(mem), CAST(avg(mem_bytes) AS INTEGER)
		FROM `+table+` WHERE name = ? AND ts >= ? GROUP BY b ORDER BY b`, b, b, name, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CtrSample{}
	for rows.Next() {
		var p CtrSample
		if err := rows.Scan(&p.TS, &p.CPU, &p.Mem, &p.MemBytes); err != nil {
			return nil, err
		}
		p.CPU, p.Mem = round2(p.CPU), round1(p.Mem)
		out = append(out, p)
	}
	return out, rows.Err()
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// SysWindow renvoie les échantillons agrégés (15 min) depuis since, plus l'instant du plus ancien.
func (s *Store) SysWindow(ctx context.Context, since time.Time) ([]SysSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ts, cpu, mem, disk, rx, tx, load1 FROM sys_agg WHERE ts >= ? ORDER BY ts`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SysSample{}
	for rows.Next() {
		var p SysSample
		if err := rows.Scan(&p.TS, &p.CPU, &p.Mem, &p.Disk, &p.Rx, &p.Tx, &p.Load1); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
