package vulnfix

// Commandes exécutées dans le conteneur temporaire : mise à niveau des seuls paquets binaires issus
// des paquets sources (Debian, Ubuntu) ou d'origine (Alpine) vulnérables. Les noms viennent de la
// base de paquets de l'image : ils sont validés (caractères permis par dpkg et apk) puis passés entre
// apostrophes, jamais interprétés par le shell.

import (
	"regexp"
	"sort"
	"strings"

	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

var (
	// Noms de paquets Debian : minuscules, chiffres, « + », « - », « . » (2 caractères au moins).
	debName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	debArch = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// Noms de paquets Alpine.
	apkName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9+._-]*$`)
)

// managerFor renvoie le gestionnaire de paquets d'un écosystème OSV (« » s'il n'est pas pris en charge).
func managerFor(eco string) string {
	base, _, _ := strings.Cut(eco, ":")
	switch base {
	case "Debian", "Ubuntu":
		return "apt"
	case "Alpine":
		return "apk"
	}
	return ""
}

// toolFor : outil requis dans l'image pour ce gestionnaire.
func toolFor(manager string) string {
	if manager == "apk" {
		return "apk"
	}
	return "apt-get"
}

// shellQuote place s entre apostrophes pour sh ; une apostrophe de s ferme la chaîne, est échappée
// par une barre oblique inverse, puis la chaîne reprend.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// validName indique si le nom (avec « :arch » éventuel pour apt) est acceptable pour ce gestionnaire.
func validName(manager, name string) bool {
	if manager == "apk" {
		return apkName.MatchString(name)
	}
	n, arch, hasArch := strings.Cut(name, ":")
	return debName.MatchString(n) && (!hasArch || debArch.MatchString(arch))
}

// nativeArch : architecture du paquet dpkg (toujours native), sinon la plus fréquente.
func nativeArch(bins []vulns.Binary) string {
	count := map[string]int{}
	for _, b := range bins {
		if b.Name == "dpkg" && b.Arch != "" && b.Arch != "all" {
			return b.Arch
		}
		if b.Arch != "" && b.Arch != "all" {
			count[b.Arch]++
		}
	}
	best := ""
	for a, n := range count {
		if n > count[best] || (n == count[best] && a < best) {
			best = a
		}
	}
	return best
}

// selectBinaries renvoie les paquets binaires installés issus des paquets sources ou d'origine
// sources, plus les binaires extra (déjà mis à niveau par une correction précédente), sous la forme
// attendue par le gestionnaire (« nom:arch » pour un paquet Debian d'une architecture étrangère).
// rejected : noms écartés car invalides.
func selectBinaries(manager string, bins []vulns.Binary, sources map[string]bool, extra []string) (args, rejected []string) {
	native := ""
	if manager == "apt" {
		native = nativeArch(bins)
	}
	seen := map[string]bool{}
	installed := map[string]bool{}
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if !validName(manager, name) {
			rejected = append(rejected, name)
			return
		}
		args = append(args, name)
	}
	for _, b := range bins {
		installed[b.Name] = true
		if !sources[b.Source] {
			continue
		}
		name := b.Name
		if manager == "apt" && b.Arch != "" && b.Arch != "all" && native != "" && b.Arch != native {
			name += ":" + b.Arch
		}
		add(name)
	}
	for _, name := range extra {
		base, _, _ := strings.Cut(name, ":")
		if installed[base] {
			add(name)
		}
	}
	sort.Strings(args)
	sort.Strings(rejected)
	return args, rejected
}

// upgradeScript construit la commande sh qui met à niveau les paquets args (déjà validés).
func upgradeScript(manager string, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	list := strings.Join(quoted, " ")
	var b strings.Builder
	b.WriteString("set -eu\n")
	b.WriteString("PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; export PATH\n")
	if manager == "apk" {
		b.WriteString("echo 'vigie: apk upgrade'\n")
		b.WriteString("apk upgrade --no-cache " + list + "\n")
	} else {
		b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
		// Les listes de paquets ne sont gardées que si l'image les contenait déjà.
		b.WriteString("keep=0\n")
		b.WriteString(`for f in /var/lib/apt/lists/*; do if [ -f "$f" ] && [ "${f##*/}" != lock ]; then keep=1; fi; done` + "\n")
		b.WriteString("echo 'vigie: apt-get update'\n")
		b.WriteString("apt-get update -q\n")
		b.WriteString("echo 'vigie: apt-get install --only-upgrade'\n")
		b.WriteString("apt-get install -y -q --only-upgrade --no-install-recommends --no-remove " +
			"-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold " + list + "\n")
		b.WriteString("apt-get clean\n")
		b.WriteString(`if [ "$keep" = 0 ]; then rm -rf /var/lib/apt/lists/*; fi` + "\n")
	}
	b.WriteString("echo 'vigie: ok'\n")
	return b.String()
}

// diffBins compare les paquets avant et après la mise à niveau : versions changées, ajouts, retraits.
func diffBins(before, after []vulns.Binary) []images.PackageChange {
	key := func(b vulns.Binary) string { return b.Name + ":" + b.Arch }
	old := map[string]vulns.Binary{}
	for _, b := range before {
		old[key(b)] = b
	}
	var out []images.PackageChange
	seen := map[string]bool{}
	for _, b := range after {
		k := key(b)
		seen[k] = true
		o, ok := old[k]
		switch {
		case !ok:
			out = append(out, images.PackageChange{Name: b.Name, To: b.Version})
		case o.Version != b.Version:
			out = append(out, images.PackageChange{Name: b.Name, From: o.Version, To: b.Version})
		}
	}
	for _, b := range before {
		if !seen[key(b)] {
			out = append(out, images.PackageChange{Name: b.Name, From: b.Version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// mergeChanges cumule les paquets d'une correction précédente (prev) et de la nouvelle : une image
// corrigée à nouveau sans repartir de l'origine garde la liste complète depuis l'origine.
func mergeChanges(prev, next []images.PackageChange) []images.PackageChange {
	index := map[string]int{}
	out := append([]images.PackageChange{}, prev...)
	for i, c := range out {
		index[c.Name] = i
	}
	for _, c := range next {
		if i, ok := index[c.Name]; ok {
			out[i].To = c.To
			continue
		}
		index[c.Name] = len(out)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// describeChanges résume les paquets mis à niveau : « openssl 3.0.11 → 3.0.15, zlib… (+4) ».
func describeChanges(list []images.PackageChange, max int) string {
	var parts []string
	for i, c := range list {
		if i >= max {
			parts = append(parts, "…")
			break
		}
		switch {
		case c.From == "":
			parts = append(parts, c.Name+" (ajouté)")
		case c.To == "":
			parts = append(parts, c.Name+" (retiré)")
		default:
			parts = append(parts, c.Name+" "+c.From+" → "+c.To)
		}
	}
	return strings.Join(parts, ", ")
}
