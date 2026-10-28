package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

func TestBuild(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	now := time.Now()
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Services : comet 90 %, aiostreams 100 % (un échec plus ancien que la semaine), un check désactivé.
	check := func(name string, enabled bool) int64 {
		c := store.Check{Name: name, URL: "https://" + name, ExpectedStatus: 200, IntervalSec: 60, TimeoutMS: 1000, Enabled: enabled}
		must(st.CreateCheck(ctx, &c))
		return c.ID
	}
	comet, aio := check("comet", true), check("aiostreams", true)
	check("ancien", false)
	for i := 0; i < 10; i++ {
		must(st.AddResult(ctx, store.CheckResult{CheckID: comet, TS: ago(time.Duration(i+1) * time.Hour), OK: i > 0, Status: 200}))
	}
	for i := 0; i < 4; i++ {
		must(st.AddResult(ctx, store.CheckResult{CheckID: aio, TS: ago(time.Duration(i+1) * time.Hour), OK: true, Status: 200}))
	}
	must(st.AddResult(ctx, store.CheckResult{CheckID: aio, TS: ago(8 * 24 * time.Hour), OK: false}))

	// Chronologie : plantages (code d'erreur, OOM) et redémarrages ; arrêts normaux ignorés.
	st.AddEvent(ctx, "comet", "die", "1", ago(time.Hour))
	st.AddEvent(ctx, "comet", "oom", "", ago(2*time.Hour))
	st.AddEvent(ctx, "comet", "die", "137", ago(2*time.Hour))
	st.AddEvent(ctx, "redis", "die", "143", ago(3*time.Hour))
	st.AddEvent(ctx, "redis", "restart", "", ago(3*time.Hour))
	st.AddEvent(ctx, "web", "die", "0", ago(4*time.Hour))
	st.AddEvent(ctx, "ancien", "die", "1", ago(8*24*time.Hour))

	// Alertes : seuls les incidents non résolus de la semaine comptent.
	alert := func(kind, severity string, resolved bool, d time.Duration) {
		must(st.AddAlert(ctx, &store.Alert{TS: ago(d), Kind: kind, Severity: severity, Target: "x", Title: kind, Resolved: resolved}))
	}
	alert("container", "critical", false, time.Hour)
	alert("container", "critical", false, 2*time.Hour)
	alert("check", "critical", false, 3*time.Hour)
	alert("container", "info", true, time.Hour)
	alert("image", "info", false, time.Hour)
	alert("summary", "warning", false, time.Hour)
	alert("report", "info", false, time.Hour)
	alert("check", "critical", false, 8*24*time.Hour)

	// Historique système : deux tranches de 15 min à 1 ko/s reçu et 2 ko/s émis, disque à 70 %.
	base := now.Add(-3 * time.Hour).Truncate(15 * time.Minute)
	for i := 0; i < 30; i++ {
		must(st.InsertSys(ctx, store.SysSample{TS: base.Add(time.Duration(i) * time.Minute).UnixMilli(), Rx: 1000, Tx: 2000, Disk: 70}))
	}
	must(st.Aggregate(ctx, 4*time.Hour))
	must(st.AddTraffic(ctx, now.Format("2006-01"), 0, 2e12, ago(time.Hour)))

	t.Setenv("VIGIE_ORACLE", "false")
	in := Input{DiskPct: 72.4, DiskUsed: 724, DiskTotal: 1000, Updates: []string{"comet"}, QuotaTB: 10}
	r, err := Build(ctx, st, in, now, 7)
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Services; s.Uptime != 95 || s.Worst == nil || s.Worst.Name != "comet" || s.Worst.Uptime != 90 || len(s.List) != 2 {
		t.Errorf("services = %+v", s)
	}
	if i := r.Incidents; i.Total != 3 || len(i.ByKind) != 2 || i.ByKind[0] != (Kind{"container", "conteneurs", 2}) || len(i.Recent) != 3 {
		t.Errorf("incidents = %+v", i)
	}
	if c := r.Containers; c.Crashes != 2 || c.Restarts != 1 || len(c.List) != 2 ||
		c.List[0] != (store.ContainerIncidents{Name: "comet", Crashes: 2}) || c.List[1] != (store.ContainerIncidents{Name: "redis", Restarts: 1}) {
		t.Errorf("conteneurs = %+v", c)
	}
	if r.Disk.Change == nil || *r.Disk.Change != 2.4 {
		t.Errorf("disque = %+v", r.Disk)
	}
	if tr := r.Traffic; tr == nil || tr.Rx != 1_800_000 || tr.Tx != 3_600_000 || tr.MonthPct != 20 || tr.Quota != 10e12 {
		t.Errorf("trafic = %+v", tr)
	}
	if r.Oracle != nil {
		t.Errorf("Oracle inattendu : %+v", r.Oracle)
	}
	want := strings.Join([]string{
		"Services : 95,00 % (comet : 90,00 %)",
		"Incidents : 3 (conteneurs 2, services 1)",
		"Conteneurs : 2 plantages, 1 redémarrage",
		"Mises à jour : 1 image",
		"Serveur : disque 72 % · sortant 3,6 Mo (mois : 20 % du quota)",
	}, "\n")
	if got := r.Text(); got != want {
		t.Errorf("texte :\n%s\nattendu :\n%s", got, want)
	}

	// Instance Oracle : le risque figure dans le rapport.
	t.Setenv("VIGIE_ORACLE", "true")
	r, err = Build(ctx, st, in, now, 7)
	if err != nil || r.Oracle == nil || !strings.HasSuffix(r.Text(), "Oracle : risque de récupération") {
		t.Errorf("Oracle = %+v (%v), texte :\n%s", r.Oracle, err, r.Text())
	}
}

func TestEmptyReport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	t.Setenv("VIGIE_ORACLE", "false")
	r, err := Build(context.Background(), st, Input{Updates: []string{}}, time.Now(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if r.Services.Uptime != -1 || r.Services.Worst != nil || r.Traffic != nil || r.Disk.Change != nil {
		t.Errorf("rapport vide = %+v", r)
	}
	if got := r.Text(); got != "Incidents : aucun" {
		t.Errorf("texte = %q", got)
	}
}
