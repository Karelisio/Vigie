package storage

import (
	"math"
	"sort"
	"time"
)

// Prévision de l'occupation du disque : pente robuste (Theil-Sen, médiane des pentes entre tous les
// couples de points horaires) sur les 7 derniers jours, après avoir retiré les nettoyages ponctuels.
const (
	forecastWindow = 7 * 24 * time.Hour
	minSpan        = 48 * time.Hour // recul minimum avant de conclure
	minHours       = 24             // points horaires minimum
	dropRun        = time.Hour      // un nettoyage fait baisser l'occupation en moins d'une heure…
	dropBefore     = 6 * time.Hour  // …par rapport au niveau des 6 heures précédentes…
	dropAfter      = 6              // …et le niveau reste bas ensuite (6 mesures de 15 min)
	riseLookback   = 48 * time.Hour // une baisse qui défait une hausse brutale récente n'est pas un nettoyage
	maxCleanups    = 2              // au-delà, les baisses font partie du rythme normal du serveur
)

// États de la prévision.
const (
	StatusOK           = "ok"           // croissance nette : jours avant d'être plein
	StatusStable       = "stable"       // pas de croissance significative (ou baisse)
	StatusInsufficient = "insufficient" // pas assez de recul pour conclure
)

// Sample est une mesure de l'occupation (octets) à un instant (ms).
type Sample struct {
	TS   int64
	Used float64
}

// Trend est la tendance estimée.
type Trend struct {
	Status   string
	PerDay   float64 // octets par jour (0 sans assez de recul)
	Hours    float64 // recul couvert par les mesures
	Cleanups int     // nettoyages ponctuels écartés du calcul
}

type xy struct{ x, y float64 } // x en heures depuis le premier point

// Estimate calcule la tendance d'une série de mesures triée ou non ; total est la taille du disque.
func Estimate(samples []Sample, total float64) Trend {
	if len(samples) < 2 || total <= 0 {
		return Trend{Status: StatusInsufficient}
	}
	s := append([]Sample(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i].TS < s[j].TS })
	span := time.Duration(s[len(s)-1].TS-s[0].TS) * time.Millisecond
	t := Trend{Status: StatusInsufficient, Hours: math.Round(span.Hours()*10) / 10}
	if span < minSpan {
		return t
	}
	// Une baisse franche : au moins 512 Mio ou 0,5 % du disque (la mesure est au dixième de pour cent).
	adj, n := removeCleanups(s, math.Max(512<<20, 0.005*total))
	pts := hourly(adj)
	if len(pts) < minHours {
		return t
	}
	t.Cleanups = n
	slope := theilSen(pts) // octets par heure
	t.PerDay = slope * 24
	// Bruit autour de la droite (écart absolu médian) : la croissance doit nettement le dépasser.
	res := make([]float64, len(pts))
	for i, p := range pts {
		res[i] = p.y - slope*p.x
	}
	mid := median(res)
	for i := range res {
		res[i] = math.Abs(res[i] - mid)
	}
	noise := 1.4826 * median(res)
	change := slope * (pts[len(pts)-1].x - pts[0].x)
	if slope > 0 && change >= math.Max(256<<20, 0.002*total) && change >= 2*noise {
		t.Status = StatusOK
	} else {
		t.Status = StatusStable
	}
	return t
}

// DaysLeft : jours avant d'être plein au rythme perDay (octets/jour), à partir de l'espace libre.
func DaysLeft(free, perDay float64) float64 {
	if perDay <= 0 {
		return math.Inf(1)
	}
	return math.Max(0, free) / perDay
}

// removeCleanups neutralise les baisses franches et durables (nettoyage, suppression d'images) : les mesures
// suivantes sont relevées d'autant, pour que la croissance d'avant et d'après se raccordent. Une hausse
// suivie d'une baisse (fichier temporaire de quelques heures) n'est pas un nettoyage : rien n'est retiré.
func removeCleanups(s []Sample, thr float64) ([]Sample, int) {
	type drop struct{ from, to int } // dernier point avant la baisse, point le plus bas
	var drops []drop
	for i := 1; i < len(s); {
		if s[i].Used >= s[i-1].Used {
			i++
			continue
		}
		from, to := i-1, i
		for to+1 < len(s) && s[to+1].Used < s[to].Used && s[to+1].TS-s[from].TS <= dropRun.Milliseconds() {
			to++
		}
		amount := s[from].Used - s[to].Used
		if amount >= thr && levelBefore(s, from)-levelAfter(s, to) >= thr/2 && !suddenRise(s, from, amount/2) {
			drops = append(drops, drop{from, to})
		}
		i = to + 1
	}
	if len(drops) == 0 || len(drops) > maxCleanups {
		return s, 0
	}
	out := make([]Sample, len(s))
	copy(out, s)
	shift, k := 0.0, 0
	for i := range out {
		if k < len(drops) && i > drops[k].from {
			if i < drops[k].to { // pendant la baisse : niveau d'avant
				out[i].Used = s[drops[k].from].Used + shift
				continue
			}
			shift += s[drops[k].from].Used - s[drops[k].to].Used
			k++
		}
		out[i].Used = s[i].Used + shift
	}
	return out, len(drops)
}

// suddenRise : une hausse d'au moins amount en moins d'une heure dans les 48 h précédant idx. La baisse
// qui suit défait alors cette hausse (fichier temporaire, image remplacée) : ce n'est pas un nettoyage.
func suddenRise(s []Sample, idx int, amount float64) bool {
	for j := idx; j > 0 && s[idx].TS-s[j].TS <= riseLookback.Milliseconds(); j-- {
		for k := j - 1; k >= 0 && s[j].TS-s[k].TS <= dropRun.Milliseconds(); k-- {
			if s[j].Used-s[k].Used >= amount {
				return true
			}
		}
	}
	return false
}

func levelBefore(s []Sample, idx int) float64 {
	var v []float64
	for i := idx; i >= 0 && s[idx].TS-s[i].TS <= dropBefore.Milliseconds(); i-- {
		v = append(v, s[i].Used)
	}
	return median(v)
}

func levelAfter(s []Sample, idx int) float64 {
	var v []float64
	for i := idx; i < len(s) && i < idx+dropAfter; i++ {
		v = append(v, s[i].Used)
	}
	return median(v)
}

// hourly réduit la série à un point par heure (médiane), x en heures depuis la première heure.
func hourly(s []Sample) []xy {
	const h = int64(time.Hour / time.Millisecond)
	var out []xy
	var cur []float64
	b0, b := s[0].TS/h, s[0].TS/h
	flush := func() {
		if len(cur) > 0 {
			out = append(out, xy{float64(b-b0) + 0.5, median(cur)})
		}
		cur = cur[:0]
	}
	for _, p := range s {
		if p.TS/h != b {
			flush()
			b = p.TS / h
		}
		cur = append(cur, p.Used)
	}
	flush()
	return out
}

// Points réduit la série à un point par heure (médiane) pour le graphique.
func Points(s []Sample) []Point {
	if len(s) == 0 {
		return []Point{}
	}
	const h = int64(time.Hour / time.Millisecond)
	sorted := append([]Sample(nil), s...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS < sorted[j].TS })
	b0 := sorted[0].TS / h
	out := []Point{}
	for _, p := range hourly(sorted) {
		out = append(out, Point{TS: (b0 + int64(p.x)) * h, Used: int64(p.y)})
	}
	return out
}

func theilSen(p []xy) float64 {
	slopes := make([]float64, 0, len(p)*(len(p)-1)/2)
	for i := range p {
		for j := i + 1; j < len(p); j++ {
			if dx := p[j].x - p[i].x; dx > 0 {
				slopes = append(slopes, (p[j].y-p[i].y)/dx)
			}
		}
	}
	return median(slopes)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}
