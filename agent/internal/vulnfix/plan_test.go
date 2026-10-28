package vulnfix

import (
	"strings"
	"testing"

	"github.com/karelisio/vigie/agent/internal/vulns"
)

func debianInv() vulns.Inventory {
	return vulns.Inventory{
		Eco: "Debian:12", Tools: []string{"sh", "apt-get"},
		Bins: [][4]string{
			{"dpkg", "dpkg", "1.21.22", "arm64"}, {"libssl3", "openssl", "3.0.11-1", "arm64"},
			{"openssl", "openssl", "3.0.11-1", "arm64"}, {"libc6", "glibc", "2.36-9", "arm64"},
		},
	}
}

func debianImage() vulns.Image {
	vs := []vulns.Vuln{
		{ID: "V1", Severity: "critical", Package: "openssl", Version: "3.0.11-1", Fixed: "3.0.15-1", Ecosystem: "Debian:12"},
		{ID: "V2", Severity: "high", Package: "openssl", Version: "3.0.11-1", Fixed: "3.0.13-1", Ecosystem: "Debian:12"},
		{ID: "V3", Severity: "medium", Package: "glibc", Version: "2.36-9", Ecosystem: "Debian:12"},
	}
	return vulns.Image{
		ID: "sha256:img", Ref: "ghcr.io/x/y:latest", OS: "Debian GNU/Linux 12 (bookworm)", Ecosystem: "Debian:12",
		Containers: []vulns.ContainerRef{{ID: "c1", Name: "comet", State: "running"}},
		Vulns:      vs, Counts: vulns.CountVulns(vs),
		Fixes: []vulns.Fix{{Ecosystem: "Debian:12", Package: "openssl", Version: "3.0.11-1", Fixed: "3.0.15-1", Counts: vulns.CountVulns(vs[:2])}},
	}
}

func TestPlanPatchRoute(t *testing.T) {
	p := buildPlan(planInput{img: debianImage(), inv: debianInv(), hasInv: true, enabled: true})
	if p.Route != RoutePatch || !p.Patch.Possible || p.Patch.Manager != "apt" {
		t.Fatalf("%+v", p)
	}
	if len(p.Patch.Packages) != 1 || p.Patch.Packages[0].Name != "openssl" || p.Patch.Packages[0].Fixed != "3.0.15-1" ||
		strings.Join(p.Patch.Packages[0].Binaries, " ") != "libssl3 openssl" {
		t.Fatalf("paquets : %+v", p.Patch.Packages)
	}
	if p.Patch.Fixes.Critical != 1 || p.Patch.Fixes.High != 1 || p.Unfixed != 1 {
		t.Fatalf("décomptes : %+v, %d sans correctif", p.Patch.Fixes, p.Unfixed)
	}
	if !strings.HasPrefix(p.Explain, "Pas de nouvelle version de l'image : Vigie met à niveau 1 paquet système (openssl)") ||
		!strings.Contains(p.Explain, "2 failles (dont 1 critique et 1 élevée)") || !strings.Contains(p.Explain, "Retour à l'image d'origine") {
		t.Fatalf("%q", p.Explain)
	}
}

func TestPlanUpdateRoute(t *testing.T) {
	img := debianImage()
	img.UpdateAvailable = true
	in := planInput{img: img, inv: debianInv(), hasInv: true, enabled: true}
	// Nouvelle version pas encore analysée : elle est proposée, analysée avant l'installation.
	p := buildPlan(in)
	if p.Route != RouteUpdate || p.Update.Candidate != nil || !strings.Contains(p.Explain, "l'analyse avant de l'installer") {
		t.Fatalf("%+v", p)
	}
	// Analysée : elle corrige des failles.
	in.candidate = &Candidate{Current: img.ID, ImageID: "sha256:new", Fixed: vulns.Counts{Critical: 1, High: 1}, Added: vulns.Counts{Low: 1}}
	p = buildPlan(in)
	if p.Route != RouteUpdate || p.Update.Candidate == nil ||
		!strings.HasPrefix(p.Explain, "La nouvelle version corrige 2 failles (dont 1 critique et 1 élevée), en ajoute 1.") {
		t.Fatalf("%q", p.Explain)
	}
	// Elle ne corrige rien : la correction des paquets l'emporte.
	in.candidate = &Candidate{Current: img.ID, ImageID: "sha256:new", Added: vulns.Counts{Critical: 1}}
	p = buildPlan(in)
	if p.Route != RoutePatch || !strings.HasPrefix(p.Explain, "La nouvelle version publiée ne corrige aucune de ces failles : Vigie") {
		t.Fatalf("%s : %q", p.Route, p.Explain)
	}
	// Analyse d'une autre image en service : ignorée.
	in.candidate = &Candidate{Current: "sha256:ancienne", Fixed: vulns.Counts{High: 1}}
	if p = buildPlan(in); p.Update.Candidate != nil || p.Route != RouteUpdate {
		t.Fatalf("%+v", p.Update)
	}
	// Ne corrige rien et la correction des paquets est impossible : la mise à jour reste proposée.
	in.candidate = &Candidate{Current: img.ID}
	in.inv.Tools = nil
	if p = buildPlan(in); p.Route != RouteUpdate || !strings.HasPrefix(p.Explain, "La nouvelle version ne corrige aucune faille connue") {
		t.Fatalf("%s : %q", p.Route, p.Explain)
	}
}

func TestPlanNotFixable(t *testing.T) {
	base := func() planInput { return planInput{img: debianImage(), inv: debianInv(), hasInv: true, enabled: true} }
	cases := []struct {
		name   string
		change func(*planInput)
		route  string
		reason string // dans patch.reason
		text   string // dans explain
	}{
		{"aucune faille", func(in *planInput) { in.img.Vulns, in.img.Counts, in.img.Fixes = nil, vulns.Counts{}, nil }, RouteNone, "", "Aucune faille connue"},
		{"agent", func(in *planInput) { in.self = true }, RouteNone, "agent Vigie", "mets l'agent à jour"},
		{"mises à jour désactivées", func(in *planInput) { in.enabled = false }, RouteNone, "désactivées", "VIGIE_ALLOW_UPDATES=false"},
		{"distroless", func(in *planInput) { in.inv.Tools = nil }, RouteNone, "type distroless", "distroless"},
		{"shell sans apt", func(in *planInput) { in.inv.Tools = []string{"sh"} }, RouteNone, "type distroless", ""},
		{"épinglée", func(in *planInput) { in.pinned = true }, RouteNone, "épinglée", ""},
		{"sans commande", func(in *planInput) { in.noCommand = true }, RouteNone, "commande par défaut", ""},
		{"inventaire ancien", func(in *planInput) { in.hasInv = false }, RouteNone, "relance une analyse", ""},
		{"système inconnu", func(in *planInput) { in.img.OS, in.img.Ecosystem = "", "" }, RouteNone, "non reconnu", ""},
		{"Fedora", func(in *planInput) { in.img.OS, in.img.Ecosystem = "Fedora Linux 40", "" }, RouteNone, "Fedora Linux 40 non pris en charge", ""},
		{"binaire introuvable", func(in *planInput) { in.inv.Bins = in.inv.Bins[:1] }, RouteNone, "introuvables", ""},
	}
	for _, c := range cases {
		in := base()
		c.change(&in)
		p := buildPlan(in)
		if p.Route != c.route || p.Patch.Possible || !strings.Contains(p.Patch.Reason, c.reason) || !strings.Contains(p.Explain, c.text) {
			t.Errorf("%s : %s, raison %q, explication %q", c.name, p.Route, p.Patch.Reason, p.Explain)
		}
	}
}

func TestPlanAppAndUnfixedVulns(t *testing.T) {
	// Seulement des dépendances npm corrigeables et une faille système sans correctif.
	vs := []vulns.Vuln{
		{ID: "G1", Severity: "high", Package: "lodash", Version: "4.17.20", Fixed: "4.17.21", Ecosystem: "npm"},
		{ID: "G2", Severity: "critical", Package: "express", Version: "4.0.0", Fixed: "4.19.2", Ecosystem: "npm"},
		{ID: "V3", Severity: "medium", Package: "glibc", Version: "2.36-9", Ecosystem: "Debian:12"},
	}
	img := debianImage()
	img.Vulns, img.Counts, img.Source = vs, vulns.CountVulns(vs), "https://github.com/viren070/aiostreams"
	img.Fixes = []vulns.Fix{
		{Ecosystem: "npm", Package: "express", Fixed: "4.19.2", Counts: vulns.Counts{Critical: 1, Fixable: 1}},
		{Ecosystem: "npm", Package: "lodash", Fixed: "4.17.21", Counts: vulns.Counts{High: 1, Fixable: 1}},
	}
	p := buildPlan(planInput{img: img, inv: debianInv(), hasInv: true, enabled: true})
	if p.Route != RouteNone || p.App.Ecosystem != "npm" || p.App.Fixable != 2 || p.App.Counts.Critical != 1 || p.Source == "" {
		t.Fatalf("%+v", p)
	}
	want := "2 failles dans les dépendances de l'application (npm) ont un correctif : seule une nouvelle version de l'image peut l'intégrer. Signale-les au projet. " +
		"1 faille sans version corrigée publiée pour l'instant : rien à faire, Vigie te préviendra quand un correctif paraîtra."
	if p.Explain != want {
		t.Fatalf("%q", p.Explain)
	}
	// Distroless avec une faille système corrigeable : la raison est donnée.
	img.Fixes = append(img.Fixes, vulns.Fix{Ecosystem: "Debian:12", Package: "openssl", Fixed: "3.0.15-1", Counts: vulns.Counts{High: 1}})
	inv := debianInv()
	inv.Tools = nil
	p = buildPlan(planInput{img: img, inv: inv, hasInv: true, enabled: true})
	if !strings.HasPrefix(p.Explain, "Image sans shell ni gestionnaire de paquets (type distroless)") {
		t.Fatalf("%q", p.Explain)
	}
}

func TestOutcomeText(t *testing.T) {
	now := []vulns.Vuln{
		{ID: "a", Severity: "medium", Ecosystem: "Debian:12"},
		{ID: "b", Severity: "high", Fixed: "2", Ecosystem: "Debian:12"},
		{ID: "c", Severity: "low", Fixed: "2", Ecosystem: "npm"},
	}
	if s := outcomeText(vulns.Counts{Critical: 2, High: 5}, vulns.CountVulns(now), now); s !=
		"7 failles corrigées (dont 2 critiques), 3 restantes (1 sans correctif, 1 dont le correctif n'est pas encore dans les dépôts, 1 à corriger par une nouvelle version de l'image)" {
		t.Fatalf("%q", s)
	}
	if s := outcomeText(vulns.Counts{High: 7}, vulns.CountVulns(now[:1]), now[:1]); s != "7 failles corrigées, 1 restante (sans correctif disponible)" {
		t.Fatalf("%q", s)
	}
	if s := outcomeText(vulns.Counts{Critical: 1}, vulns.CountVulns(now[1:2]), now[1:2]); s != "1 faille corrigée (dont 1 critique), 1 restante (correctif pas encore dans les dépôts)" {
		t.Fatalf("%q", s)
	}
	if s := outcomeText(vulns.Counts{Low: 1}, vulns.Counts{}, nil); s != "1 faille corrigée, aucune restante" {
		t.Fatalf("%q", s)
	}
	c := &Candidate{Version: "1.5.0", Fixed: vulns.Counts{Critical: 1, High: 2}, Added: vulns.Counts{Low: 1}, Remaining: vulns.Counts{Low: 1, Medium: 1}}
	if s := updateOutcomeText(c); s != "version 1.5.0 installée : 3 failles corrigées (dont 1 critique), 1 nouvelle, 2 restantes" {
		t.Fatalf("%q", s)
	}
}
