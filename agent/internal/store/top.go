package store

import (
	"context"
	"time"
)

// Consumer : consommation d'un conteneur sur une période.
type Consumer struct {
	Name    string  `json:"name"`
	CPUAvg  float64 `json:"cpu_avg"` // % de la capacité de l'hôte
	CPUMax  float64 `json:"cpu_max"`
	MemAvg  int64   `json:"mem_avg"` // octets
	RxBytes int64   `json:"rx_bytes"`
	TxBytes int64   `json:"tx_bytes"`
}

// Top calcule la consommation de chaque conteneur sur la période, du plus gourmand en CPU au moins gourmand.
// Les volumes réseau sont les débits moyens multipliés par la durée de chaque échantillon (1 min ou 15 min).
func (s *Store) Top(ctx context.Context, r Range) ([]Consumer, error) {
	table, cpuMax, step := "ctr_minute", "max(cpu)", int64(60)
	if r.Agg {
		table, cpuMax, step = "ctr_agg", "max(cpu_max)", aggBucket/1000
	}
	from := time.Now().Add(-r.Span).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT name, avg(cpu), `+cpuMax+`, avg(mem_bytes), COALESCE(sum(rx), 0) * ?, COALESCE(sum(tx), 0) * ?
		FROM `+table+` WHERE ts >= ? GROUP BY name ORDER BY 2 DESC, name`, step, step, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Consumer{}
	for rows.Next() {
		var c Consumer
		var mem, rx, tx float64
		if err := rows.Scan(&c.Name, &c.CPUAvg, &c.CPUMax, &mem, &rx, &tx); err != nil {
			return nil, err
		}
		c.CPUAvg, c.CPUMax = round2(c.CPUAvg), round2(c.CPUMax)
		c.MemAvg, c.RxBytes, c.TxBytes = int64(mem), int64(rx), int64(tx)
		out = append(out, c)
	}
	return out, rows.Err()
}
