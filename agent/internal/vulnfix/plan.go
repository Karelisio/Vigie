package vulnfix

// Choix de la voie de correction de chaque image, expliqué à l'utilisateur :
//   - update : une nouvelle version de l'image est publiée ; Vigie l'analyse avant de l'installer ;
//   - patch  : Vigie met à niveau les paquets système vulnérables dans une image dérivée ;
//   - none   : rien à faire (pas de faille, pas de version corrigée connue, image non modifiable…).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

// Voies de correction.
const (
	RouteUpdate = "update"
	RoutePatch  = "patch"
	RouteNone   = "none"
)

// Plan est la correction proposée pour une image.
type Plan struct {
	ImageID    string               `json:"image_id"`
	Ref        string               `json:"ref"`
	Containers []vulns.ContainerRef `json:"containers"`
	OS         string               `json:"os"`
	Counts     vulns.Counts         `json:"counts"` // failles actuelles
	Route      string               `json:"route"`  // update, patch, none
	Title      string               `json:"title"`  // la voie en quelques mots
	Explain    string               `json:"explain"`
	Update     UpdateRoute          `json:"update"`
	Patch      PatchRoute           `json:"patch"`
	App        AppVulns             `json:"app"`
	Unfixed    int                  `json:"unfixed"` // failles sans version corrigée connue
	Patched    *images.Patched      `json:"patched,omitempty"`
	Last       *Outcome             `json:"last,omitempty"` // bilan de la dernière correction de cette image
	Source     string               `json:"source,omitempty"`
	// Declined : un conteneur est revenu à l'image d'origine après une correction ; la correction
	// automatique de nuit ne la refait pas (le bouton reste possible).
	Declined bool `json:"declined"`
	// Self : image de l'agent Vigie, jamais corrigée par lui-même.
	Self bool `json:"self"`
}

// UpdateRoute : nouvelle version de l'image.
type UpdateRoute struct {
	Available bool       `json:"available"`
	Candidate *Candidate `json:"candidate,omitempty"` // analyse de la nouvelle version, si faite
}

// Candidate compare la nouvelle version (téléchargée, pas installée) à l'image en service.
type Candidate struct {
	Current    string       `json:"current"` // image en service
	ImageID    string       `json:"image_id"`
	Version    string       `json:"version,omitempty"`
	AnalyzedAt int64        `json:"analyzed_at"`
	Fixed      vulns.Counts `json:"fixed"`     // failles actuelles absentes de la nouvelle version
	Added      vulns.Counts `json:"added"`     // failles nouvelles
	Remaining  vulns.Counts `json:"remaining"` // failles de la nouvelle version
}

// PatchRoute : correction des paquets système par Vigie.
type PatchRoute struct {
	Possible bool          `json:"possible"`
	Manager  string        `json:"manager,omitempty"` // apt, apk
	Packages []PackagePlan `json:"packages"`
	Fixes    vulns.Counts  `json:"fixes"`            // failles corrigées attendues
	Reason   string        `json:"reason,omitempty"` // pourquoi Vigie ne peut pas corriger cette image
}

// PackagePlan est un paquet source (ou d'origine) vulnérable à mettre à niveau.
type PackagePlan struct {
	Name     string       `json:"name"`
	Version  string       `json:"version"`  // installée
	Fixed    string       `json:"fixed"`    // version corrigée (au moins)
	Binaries []string     `json:"binaries"` // paquets installés mis à niveau
	Counts   vulns.Counts `json:"counts"`
}

// AppVulns : failles des dépendances de l'application (npm), que seule une nouvelle image corrige.
type AppVulns struct {
	Ecosystem string       `json:"ecosystem,omitempty"`
	Counts    vulns.Counts `json:"counts"`
	Fixable   int          `json:"fixable"`
}

// planInput rassemble ce que le choix de la voie demande.
type planInput struct {
	img       vulns.Image
	inv       vulns.Inventory
	hasInv    bool
	self      bool       // image utilisée par l'agent
	enabled   bool       // mises à jour autorisées (VIGIE_ALLOW_UPDATES)
	pinned    bool       // tous ses conteneurs désignent l'image par digest
	noCommand bool       // image sans Entrypoint ni Cmd
	candidate *Candidate // analyse de la nouvelle version, nil si aucune
	declined  bool
}

// buildPlan choisit la voie de correction d'une image et l'explique.
func buildPlan(in planInput) Plan {
	img := in.img
	p := Plan{
		ImageID: img.ID, Ref: img.Ref, Containers: img.Containers, OS: img.OS, Counts: img.Counts,
		Patched: img.Patched, Source: img.Source, Declined: in.declined, Self: in.self,
		Unfixed: img.Counts.Total() - img.Counts.Fixable,
		Patch:   PatchRoute{Packages: []PackagePlan{}},
	}
	if p.Containers == nil {
		p.Containers = []vulns.ContainerRef{}
	}
	sysFixes := map[string]vulns.Fix{}
	for _, f := range img.Fixes {
		if vulns.SystemPackages(f.Ecosystem) {
			sysFixes[f.Package] = f
		} else {
			p.App.Ecosystem = f.Ecosystem
			p.App.Fixable += f.Counts.Total()
		}
	}
	for _, v := range img.Vulns {
		if !vulns.SystemPackages(v.Ecosystem) {
			p.App.Ecosystem = v.Ecosystem
			p.App.Counts = p.App.Counts.Plus(vulns.CountVulns([]vulns.Vuln{v}))
		}
	}
	p.Patch = patchRoute(in, sysFixes)
	p.Update = UpdateRoute{Available: img.UpdateAvailable && !in.self && in.enabled && !in.pinned}
	if p.Update.Available && in.candidate != nil && in.candidate.Current == img.ID {
		p.Update.Candidate = in.candidate
	}

	switch {
	case img.Counts.Total() == 0:
		p.Route, p.Title, p.Explain = RouteNone, "Rien à corriger", "Aucune faille connue dans cette image."
	case in.self:
		p.Route, p.Title = RouteNone, "Image de l'agent Vigie"
		p.Explain = "Vigie ne modifie pas sa propre image : mets l'agent à jour depuis Réglages quand une nouvelle version paraît."
	case !in.enabled:
		p.Route, p.Title = RouteNone, "Corrections désactivées"
		p.Explain = "Les mises à jour sont désactivées sur cet agent (VIGIE_ALLOW_UPDATES=false) : Vigie ne peut ni mettre à jour ni corriger cette image."
	case p.Update.Available && (p.Update.Candidate == nil || p.Update.Candidate.Fixed.Total() > 0 || !p.Patch.Possible):
		p.Route, p.Title = RouteUpdate, "Installer la nouvelle version"
		p.Explain = updateExplain(p.Update.Candidate)
	case p.Patch.Possible:
		p.Route, p.Title = RoutePatch, "Corriger les paquets système"
		p.Explain = patchExplain(p.Patch)
		if p.Update.Available {
			p.Explain = "La nouvelle version publiée ne corrige aucune de ces failles : " + lowerFirst(p.Explain)
		} else {
			p.Explain = "Pas de nouvelle version de l'image : " + lowerFirst(p.Explain)
		}
	default:
		p.Route, p.Title, p.Explain = RouteNone, "Rien à faire pour l'instant", noneExplain(p, len(sysFixes) > 0)
	}
	return p
}

func patchRoute(in planInput, sysFixes map[string]vulns.Fix) PatchRoute {
	img := in.img
	r := PatchRoute{Packages: []PackagePlan{}, Manager: managerFor(img.Ecosystem)}
	switch {
	case in.self:
		r.Reason = "Image de l'agent Vigie."
	case !in.enabled:
		r.Reason = "Mises à jour désactivées sur cet agent (VIGIE_ALLOW_UPDATES=false)."
	case in.pinned:
		r.Reason = "Image désignée par son digest (épinglée) : Vigie ne peut pas lui substituer une image corrigée."
	case img.OS == "":
		r.Reason = "Système de l'image non reconnu (image « scratch » ou sans os-release) : Vigie ne peut pas modifier ses paquets."
	case r.Manager == "":
		r.Reason = fmt.Sprintf("Système %s non pris en charge : Vigie sait corriger Debian, Ubuntu et Alpine.", img.OS)
	case !in.hasInv:
		r.Reason = "Inventaire des paquets à relire : relance une analyse."
	case !in.inv.HasTool("sh") || !in.inv.HasTool(toolFor(r.Manager)):
		r.Reason = "Image sans shell ni gestionnaire de paquets (type distroless) : Vigie ne peut pas mettre ses paquets à niveau. Seule une nouvelle version de l'image peut corriger ses failles."
	case in.noCommand:
		r.Reason = "Image sans commande par défaut (ni ENTRYPOINT ni CMD) : Vigie ne peut pas en dériver une image fidèle."
	case len(sysFixes) == 0:
		// Aucune version corrigée connue pour les paquets système : rien à mettre à niveau.
		return r
	}
	if r.Reason != "" {
		return r
	}
	bins := in.inv.Binaries()
	names := make([]string, 0, len(sysFixes))
	for n := range sysFixes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f := sysFixes[n]
		args, _ := selectBinaries(r.Manager, bins, map[string]bool{n: true}, nil)
		if len(args) == 0 {
			continue // binaire introuvable ou nom invalide
		}
		r.Packages = append(r.Packages, PackagePlan{Name: n, Version: f.Version, Fixed: f.Fixed, Binaries: args, Counts: f.Counts})
		r.Fixes = r.Fixes.Plus(f.Counts)
	}
	sort.SliceStable(r.Packages, func(i, j int) bool {
		a, b := r.Packages[i].Counts, r.Packages[j].Counts
		for _, p := range [][2]int{{a.Critical, b.Critical}, {a.High, b.High}, {a.Total(), b.Total()}} {
			if p[0] != p[1] {
				return p[0] > p[1]
			}
		}
		return r.Packages[i].Name < r.Packages[j].Name
	})
	if len(r.Packages) == 0 {
		r.Reason = "Paquets vulnérables introuvables dans la base de paquets de l'image : relance une analyse."
		return r
	}
	r.Possible = true
	return r
}

// severityText : « 7 failles (dont 2 critiques) ».
func severityText(c vulns.Counts) string {
	s := plural(c.Total(), "faille", "failles")
	switch {
	case c.Critical > 0 && c.High > 0:
		s += fmt.Sprintf(" (dont %s et %s)", plural(c.Critical, "critique", "critiques"), plural(c.High, "élevée", "élevées"))
	case c.Critical > 0:
		s += " (dont " + plural(c.Critical, "critique", "critiques") + ")"
	case c.High > 0:
		s += " (dont " + plural(c.High, "élevée", "élevées") + ")"
	}
	return s
}

func updateExplain(c *Candidate) string {
	if c == nil {
		return "Une nouvelle version de l'image est publiée. Vigie la télécharge et l'analyse avant de l'installer, puis recrée le conteneur à l'identique (retour à la version précédente possible)."
	}
	s := "La nouvelle version corrige " + severityText(c.Fixed)
	if c.Added.Total() > 0 {
		s += fmt.Sprintf(", en ajoute %d", c.Added.Total())
	}
	s += "."
	if c.Fixed.Total() == 0 {
		s = "La nouvelle version ne corrige aucune faille connue de cette image"
		if c.Added.Total() > 0 {
			s += " et en ajoute " + fmt.Sprint(c.Added.Total())
		}
		s += "."
	}
	return s + " Installation comme une mise à jour, avec retour à la version précédente possible."
}

func patchExplain(r PatchRoute) string {
	names := make([]string, 0, 3)
	for i, pk := range r.Packages {
		if i == 3 {
			names = append(names, "…")
			break
		}
		names = append(names, pk.Name)
	}
	return fmt.Sprintf("Vigie met à niveau %s (%s) dans une image dérivée et recrée le conteneur, pour corriger %s si les dépôts de l'image proposent déjà les versions corrigées. Retour à l'image d'origine possible.",
		plural(len(r.Packages), "paquet système", "paquets système"), strings.Join(names, ", "), severityText(r.Fixes))
}

func noneExplain(p Plan, sysFixable bool) string {
	var parts []string
	if p.Patch.Reason != "" && sysFixable {
		parts = append(parts, p.Patch.Reason)
	}
	if p.App.Fixable > 0 {
		s := fmt.Sprintf("%s dans les dépendances de l'application (%s) ont un correctif : seule une nouvelle version de l'image peut l'intégrer.",
			capitalize(plural(p.App.Fixable, "faille", "failles")), p.App.Ecosystem)
		if p.App.Fixable == 1 {
			s = fmt.Sprintf("1 faille dans les dépendances de l'application (%s) a un correctif : seule une nouvelle version de l'image peut l'intégrer.", p.App.Ecosystem)
		}
		if p.Source != "" && p.App.Fixable == 1 {
			s += " Signale-la au projet."
		} else if p.Source != "" {
			s += " Signale-les au projet."
		}
		parts = append(parts, s)
	}
	if p.Unfixed > 0 {
		parts = append(parts, fmt.Sprintf("%s sans version corrigée publiée pour l'instant : rien à faire, Vigie te préviendra quand un correctif paraîtra.",
			capitalize(plural(p.Unfixed, "faille", "failles"))))
	}
	if len(parts) == 0 && p.Patch.Reason != "" {
		parts = append(parts, p.Patch.Reason)
	}
	if len(parts) == 0 {
		parts = append(parts, "Aucune correction disponible pour l'instant.")
	}
	return strings.Join(parts, " ")
}

func lowerFirst(s string) string {
	if strings.HasPrefix(s, "Vigie") {
		return s // nom propre
	}
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToLower(string(r[0])) + string(r[1:])
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func plural(n int, one, many string) string {
	if n == 1 {
		return strings.TrimSpace("1 " + one)
	}
	return strings.TrimSpace(fmt.Sprintf("%d %s", n, many))
}
