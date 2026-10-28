// Package traffic compte le trafic réseau mensuel de l'hôte, pour suivre un quota sortant
// (Oracle Always Free : 10 To par mois).
package traffic

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	monthLayout = "2006-01"
	keepMonths  = 13 // mois conservés en base, mois en cours compris
	historyLen  = 12 // mois renvoyés à l'appli
	terabyte    = 1e12
)

// counter convertit les compteurs cumulés de l'hôte (depuis son démarrage) en octets écoulés.
type counter struct {
	rx, tx uint64
	primed bool
}

// next renvoie les octets écoulés depuis le relevé précédent ; ok est faux au premier relevé.
func (c *counter) next(rx, tx uint64) (dRx, dTx uint64, ok bool) {
	ok = c.primed
	if ok {
		dRx, dTx = delta(c.rx, rx), delta(c.tx, tx)
	}
	c.rx, c.tx, c.primed = rx, tx, true
	return dRx, dTx, ok
}

// delta : un compteur qui recule a été remis à zéro (redémarrage de l'hôte, interface réinitialisée) :
// tout ce qu'il indique est nouveau.
func delta(prev, cur uint64) uint64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}

// Meter accumule le trafic de l'hôte, mois par mois, dans la base.
// Il est alimenté par la boucle minute du moniteur (un seul appelant).
type Meter struct {
	st    *store.Store
	c     counter
	month string
}

func NewMeter(st *store.Store) *Meter { return &Meter{st: st} }

// Sample enregistre le trafic écoulé depuis le relevé précédent ; ts (heure locale) date la minute mesurée.
func (m *Meter) Sample(ctx context.Context, ts time.Time, rx, tx uint64) error {
	if rx == 0 && tx == 0 {
		return nil // compteurs illisibles
	}
	dRx, dTx, ok := m.c.next(rx, tx)
	month := ts.Format(monthLayout)
	if month != m.month {
		m.month = month
		oldest := monthStart(ts).AddDate(0, 1-keepMonths, 0).Format(monthLayout)
		if err := m.st.PruneTraffic(ctx, oldest); err != nil {
			return err
		}
	}
	if !ok {
		return nil
	}
	return m.st.AddTraffic(ctx, month, int64(dRx), int64(dTx), ts.UnixMilli())
}

// Report : trafic du mois en cours et historique.
type Report struct {
	Month       string               `json:"month"`
	Tx          int64                `json:"tx"`
	Rx          int64                `json:"rx"`
	Quota       int64                `json:"quota"`        // octets sortants autorisés par mois, 0 sans quota
	Pct         float64              `json:"pct"`          // part du quota consommée
	ProjectedTx int64                `json:"projected_tx"` // sortant prévu à la fin du mois, au rythme actuel
	DaysLeft    float64              `json:"days_left"`
	History     []store.TrafficMonth `json:"history"` // derniers mois, du plus ancien au plus récent
}

// Build calcule le rapport du mois de now (heure locale) pour un quota en To (0 : aucun).
func Build(ctx context.Context, st *store.Store, quotaTB float64, now time.Time) (Report, error) {
	hist, err := st.TrafficMonths(ctx, historyLen)
	if err != nil {
		return Report{}, err
	}
	end := monthStart(now).AddDate(0, 1, 0)
	r := Report{
		Month:    now.Format(monthLayout),
		Quota:    int64(math.Round(quotaTB * terabyte)),
		DaysLeft: round1(end.Sub(now).Hours() / 24),
		History:  hist,
	}
	for _, h := range hist {
		if h.Month == r.Month {
			r.Tx, r.Rx = h.Tx, h.Rx
			r.ProjectedTx = project(h.Tx, time.UnixMilli(h.Since), now, end)
		}
	}
	if r.Quota > 0 {
		r.Pct = round1(100 * float64(r.Tx) / float64(r.Quota))
	}
	return r, nil
}

// project prolonge jusqu'à end le rythme observé depuis since (début de la mesure dans le mois).
func project(tx int64, since, now, end time.Time) int64 {
	// Sur quelques minutes, le rythme n'a pas de sens : au moins une heure de référence.
	elapsed := max(now.Sub(since), time.Hour)
	return tx + int64(float64(tx)*end.Sub(now).Seconds()/elapsed.Seconds())
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// Volume formate un nombre d'octets en unités décimales, comme les quotas des hébergeurs : « 1,64 To ».
func Volume(b int64) string {
	units := []string{"o", "ko", "Mo", "Go", "To", "Po"}
	v, i := float64(b), 0
	for math.Abs(v) >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	var s string
	switch {
	case i == 0 || v >= 100:
		s = fmt.Sprintf("%.0f", v)
	case v >= 10:
		s = fmt.Sprintf("%.1f", v)
	default:
		s = fmt.Sprintf("%.2f", v)
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return strings.Replace(s, ".", ",", 1) + " " + units[i]
}
