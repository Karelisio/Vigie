// Package oracle estime le risque de récupération d'une instance Oracle Cloud « Always Free » inactive.
//
// Oracle peut récupérer une instance si, sur 7 jours : le 95e centile du CPU est sous 20 %,
// l'utilisation réseau est sous 20 % et (formes A1 seulement) la mémoire est sous 20 %.
package oracle

import (
	"context"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	limit  = 20.0
	window = 7 * 24 * time.Hour
)

type Metric struct {
	Value     float64 `json:"value"` // %
	Below     bool    `json:"below"` // sous le seuil de 20 %
	Applies   bool    `json:"applies"`
	Threshold float64 `json:"threshold"`
}

type Report struct {
	Applicable bool    `json:"applicable"`
	Risk       string  `json:"risk"` // low, medium, high, unknown
	Days       float64 `json:"days"` // période couverte par l'historique
	CPU        Metric  `json:"cpu"`
	Mem        Metric  `json:"mem"`
	Net        Metric  `json:"net"`
	NetMbps    float64 `json:"net_mbps"` // bande passante de référence
	Summary    string  `json:"summary"`
}

// Applicable détecte une instance Oracle (noyau « -oracle ») ou VIGIE_ORACLE=true.
func Applicable(kernel string) bool {
	switch strings.ToLower(os.Getenv("VIGIE_ORACLE")) {
	case "true", "1", "oui":
		return true
	case "false", "0", "non":
		return false
	}
	return strings.Contains(strings.ToLower(kernel), "oracle")
}

// capacityMbps : 1 Gbit/s par OCPU sur Ampere A1, 480 Mbit/s sur E2.1.Micro ; VIGIE_NET_MBPS pour forcer.
func capacityMbps(numCPU int) float64 {
	if v, err := strconv.ParseFloat(os.Getenv("VIGIE_NET_MBPS"), 64); err == nil && v > 0 {
		return v
	}
	if runtime.GOARCH == "arm64" {
		return float64(max(1, numCPU)) * 1000
	}
	return 480
}

func p95(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(0.95*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// Evaluate calcule le rapport sur les 7 derniers jours.
func Evaluate(ctx context.Context, st *store.Store, kernel string, numCPU int) (Report, error) {
	r := Report{Applicable: Applicable(kernel), Risk: "unknown"}
	if !r.Applicable {
		return r, nil
	}
	pts, err := st.SysWindow(ctx, time.Now().Add(-window))
	if err != nil {
		return r, err
	}
	r.NetMbps = capacityMbps(numCPU)
	if len(pts) == 0 {
		r.Summary = "Pas encore d'historique."
		return r, nil
	}
	cpu := make([]float64, len(pts))
	mem := make([]float64, len(pts))
	net := make([]float64, len(pts))
	capBps := r.NetMbps * 1e6 / 8
	for i, p := range pts {
		cpu[i], mem[i] = p.CPU, p.Mem
		net[i] = 100 * (p.Rx + p.Tx) / capBps
	}
	r.Days = round1(float64(pts[len(pts)-1].TS-pts[0].TS) / float64(24*time.Hour/time.Millisecond))
	arm := runtime.GOARCH == "arm64"
	r.CPU = Metric{Value: round1(p95(cpu)), Applies: true, Threshold: limit}
	r.Mem = Metric{Value: round1(p95(mem)), Applies: arm, Threshold: limit}
	r.Net = Metric{Value: math.Round(p95(net)*100) / 100, Applies: true, Threshold: limit}
	below, applies := 0, 0
	for _, m := range []*Metric{&r.CPU, &r.Mem, &r.Net} {
		m.Below = m.Value < limit
		if m.Applies {
			applies++
			if m.Below {
				below++
			}
		}
	}
	switch {
	case below == applies:
		r.Risk = "high"
		r.Summary = "Tous les critères d'inactivité d'Oracle sont réunis : l'instance peut être récupérée."
	case below == applies-1:
		r.Risk = "medium"
		r.Summary = "Un seul critère maintient l'instance au-dessus du seuil d'inactivité."
	default:
		r.Risk = "low"
		r.Summary = "L'instance est assez utilisée pour ne pas être considérée inactive."
	}
	if r.Days < 1 {
		r.Summary += " (estimation sur moins d'un jour)"
	}
	return r, nil
}
