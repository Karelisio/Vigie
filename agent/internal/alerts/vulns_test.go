package alerts

import (
	"context"
	"testing"

	"github.com/karelisio/vigie/agent/internal/vulns"
)

func vulnReport(update bool, list ...vulns.Vuln) vulns.Report {
	return vulns.Report{Images: []vulns.Image{
		{
			ID: "sha256:aaaa", Ref: "ghcr.io/g0ldyy/comet:latest", UpdateAvailable: update,
			Containers: []vulns.ContainerRef{{ID: "c1", Name: "comet"}},
			Vulns:      list,
		},
		{ID: "sha256:bbbb", Containers: []vulns.ContainerRef{{ID: "c2", Name: "propre"}}, Vulns: []vulns.Vuln{}},
	}}
}

func vuln(id, sev, pkg, fixed string) vulns.Vuln {
	return vulns.Vuln{ID: id, Aliases: []string{"CVE-" + id}, Severity: sev, Package: pkg, Version: "1.0-1", Fixed: fixed}
}

func setVulnAlert(t *testing.T, e *Engine, level string, muted ...string) {
	t.Helper()
	s := e.set.Get()
	s.VulnAlert = level
	s.Muted = muted
	if _, err := e.set.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestVulnAlertsOncePerVulnAndImage(t *testing.T) {
	e, st := newEngine(t)
	r := vulnReport(false,
		vuln("1", "critical", "openssl", "3.0.15-1"),
		vuln("2", "critical", "openssl", "3.0.15-1"),
		vuln("3", "critical", "glibc", ""), // pas de correction : jamais signalée
		vuln("4", "high", "zlib", "1.3-1"),
		vuln("5", "medium", "curl", "8.0-1"),
	)
	e.OnVulns(r)
	e.OnVulns(r) // même résultat : rien de nouveau

	// Niveau « élevée » : la faille élevée corrigeable s'ajoute, une fois.
	setVulnAlert(t, e, "high")
	e.OnVulns(r)

	// Redémarrage de l'agent : les failles déjà signalées sont mémorisées en base.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again := New(ctx, e.cfg, e.mon, e.dk, st, e.log)
	r.Images[0].Vulns = append(r.Images[0].Vulns, vuln("6", "critical", "openssl", "3.0.16-1"), vuln("7", "high", "libxml2", "2.9.14-2"))
	r.Images[0].UpdateAvailable = true
	again.OnVulns(r)

	want := []string{
		"critical comet : 2 failles critiques corrigées dans une version plus récente",
		"warning comet : 1 faille de gravité élevée corrigée dans une version plus récente",
		"critical comet : 2 failles (1 critique) corrigées dans une version plus récente",
	}
	got := alertTitles(t, st)
	if len(got) != len(want) {
		t.Fatalf("alertes : %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("alerte %d : %q, attendu %q", i, got[i], want[i])
		}
	}
	list, _ := st.Alerts(context.Background(), 10)
	if b := list[2].Body; b != "openssl 1.0-1 → 3.0.15-1 (CVE-1). Mets à jour l'image si une nouvelle version est disponible." {
		t.Errorf("corps : %q", b)
	}
	if b := list[0].Body; b != "openssl 1.0-1 → 3.0.16-1 (CVE-6) · libxml2 1.0-1 → 2.9.14-2 (CVE-7). Une nouvelle image est disponible : mets-la à jour depuis Vigie." {
		t.Errorf("corps : %q", b)
	}
	if list[0].Kind != "vuln" || list[0].Target != "comet" {
		t.Errorf("%+v", list[0])
	}
}

func TestVulnAlertsOffAndMuted(t *testing.T) {
	e, st := newEngine(t)
	r := vulnReport(false, vuln("1", "critical", "openssl", "3.0.15-1"))
	setVulnAlert(t, e, "off")
	e.OnVulns(r)
	// Conteneur en sourdine : pas d'alerte, ni de mémorisation.
	setVulnAlert(t, e, "critical", "comet")
	e.OnVulns(r)
	if got := alertTitles(t, st); len(got) != 0 {
		t.Fatalf("aucune alerte attendue : %q", got)
	}
	// Sourdine levée : l'alerte part.
	setVulnAlert(t, e, "critical")
	e.OnVulns(r)
	if got := alertTitles(t, st); len(got) != 1 {
		t.Fatalf("alertes : %q", got)
	}
	// Plus de 3 paquets : résumé.
	r = vulnReport(false, vuln("a", "critical", "p1", "2"), vuln("b", "critical", "p2", "2"), vuln("c", "critical", "p3", "2"), vuln("d", "critical", "p4", "2"))
	r.Images[0].ID = "sha256:autre"
	e.OnVulns(r)
	list, _ := st.Alerts(context.Background(), 1)
	if list[0].Body != "p1 1.0-1 → 2 (CVE-a) · p2 1.0-1 → 2 (CVE-b) · p3 1.0-1 → 2 (CVE-c) · et 1 autre(s) paquet(s). Mets à jour l'image si une nouvelle version est disponible." {
		t.Fatalf("%q", list[0].Body)
	}
}
