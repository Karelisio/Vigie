// Package report construit le rapport de la semaine : disponibilité des services, incidents,
// plantages de conteneurs, images à mettre à jour, disque, trafic et risque Oracle.
package report

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/oracle"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/traffic"
)

type Report struct {
	From       int64          `json:"from"` // ms
	To         int64          `json:"to"`
	Days       int            `json:"days"`
	Services   Services       `json:"services"`
	Incidents  Incidents      `json:"incidents"`
	Containers Containers     `json:"containers"`
	Updates    []string       `json:"updates"` // conteneurs dont une nouvelle image est publiée
	Disk       Disk           `json:"disk"`
	Traffic    *Traffic       `json:"traffic,omitempty"` // absent sans historique
	Oracle     *oracle.Report `json:"oracle,omitempty"`  // instances Oracle Cloud seulement
	SSH        *SSH           `json:"ssh,omitempty"`     // absent sans événement SSH sur la période
}

// SSH résume les connexions SSH de la période (journal d'authentification de l'hôte).
type SSH struct {
	Failed   int `json:"failed"`
	Accepted int `json:"accepted"`
	NewIPs   int `json:"new_ips"` // adresses jamais vues lors d'une connexion réussie
	Bans     int `json:"bans"`    // bannissements fail2ban
}

type Service struct {
	Name   string  `json:"name"`
	Uptime float64 `json:"uptime"` // %, -1 sans mesure
}

type Services struct {
	Uptime float64   `json:"uptime"` // moyenne des services mesurés, -1 sans mesure
	Worst  *Service  `json:"worst,omitempty"`
	List   []Service `json:"list"` // du moins disponible au plus disponible, services sans mesure à la fin
}

type Kind struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type Incidents struct {
	Total  int           `json:"total"`
	ByKind []Kind        `json:"by_kind"`
	Recent []store.Alert `json:"recent"`
}

type Containers struct {
	Crashes  int                        `json:"crashes"`
	Restarts int                        `json:"restarts"`
	List     []store.ContainerIncidents `json:"list"`
}

type Disk struct {
	Pct    float64  `json:"pct"`
	Used   uint64   `json:"used"`
	Total  uint64   `json:"total"`
	Change *float64 `json:"change,omitempty"` // évolution en points sur la période
}

type Traffic struct {
	Rx       int64   `json:"rx"` // octets sur la période (d'après l'historique par tranches de 15 min)
	Tx       int64   `json:"tx"`
	MonthTx  int64   `json:"month_tx"` // sortant du mois en cours
	Quota    int64   `json:"quota"`    // 0 sans quota
	MonthPct float64 `json:"month_pct"`
}

// Input : état courant de l'agent (le reste du rapport vient de la base).
type Input struct {
	DiskPct   float64
	DiskUsed  uint64
	DiskTotal uint64
	Updates   []string
	Kernel    string
	NumCPU    int
	QuotaTB   float64
}

// FromMonitor rassemble l'état courant utile au rapport.
func FromMonitor(mon *monitor.Monitor) Input {
	s := mon.Snapshot()
	in := Input{
		DiskPct: s.DiskPct, DiskUsed: s.DiskUsed, DiskTotal: s.DiskTotal, Updates: []string{},
		Kernel: mon.Host.Kernel, NumCPU: mon.Host.NumCPU, QuotaTB: mon.Settings().Get().NetQuotaTB,
	}
	for name, st := range mon.ImageUpdates() {
		if st.UpdateAvailable {
			in.Updates = append(in.Updates, name)
		}
	}
	sort.Strings(in.Updates)
	return in
}

// kindLabels nomme les types d'incidents (au pluriel, pour « Incidents : 3 (conteneurs 2, services 1) »).
var kindLabels = map[string]string{
	"container": "conteneurs",
	"check":     "services",
	"system":    "ressources",
	"oracle":    "Oracle",
	"traffic":   "trafic",
	"job":       "mises à jour",
	"port":      "ports",
	"ssh":       "SSH",
}

// Build construit le rapport des days derniers jours.
func Build(ctx context.Context, st *store.Store, in Input, now time.Time, days int) (Report, error) {
	from := now.AddDate(0, 0, -days)
	r := Report{
		From: from.UnixMilli(), To: now.UnixMilli(), Days: days, Updates: in.Updates,
		Disk: Disk{Pct: in.DiskPct, Used: in.DiskUsed, Total: in.DiskTotal},
	}
	var err error
	if r.Services, err = services(ctx, st, from); err != nil {
		return r, err
	}

	counts, err := st.IncidentCounts(ctx, from)
	if err != nil {
		return r, err
	}
	r.Incidents.ByKind = []Kind{}
	for _, k := range counts {
		label := kindLabels[k.Kind]
		if label == "" {
			label = k.Kind
		}
		r.Incidents.ByKind = append(r.Incidents.ByKind, Kind{Kind: k.Kind, Label: label, Count: k.Count})
		r.Incidents.Total += k.Count
	}
	if r.Incidents.Recent, err = st.Incidents(ctx, from, 10); err != nil {
		return r, err
	}

	if r.Containers.List, err = st.ContainerIncidents(ctx, from); err != nil {
		return r, err
	}
	for _, c := range r.Containers.List {
		r.Containers.Crashes += c.Crashes
		r.Containers.Restarts += c.Restarts
	}

	// Historique agrégé : évolution du disque et trafic de la période.
	pts, err := st.SysWindow(ctx, from)
	if err != nil {
		return r, err
	}
	if len(pts) > 0 {
		if in.DiskTotal > 0 {
			change := math.Round((in.DiskPct-pts[0].Disk)*10) / 10
			r.Disk.Change = &change
		}
		t := &Traffic{}
		step := store.AggStep.Seconds()
		for _, p := range pts {
			t.Rx += int64(p.Rx * step)
			t.Tx += int64(p.Tx * step)
		}
		if in.QuotaTB > 0 {
			month, err := traffic.Build(ctx, st, in.QuotaTB, now)
			if err != nil {
				return r, err
			}
			t.MonthTx, t.Quota, t.MonthPct = month.Tx, month.Quota, month.Pct
		}
		r.Traffic = t
	}

	if o, err := oracle.Evaluate(ctx, st, in.Kernel, in.NumCPU); err == nil && o.Applicable {
		r.Oracle = &o
	}
	if w, err := st.SSHWeek(ctx, from); err == nil && w.Failed+w.Accepted+w.Bans > 0 {
		r.SSH = &SSH{Failed: w.Failed, Accepted: w.Accepted, NewIPs: w.NewIPs, Bans: w.Bans}
	}
	return r, nil
}

func services(ctx context.Context, st *store.Store, from time.Time) (Services, error) {
	out := Services{Uptime: -1, List: []Service{}}
	list, err := st.Checks(ctx)
	if err != nil {
		return out, err
	}
	sum, n := 0.0, 0
	for _, c := range list {
		if !c.Enabled {
			continue
		}
		up, _, err := st.Uptime(ctx, c.ID, from)
		if err != nil {
			return out, err
		}
		out.List = append(out.List, Service{Name: c.Name, Uptime: up})
		if up >= 0 {
			sum += up
			n++
		}
	}
	sort.SliceStable(out.List, func(i, j int) bool {
		a, b := out.List[i].Uptime, out.List[j].Uptime
		if (a < 0) != (b < 0) {
			return b < 0
		}
		return a < b
	})
	if n > 0 {
		out.Uptime = math.Round(100*sum/float64(n)) / 100
		worst := out.List[0]
		out.Worst = &worst
	}
	return out, nil
}

// Text résume le rapport en quelques lignes, pour la notification.
func (r Report) Text() string {
	var lines []string
	if s := r.Services; s.Uptime >= 0 {
		line := "Services : " + pct(s.Uptime, 2)
		if len(s.List) > 1 && s.Worst != nil && s.Worst.Uptime < 100 {
			line += fmt.Sprintf(" (%s : %s)", s.Worst.Name, pct(s.Worst.Uptime, 2))
		}
		lines = append(lines, line)
	}
	if r.Incidents.Total == 0 {
		lines = append(lines, "Incidents : aucun")
	} else {
		parts := make([]string, 0, len(r.Incidents.ByKind))
		for _, k := range r.Incidents.ByKind {
			parts = append(parts, fmt.Sprintf("%s %d", k.Label, k.Count))
		}
		lines = append(lines, fmt.Sprintf("Incidents : %d (%s)", r.Incidents.Total, strings.Join(parts, ", ")))
	}
	if c := r.Containers; c.Crashes+c.Restarts > 0 {
		lines = append(lines, fmt.Sprintf("Conteneurs : %s, %s", plural(c.Crashes, "plantage"), plural(c.Restarts, "redémarrage")))
	}
	if n := len(r.Updates); n > 0 {
		lines = append(lines, fmt.Sprintf("Mises à jour : %s", plural(n, "image")))
	}
	host := []string{}
	if r.Disk.Total > 0 {
		host = append(host, "disque "+pct(r.Disk.Pct, 0))
	}
	if t := r.Traffic; t != nil {
		out := "sortant " + traffic.Volume(t.Tx)
		if t.Quota > 0 {
			out += " (mois : " + pct(t.MonthPct, 0) + " du quota)"
		}
		host = append(host, out)
	}
	if len(host) > 0 {
		lines = append(lines, "Serveur : "+strings.Join(host, " · "))
	}
	if o := r.Oracle; o != nil && o.Risk != "unknown" {
		lines = append(lines, "Oracle : "+map[string]string{"low": "risque faible", "medium": "à surveiller", "high": "risque de récupération"}[o.Risk])
	}
	if s := r.SSH; s != nil {
		line := "SSH : " + plural(s.Failed, "échec") + ", " + plural(s.Accepted, "connexion")
		if s.NewIPs > 0 {
			line += fmt.Sprintf(" (%d depuis une nouvelle adresse)", s.NewIPs)
		}
		if s.Bans > 0 {
			line += ", " + plural(s.Bans, "bannissement")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func pct(v float64, decimals int) string {
	if v >= 99.995 && decimals > 0 {
		return "100 %"
	}
	return strings.Replace(fmt.Sprintf("%.*f %%", decimals, v), ".", ",", 1)
}

func plural(n int, word string) string {
	if n > 1 {
		return fmt.Sprintf("%d %ss", n, word)
	}
	return fmt.Sprintf("%d %s", n, word)
}
