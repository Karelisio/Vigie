package statuspage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func checkStatus(id int64, name, url string, ok bool, fails int) checks.Status {
	s := checks.Status{Check: store.Check{ID: id, Name: name, URL: url, Enabled: true}, ConsecutiveFailures: fails}
	res := store.CheckResult{CheckID: id, TS: time.Now().UnixMilli(), OK: ok, Status: 200, LatencyMS: 120}
	if !ok {
		res.Status, res.Error = 0, "dial tcp 10.0.0.5:443: connect: connection refused (https://secret-host.15122000.xyz/manifest.json)"
	}
	s.Last = &res
	return s
}

func TestConfigSlugAndValidation(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	m, err := New(ctx, st, Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := m.Get()
	if len(c.Slug) < 16 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(c.Slug) || c.Enabled || c.Title != DefaultTitle || !c.ShowUptime || c.Items == nil {
		t.Fatalf("configuration initiale : %+v", c)
	}
	if m.Match(c.Slug) {
		t.Fatal("page désactivée accessible")
	}
	for _, bad := range []struct {
		items []Item
		msg   string
	}{
		{[]Item{{Kind: KindCheck, Ref: "1", Label: "  "}}, "nom public"},
		{[]Item{{Kind: "image", Ref: "1", Label: "A"}}, "type d'élément inconnu"},
		{[]Item{{Kind: KindCheck, Ref: "abc", Label: "A"}}, "identifiant de service invalide"},
		{[]Item{{Kind: KindContainer, Ref: "../etc", Label: "A"}}, "nom de conteneur invalide"},
		{[]Item{{Kind: KindCheck, Ref: "1", Label: "A"}, {Kind: KindCheck, Ref: "1", Label: "B"}}, "en double"},
		{[]Item{{Kind: KindCheck, Ref: "1", Label: strings.Repeat("é", 61)}}, "trop long"},
	} {
		if _, err := m.Update(ctx, Config{Items: bad.items}); err == nil || !strings.Contains(err.Error(), bad.msg) {
			t.Errorf("%+v : %v (attendu %q)", bad.items, err, bad.msg)
		}
	}
	many := make([]Item, maxItems+1)
	for i := range many {
		many[i] = Item{Kind: KindContainer, Ref: "c" + strings.Repeat("x", i), Label: "C"}
	}
	if _, err := m.Update(ctx, Config{Items: many}); err == nil {
		t.Error("trop d'éléments acceptés")
	}

	out, err := m.Update(ctx, Config{
		Enabled: true, Slug: "choisi-par-le-client", Title: "  Mes\tservices  ", Description: "Stremio\nchez moi",
		Items: []Item{{Kind: " Check ", Ref: " 3 ", Label: " AIOStreams "}, {Kind: KindContainer, Ref: "/comet", Label: "Comet"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Slug != c.Slug || out.Title != "Mes services" || out.Description != "Stremio chez moi" ||
		out.Items[0] != (Item{KindCheck, "3", "AIOStreams"}) || out.Items[1].Ref != "comet" || out.ShowUptime {
		t.Fatalf("après modification : %+v", out)
	}
	if !m.Match(c.Slug) || m.Match(c.Slug+"x") || m.Match("") || m.Match(strings.ToUpper(c.Slug)) {
		t.Fatal("comparaison de l'identifiant")
	}

	// Relu en base au redémarrage.
	again, _ := New(ctx, st, Options{})
	if g := again.Get(); g.Slug != c.Slug || !g.Enabled || len(g.Items) != 2 {
		t.Fatalf("après redémarrage : %+v", g)
	}
	// Nouvel identifiant : l'ancien ne fonctionne plus.
	r, err := again.Regenerate(ctx)
	if err != nil || r.Slug == c.Slug || len(r.Slug) < 16 || again.Match(c.Slug) || !again.Match(r.Slug) || len(r.Items) != 2 {
		t.Fatalf("régénération : %+v %v", r, err)
	}
}

func TestPageStates(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	list := []checks.Status{
		checkStatus(1, "aiostreams-prod", "https://aiostreams.15122000.xyz", true, 0),
		checkStatus(2, "comet-interne", "https://comet.15122000.xyz", false, 1),
		checkStatus(3, "meta", "https://aiometadata.15122000.xyz", false, 3),
		{Check: store.Check{ID: 4, Name: "pause", Enabled: false}},
	}
	containers := []monitor.Container{
		{Name: "web", State: "running", Image: "ghcr.io/prive/web:1.2"},
		{Name: "db", State: "running", Health: "unhealthy", Image: "postgres:16"},
		{Name: "stopped", State: "exited", ExitCode: 1},
	}
	m, _ := New(ctx, st, Options{
		Checks: func() []checks.Status { return list }, Containers: func() []monitor.Container { return containers }, DownAfter: 2,
	})
	cfg := Config{Enabled: true, ShowUptime: true, Items: []Item{
		{KindCheck, "1", "Recherche"}, {KindCheck, "2", "Comet"}, {KindCheck, "3", "Fiches"}, {KindCheck, "4", "En pause"},
		{KindCheck, "99", "Supprimé"},
		{KindContainer, "web", "Site"}, {KindContainer, "db", "Base"}, {KindContainer, "stopped", "Arrêté"}, {KindContainer, "gone", "Disparu"},
	}}
	if _, err := m.Update(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	p := m.Page(ctx, time.Now())
	got := map[string]string{}
	for _, it := range p.Items {
		got[it.Name] = it.Status
		if len(it.Days) != Days {
			t.Errorf("%s : %d jours", it.Name, len(it.Days))
		}
	}
	want := map[string]string{
		"Recherche": Online, "Comet": Degraded, "Fiches": Offline, "En pause": Unknown,
		"Site": Online, "Base": Degraded, "Arrêté": Offline, "Disparu": Offline,
	}
	if len(got) != len(want) {
		t.Fatalf("éléments : %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s : %s, attendu %s", k, got[k], v)
		}
	}
	// 7 éléments connus, 3 hors ligne, 2 dégradés : perturbations.
	if p.Status != Disrupted || detail(p) != "5 services sur 7 sont perturbés." {
		t.Errorf("état global : %s (%s)", p.Status, detail(p))
	}

	for _, c := range []struct {
		counts map[string]int
		want   string
	}{
		{map[string]int{}, Operational},
		{map[string]int{Online: 3, Unknown: 2}, Operational},
		{map[string]int{Online: 2, Offline: 1}, Disrupted},
		{map[string]int{Online: 1, Offline: 1}, Disrupted},
		{map[string]int{Online: 1, Offline: 2}, Outage},
		{map[string]int{Offline: 1}, Outage},
		{map[string]int{Degraded: 3}, Disrupted},
	} {
		if got := overall(c.counts); got != c.want {
			t.Errorf("overall(%v) = %s, attendu %s", c.counts, got, c.want)
		}
	}
}

func TestOutages(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).UnixMilli()
	minute := int64(60_000)
	runs := []store.RunPoint{}
	pattern := "++-+---++--+----" // + réussite, - échec, une minute d'écart
	for i, c := range pattern {
		runs = append(runs, store.RunPoint{TS: base + int64(i)*minute, OK: c == '+'})
	}
	got := outages(runs, 2)
	if len(got) != 3 {
		t.Fatalf("coupures : %+v", got)
	}
	if got[0].Start.UnixMilli() != base+4*minute || got[0].End.UnixMilli() != base+7*minute ||
		got[1].Start.UnixMilli() != base+9*minute || got[1].End.UnixMilli() != base+11*minute ||
		got[2].Start.UnixMilli() != base+12*minute || !got[2].End.IsZero() {
		t.Errorf("coupures : %+v", got)
	}
}

func TestContainerIncidents(t *testing.T) {
	at := func(min int) int64 {
		return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute).UnixMilli()
	}
	// Chronologie du plus récent au plus ancien, comme store.Events.
	events := []store.Event{
		{TS: at(200), Action: "die", Detail: "1", Source: "docker"},
		{TS: at(120), Action: "health", Detail: "healthy", Source: "docker"},
		{TS: at(100), Action: "health", Detail: "unhealthy", Source: "docker"},
		{TS: at(61), Action: "start", Source: "docker"},
		{TS: at(60), Action: "stop", Detail: "ok", Source: "vigie"},
		{TS: at(60), Action: "die", Detail: "0", Source: "docker"}, // mise à jour : 1 min, ignorée
		{TS: at(30), Action: "start", Source: "docker"},
		{TS: at(10), Action: "die", Detail: "137", Source: "docker"},
	}
	got := containerIncidents(events, &monitor.Container{State: "exited"})
	if len(got) != 3 ||
		got[0] != (Incident{Status: Offline, Start: time.UnixMilli(at(10)), End: time.UnixMilli(at(30))}) ||
		got[1] != (Incident{Status: Degraded, Start: time.UnixMilli(at(100)), End: time.UnixMilli(at(120))}) ||
		got[2] != (Incident{Status: Offline, Start: time.UnixMilli(at(200))}) {
		t.Fatalf("incidents : %+v", got)
	}
	// Redémarrage non vu (agent arrêté) : fin à l'heure de démarrage du conteneur.
	got = containerIncidents(events[:1], &monitor.Container{State: "running", StartedAt: at(260)})
	if len(got) != 1 || got[0].End.UnixMilli() != at(260) {
		t.Fatalf("redémarrage manqué : %+v", got)
	}
	// Conteneur disparu : toujours hors ligne.
	if got = containerIncidents(events[:1], nil); len(got) != 1 || !got[0].End.IsZero() {
		t.Fatalf("conteneur disparu : %+v", got)
	}
}

func TestHistoryFromStore(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Now()
	c := store.Check{Name: "a", URL: "https://a.example.com", ExpectedStatus: 200, IntervalSec: 60, TimeoutMS: 1000, Enabled: true, Kind: "http"}
	if err := st.CreateCheck(ctx, &c); err != nil {
		t.Fatal(err)
	}
	// Il y a 40 jours : 1 échec sur 4 ; il y a 2 jours : coupure de 3 échecs ; aujourd'hui : tout va bien.
	add := func(ts time.Time, ok bool) {
		if err := st.AddResult(ctx, store.CheckResult{CheckID: c.ID, TS: ts.UnixMilli(), OK: ok, LatencyMS: 50}); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-40 * 24 * time.Hour)
	for i, ok := range []bool{true, false, true, true} {
		add(old.Add(time.Duration(i)*time.Minute), ok)
	}
	two := now.Add(-2 * 24 * time.Hour)
	for i, ok := range []bool{true, false, false, false, true} {
		add(two.Add(time.Duration(i)*time.Minute), ok)
	}
	add(now.Add(-time.Minute), true)

	// Conteneurs mesurés pendant 2 h : « web » en marche la première heure seulement,
	// « nouveau » créé au bout d'une heure (compté à partir de sa première mesure).
	start := now.Add(-3 * time.Hour).Truncate(time.Hour)
	for i := 0; i < 120; i++ {
		ts := start.Add(time.Duration(i) * time.Minute)
		if err := st.InsertSys(ctx, store.SysSample{TS: ts.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		name := "web"
		if i >= 60 {
			name = "nouveau"
		}
		if err := st.InsertCtr(ctx, []store.CtrSample{{Name: name, TS: ts.UnixMilli()}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Aggregate(ctx, 4*time.Hour); err != nil {
		t.Fatal(err)
	}

	m, _ := New(ctx, st, Options{DownAfter: 2})
	h := m.checkHistory(ctx, c.ID, now)
	if h.DataDays != 3 || h.Uptime != 0.6 { // 6 réussites sur 10
		t.Errorf("historique du check : %d jours, %f", h.DataDays, h.Uptime)
	}
	if last := h.Days[Days-1]; last.Ratio != 1 || !last.Date.Equal(now.UTC().Truncate(24*time.Hour)) {
		t.Errorf("aujourd'hui : %+v", last)
	}
	if d := h.Days[Days-41]; d.Ratio != 0.75 && h.Days[Days-42].Ratio != 0.75 {
		t.Errorf("il y a 40 jours : %+v", d)
	}
	if len(h.incidents) != 1 || h.incidents[0].End.Sub(h.incidents[0].Start) != 3*time.Minute {
		t.Errorf("incidents du check : %+v", h.incidents)
	}

	if ch := m.containerHistory(ctx, "web", now); ch.DataDays < 1 || ch.Uptime != 0.5 {
		t.Errorf("historique du conteneur : %d jours, %f", ch.DataDays, ch.Uptime)
	}
	if ch := m.containerHistory(ctx, "nouveau", now); ch.DataDays < 1 || ch.Uptime != 1 {
		t.Errorf("conteneur récent : %d jours, %f", ch.DataDays, ch.Uptime)
	}
	// Jamais vu en marche : aucune donnée (il n'existait peut-être pas).
	if none := m.containerHistory(ctx, "inconnu", now); none.Uptime != -1 || none.DataDays != 0 || none.Days[Days-1].Ratio != -1 {
		t.Errorf("conteneur jamais lancé : %+v", none)
	}
}

// Rien d'interne ne sort : ni adresse, ni IP, ni message d'erreur, ni image, ni nom interne non choisi.
func TestNoLeakAndCSP(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	list := []checks.Status{
		checkStatus(1, "aiostreams-prod", "https://aiostreams.15122000.xyz/secret-config/manifest.json", true, 0),
		checkStatus(2, "comet-interne", "https://comet.15122000.xyz/abc", false, 5),
	}
	containers := []monitor.Container{{
		ID: "0123456789abcdef", Name: "vigie-agent-interne", State: "running", Image: "ghcr.io/prive/agent:1.2", Ports: []string{"8080/tcp"},
		ImageRef: "ghcr.io/prive/agent@sha256:deadbeef",
	}}
	m, _ := New(ctx, st, Options{
		Checks: func() []checks.Status { return list }, Containers: func() []monitor.Container { return containers }, DownAfter: 2,
	})
	if _, err := m.Update(ctx, Config{Enabled: true, ShowUptime: true, Title: "Mes <services>", Description: "Pour la famille", Items: []Item{
		{KindCheck, "1", "Recherche"}, {KindCheck, "2", "Comet <b>"}, {KindContainer, "vigie-agent-interne", "Tableau de bord"},
	}}); err != nil {
		t.Fatal(err)
	}
	p := m.Page(ctx, time.Now())
	html, err := p.HTML()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p.JSON())
	for _, out := range []string{string(html), string(raw)} {
		for _, secret := range []string{
			"15122000", "secret", "10.0.0.5", "connection refused", "dial tcp", "aiostreams-prod", "comet-interne",
			"vigie-agent-interne", "ghcr.io", "prive", "0123456789", "8080", "deadbeef", "<b>", "<services>",
		} {
			if strings.Contains(out, secret) {
				t.Errorf("fuite de %q", secret)
			}
		}
	}
	h := string(html)
	for _, want := range []string{"Mes &lt;services&gt;", "Pour la famille", "Recherche", "Comet &lt;b&gt;", "Tableau de bord",
		"Perturbations", "Hors ligne", "En ligne", "Propulsé par", `<meta http-equiv="refresh" content="60">`, `role="img"`, `lang="fr"`} {
		if !strings.Contains(h, want) {
			t.Errorf("absent de la page : %q", want)
		}
	}
	if strings.Contains(h, "<script") || strings.Contains(h, "style=\"") {
		t.Error("script ou style en ligne dans la page")
	}
	// L'empreinte de la CSP correspond exactement à la feuille de style servie.
	i, j := strings.Index(h, "<style>"), strings.Index(h, "</style>")
	sum := sha256.Sum256([]byte(h[i+len("<style>") : j]))
	if !strings.Contains(CSP, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || !strings.HasPrefix(CSP, "default-src 'none'") {
		t.Fatalf("CSP %q ne correspond pas à la feuille de style", CSP)
	}

	var pub PublicJSON
	if err := json.Unmarshal(raw, &pub); err != nil || pub.Status != Disrupted || len(pub.Items) != 3 || len(pub.Items[0].Days) != Days ||
		pub.Items[1].Status != Offline || pub.StatusText != "Perturbations" {
		t.Fatalf("JSON : %s", raw)
	}
	// Disponibilité masquée : ni barres ni pourcentages.
	if _, err := m.Update(ctx, Config{Enabled: true, Items: m.Get().Items}); err != nil {
		t.Fatal(err)
	}
	p = m.Page(ctx, time.Now())
	html, _ = p.HTML()
	raw, _ = json.Marshal(p.JSON())
	if strings.Contains(string(html), `class="bars"`) || strings.Contains(string(raw), `"days"`) || strings.Contains(string(raw), `"uptime"`) {
		t.Fatalf("disponibilité affichée malgré show_uptime=false : %s", raw)
	}
}

func TestFrenchFormatting(t *testing.T) {
	if got := duration(27 * time.Minute); got != "27 min" {
		t.Error(got)
	}
	if got := duration(125 * time.Minute); got != "2 h 05" {
		t.Error(got)
	}
	if got := duration(50 * time.Hour); got != "2 j 2 h" {
		t.Error(got)
	}
	if got := ratioText(0.99996); got != "99,99 %" {
		t.Error(got)
	}
	if got := ratioText(1); got != "100 %" {
		t.Error(got)
	}
	if got := shortDate(time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), true); got != "3 août 2026" {
		t.Error(got)
	}
	if got := plural(1, 4, "perturbé", "perturbés"); got != "1 service sur 4 est perturbé." {
		t.Error(got)
	}
}
