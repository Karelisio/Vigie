package statuspage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
)

// style est la feuille de style intégrée. Sans commentaire : html/template retire les commentaires CSS,
// ce qui changerait l'empreinte annoncée dans la Content-Security-Policy.
const style = `:root{--bg:#f4f5f7;--card:#fff;--text:#15181c;--muted:#56606b;--line:#e1e4e8;--ok:#1f8a4c;--ok-bg:#e2f3e9;--ok-fg:#13633a;--part:#d4930a;--part-bg:#fcf0d4;--part-fg:#734a00;--down:#d23c3c;--down-bg:#fce5e3;--down-fg:#9f1f1a;--none:#d2d7dd;--unk-bg:#eaecef;--unk-fg:#49515b;--link:#1b5cba}
@media (prefers-color-scheme:dark){:root{--bg:#0d1013;--card:#161a1f;--text:#e6e9ed;--muted:#9aa4af;--line:#262c33;--ok:#36b068;--ok-bg:#11301e;--ok-fg:#80dca6;--part:#e3a526;--part-bg:#33270b;--part-fg:#f5cc74;--down:#ea5a5a;--down-bg:#3a1616;--down-fg:#ff9e96;--none:#2b323a;--unk-bg:#22282f;--unk-fg:#b4bdc7;--link:#82b2ff}}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%;text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--text);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif}
main{max-width:760px;margin:0 auto;padding:40px 16px 32px}
h1{font-size:1.8rem;line-height:1.2;margin:0 0 6px;letter-spacing:-.01em;overflow-wrap:anywhere}
.desc{margin:0;color:var(--muted);overflow-wrap:anywhere}
.banner{display:flex;align-items:center;gap:14px;margin:24px 0 32px;padding:18px 20px;border-radius:18px;background:var(--ok-bg);color:var(--ok-fg)}
.banner.s-degraded{background:var(--part-bg);color:var(--part-fg)}
.banner.s-outage{background:var(--down-bg);color:var(--down-fg)}
.ico{flex:none;display:flex;align-items:center;justify-content:center;width:38px;height:38px;border-radius:50%;background:var(--ok);color:#fff;font-size:20px;font-weight:700;line-height:1}
.s-degraded .ico{background:var(--part);color:#231700}
.s-outage .ico{background:var(--down)}
.s-operational .ico::after{content:"\2713"}
.s-degraded .ico::after{content:"!"}
.s-outage .ico::after{content:"\2715"}
.head{margin:0;font-size:1.2rem;font-weight:700;line-height:1.3}
.sub{margin:2px 0 0;font-size:.92rem}
section{margin:0 0 32px}
h2{margin:0 0 10px 4px;font-size:1rem;font-weight:650;color:var(--muted)}
.list{list-style:none;margin:0;padding:0;background:var(--card);border:1px solid var(--line);border-radius:18px}
.item{padding:16px 18px}
.item+.item,.inc+.inc{border-top:1px solid var(--line)}
.row{display:flex;align-items:center;justify-content:space-between;gap:12px}
.name{font-weight:650;overflow-wrap:anywhere}
.pill{flex:none;display:inline-flex;align-items:center;gap:6px;padding:3px 10px;border-radius:999px;font-size:.85rem;font-weight:650;white-space:nowrap}
.pill::before{content:"";width:8px;height:8px;border-radius:50%;background:currentColor}
.p-online{background:var(--ok-bg);color:var(--ok-fg)}
.p-degraded{background:var(--part-bg);color:var(--part-fg)}
.p-offline{background:var(--down-bg);color:var(--down-fg)}
.p-unknown{background:var(--unk-bg);color:var(--unk-fg)}
.bars{display:flex;gap:2px;height:34px;margin-top:14px}
.d{flex:1 1 0;min-width:0;border-radius:2px;background:var(--none)}
.d.ok{background:var(--ok)}
.d.part{background:var(--part)}
.d.down{background:var(--down)}
.legend{display:flex;justify-content:space-between;gap:8px;margin-top:6px;font-size:.78rem;color:var(--muted)}
.legend .up{text-align:center}
.r30{display:none}
.keys{display:flex;flex-wrap:wrap;gap:6px 18px;list-style:none;margin:12px 4px 0;padding:0;font-size:.8rem;color:var(--muted)}
.keys li{display:flex;align-items:center;gap:6px}
.keys .d{flex:none;width:10px;height:10px}
.inc{display:flex;gap:12px;padding:14px 18px}
.inc p{margin:0;overflow-wrap:anywhere}
.dot{flex:none;width:10px;height:10px;margin-top:8px;border-radius:50%;background:var(--down)}
.dot.deg{background:var(--part)}
.when{font-size:.88rem;color:var(--muted)}
.empty{margin:0;padding:16px 18px;background:var(--card);border:1px solid var(--line);border-radius:18px;color:var(--muted)}
footer{font-size:.82rem;color:var(--muted);text-align:center}
footer p{margin:4px 0}
a{color:var(--link)}
@media (max-width:600px){main{padding-top:28px}h1{font-size:1.5rem}.banner{margin:20px 0 28px;padding:16px}.item{padding:14px 16px}.bars{height:30px}.bars .d:nth-child(-n+60){display:none}.r90{display:none}.r30{display:inline}}`

const page = `<!doctype html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="robots" content="noindex, nofollow">
<meta http-equiv="refresh" content="60">
<title>{{.Title}}</title>
<link rel="icon" href="{{.Icon}}">
<style>` + style + `</style>
</head>
<body>
<main>
<header>
<h1>{{.Title}}</h1>
{{- if .Description}}
<p class="desc">{{.Description}}</p>
{{- end}}
</header>
<section class="banner s-{{.Status}}">
<span class="ico" aria-hidden="true"></span>
<div><p class="head">{{.StatusText}}</p><p class="sub">{{.StatusDetail}}</p></div>
</section>
<section aria-labelledby="services">
<h2 id="services">Services</h2>
{{- if .Items}}
<ul class="list">
{{- range .Items}}
<li class="item">
<div class="row"><span class="name">{{.Name}}</span><span class="pill p-{{.Status}}">{{.StatusText}}</span></div>
{{- if $.ShowUptime}}
<div class="bars" role="img" aria-label="{{.BarsLabel}}">{{range .Days}}<span class="d {{.Class}}" title="{{.Title}}"></span>{{end}}</div>
<div class="legend" aria-hidden="true"><span><span class="r90">il y a 90 jours</span><span class="r30">il y a 30 jours</span></span><span class="up">{{.UptimeText}}</span><span>aujourd'hui</span></div>
{{- end}}
</li>
{{- end}}
</ul>
{{- if .ShowUptime}}
<ul class="keys" aria-hidden="true"><li><span class="d ok"></span>Opérationnel</li><li><span class="d part"></span>Perturbé</li><li><span class="d down"></span>Hors service</li><li><span class="d"></span>Pas de données</li></ul>
{{- end}}
{{- else}}
<p class="empty">Aucun service n'est affiché pour l'instant.</p>
{{- end}}
</section>
<section aria-labelledby="incidents">
<h2 id="incidents">Incidents des 7 derniers jours</h2>
{{- if .Incidents}}
<ul class="list">
{{- range .Incidents}}
<li class="inc"><span class="dot{{if .Degraded}} deg{{end}}" aria-hidden="true"></span><div><p><strong>{{.Name}}</strong> · {{.StatusText}}</p><p class="when">{{.When}}</p></div></li>
{{- end}}
</ul>
{{- else}}
<p class="empty">Aucun incident ces 7 derniers jours.</p>
{{- end}}
</section>
<footer>
<p>Mis à jour le {{.Updated}} · actualisation automatique chaque minute</p>
<p>Propulsé par <a href="https://github.com/Karelisio/Vigie" rel="noopener noreferrer">Vigie</a></p>
</footer>
</main>
</body>
</html>
`

var tmpl = template.Must(template.New("status").Parse(page))

// CSP : rien d'autre que la feuille de style intégrée (par son empreinte) et l'icône en data:.
var CSP = func() string {
	sum := sha256.Sum256([]byte(style))
	return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; img-src data:; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
}()

// Icônes de l'onglet : un disque de la couleur de l'état global (aucune requête supplémentaire).
var icons = map[string]template.URL{
	Operational: icon("1f8a4c"),
	Disrupted:   icon("d4930a"),
	Outage:      icon("d23c3c"),
}

func icon(color string) template.URL {
	return template.URL("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Ccircle cx='8' cy='8' r='7' fill='%23" + color + "'/%3E%3C/svg%3E")
}

var statusTexts = map[string]string{
	Operational: "Tous les services fonctionnent",
	Disrupted:   "Perturbations",
	Outage:      "Panne",
	Online:      "En ligne",
	Offline:     "Hors ligne",
	Unknown:     "Inconnu",
}

func itemText(status string) string {
	if status == Degraded {
		return "Dégradé"
	}
	return statusTexts[status]
}

// ---- Vue HTML ----

type view struct {
	Title, Description string
	Icon               template.URL
	Status, StatusText string
	StatusDetail       string
	ShowUptime         bool
	Items              []itemView
	Incidents          []incidentView
	Updated            string
}

type itemView struct {
	Name, Status, StatusText string
	BarsLabel, UptimeText    string
	Days                     []dayView
}

type dayView struct{ Class, Title string }

type incidentView struct {
	Name, StatusText, When string
	Degraded               bool
}

// HTML rend la page complète.
func (p Page) HTML() ([]byte, error) {
	v := view{
		Title: p.Title, Description: p.Description, Icon: icons[p.Status],
		Status: p.Status, StatusText: statusTexts[p.Status], StatusDetail: detail(p),
		ShowUptime: p.ShowUptime, Updated: longDate(p.UpdatedAt) + " à " + clock(p.UpdatedAt) + " (" + zone(p.UpdatedAt) + ")",
	}
	for _, it := range p.Items {
		iv := itemView{Name: it.Name, Status: it.Status, StatusText: itemText(it.Status)}
		if p.ShowUptime {
			iv.UptimeText, iv.BarsLabel = uptimeTexts(it)
			for _, d := range it.Days {
				iv.Days = append(iv.Days, dayView{Class: dayClass(d.Ratio), Title: shortDate(d.Date, true) + " : " + ratioText(d.Ratio)})
			}
		}
		v.Items = append(v.Items, iv)
	}
	for _, inc := range p.Incidents {
		v.Incidents = append(v.Incidents, incidentView{
			Name: inc.Name, StatusText: strings.ToLower(itemText(inc.Status)), When: when(inc, p.UpdatedAt), Degraded: inc.Status == Degraded,
		})
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// detail : la phrase sous l'état global.
func detail(p Page) string {
	known := p.Counts[Online] + p.Counts[Degraded] + p.Counts[Offline]
	hit := p.Counts[Degraded] + p.Counts[Offline]
	switch {
	case len(p.Items) == 0:
		return "Aucun service affiché."
	case p.Status == Outage:
		return plural(p.Counts[Offline], known, "hors ligne", "hors ligne")
	case hit > 0:
		return plural(hit, known, "perturbé", "perturbés")
	case known == 1:
		return "Le service est en ligne."
	case known == 0:
		return "État en cours de vérification."
	}
	return fmt.Sprintf("Les %d services sont en ligne.", known)
}

func plural(n, of int, one, many string) string {
	if n > 1 {
		return fmt.Sprintf("%d services sur %d sont %s.", n, of, many)
	}
	return fmt.Sprintf("%d service sur %d est %s.", n, of, one)
}

func dayClass(r float64) string {
	switch {
	case r < 0:
		return "none"
	case r >= 0.995:
		return "ok"
	case r >= 0.95:
		return "part"
	}
	return "down"
}

// percent : pourcentage arrondi par défaut à 2 décimales (jamais 100 % s'il y a eu une panne).
func percent(r float64) float64 { return math.Floor(r*10000) / 100 }

func ratioText(r float64) string {
	if r < 0 {
		return "pas de données"
	}
	return frNumber(percent(r)) + " %"
}

func frNumber(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// uptimeTexts : légende sous les barres et texte alternatif (jours perturbés, les 10 pires).
func uptimeTexts(it PageItem) (legend, label string) {
	if it.DataDays == 0 {
		return "pas encore de données", "Disponibilité de " + it.Name + " : pas encore de données."
	}
	n := "jours"
	if it.DataDays == 1 {
		n = "jour"
	}
	legend = fmt.Sprintf("%s %% sur %d %s", frNumber(percent(it.Uptime)), it.DataDays, n)
	var worst []Day
	for _, d := range it.Days {
		if d.Ratio >= 0 && d.Ratio < 0.995 {
			worst = append(worst, d)
		}
	}
	label = "Disponibilité quotidienne de " + it.Name + " : " + legend + ". "
	if len(worst) == 0 {
		return legend, label + "Aucun jour perturbé."
	}
	// Les plus touchés d'abord.
	for i := 1; i < len(worst); i++ {
		for j := i; j > 0 && worst[j].Ratio < worst[j-1].Ratio; j-- {
			worst[j], worst[j-1] = worst[j-1], worst[j]
		}
	}
	var parts []string
	for _, d := range worst[:min(10, len(worst))] {
		parts = append(parts, shortDate(d.Date, false)+" ("+ratioText(d.Ratio)+")")
	}
	label += fmt.Sprintf("Jours perturbés : %s.", strings.Join(parts, ", "))
	if len(worst) > 10 {
		label += fmt.Sprintf(" Et %d autres.", len(worst)-10)
	}
	return legend, label
}

var months = []string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}

// shortDate : « 28 oct. » (jour UTC des barres), avec l'année si demandé.
func shortDate(t time.Time, year bool) string {
	t = t.UTC()
	s := strconv.Itoa(t.Day()) + " " + months[t.Month()-1]
	if year {
		s += " " + strconv.Itoa(t.Year())
	}
	return s
}

// localDate : « 28 oct. » en heure locale de l'agent (TZ).
func localDate(t time.Time) string {
	t = t.In(time.Local)
	return strconv.Itoa(t.Day()) + " " + months[t.Month()-1]
}

func longDate(t time.Time) string {
	t = t.In(time.Local)
	return localDate(t) + " " + strconv.Itoa(t.Year())
}

func clock(t time.Time) string { return t.In(time.Local).Format("15:04") }

// zone : « UTC+1 », « UTC+5:30 », « UTC ».
func zone(t time.Time) string {
	_, off := t.In(time.Local).Zone()
	if off == 0 {
		return "UTC"
	}
	sign := "+"
	if off < 0 {
		sign, off = "−", -off
	}
	s := "UTC" + sign + strconv.Itoa(off/3600)
	if m := off % 3600 / 60; m != 0 {
		s += fmt.Sprintf(":%02d", m)
	}
	return s
}

// when : « 28 oct., 14:05 → 14:32 · 27 min », ou « depuis le 28 oct. à 14:05 · en cours ».
func when(inc Incident, now time.Time) string {
	if inc.End.IsZero() {
		return "Depuis le " + localDate(inc.Start) + " à " + clock(inc.Start) + " · en cours (" + duration(now.Sub(inc.Start)) + ")"
	}
	end := clock(inc.End)
	if localDate(inc.End) != localDate(inc.Start) {
		end = localDate(inc.End) + ", " + end
	}
	return localDate(inc.Start) + ", " + clock(inc.Start) + " → " + end + " · " + duration(inc.End.Sub(inc.Start))
}

func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moins d'une minute"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	case d < 24*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return strconv.Itoa(h) + " h"
		}
		return fmt.Sprintf("%d h %02d", h, m)
	}
	days, h := int(d.Hours())/24, int(d.Hours())%24
	if h == 0 {
		return strconv.Itoa(days) + " j"
	}
	return fmt.Sprintf("%d j %d h", days, h)
}

// ---- Vue JSON ----

type PublicJSON struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      string         `json:"status"` // operational, degraded, outage
	StatusText  string         `json:"status_text"`
	UpdatedAt   int64          `json:"updated_at"`
	Items       []ItemJSON     `json:"items"`
	Incidents   []IncidentJSON `json:"incidents"`
}

type ItemJSON struct {
	Name   string    `json:"name"`
	Status string    `json:"status"`           // online, degraded, offline, unknown
	Uptime *float64  `json:"uptime,omitempty"` // % sur les jours avec données (absent si la disponibilité est masquée)
	Days   []DayJSON `json:"days,omitempty"`   // 90 jours, du plus ancien à aujourd'hui
}

type DayJSON struct {
	Date   string   `json:"date"`   // AAAA-MM-JJ (UTC)
	Uptime *float64 `json:"uptime"` // %, null sans donnée
}

type IncidentJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"` // offline ou degraded
	Start  int64  `json:"start"`
	End    *int64 `json:"end"` // null : en cours
}

// JSON renvoie la version JSON de la page.
func (p Page) JSON() PublicJSON {
	out := PublicJSON{
		Title: p.Title, Description: p.Description, Status: p.Status, StatusText: statusTexts[p.Status],
		UpdatedAt: p.UpdatedAt.UnixMilli(), Items: []ItemJSON{}, Incidents: []IncidentJSON{},
	}
	for _, it := range p.Items {
		ij := ItemJSON{Name: it.Name, Status: it.Status}
		if p.ShowUptime {
			if it.Uptime >= 0 {
				v := percent(it.Uptime)
				ij.Uptime = &v
			}
			ij.Days = make([]DayJSON, 0, len(it.Days))
			for _, d := range it.Days {
				dj := DayJSON{Date: d.Date.UTC().Format("2006-01-02")}
				if d.Ratio >= 0 {
					v := percent(d.Ratio)
					dj.Uptime = &v
				}
				ij.Days = append(ij.Days, dj)
			}
		}
		out.Items = append(out.Items, ij)
	}
	for _, inc := range p.Incidents {
		ij := IncidentJSON{Name: inc.Name, Status: inc.Status, Start: inc.Start.UnixMilli()}
		if !inc.End.IsZero() {
			e := inc.End.UnixMilli()
			ij.End = &e
		}
		out.Incidents = append(out.Incidents, ij)
	}
	return out
}
