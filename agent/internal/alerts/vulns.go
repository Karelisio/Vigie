package alerts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/karelisio/vigie/agent/internal/vulns"
)

// vulnKey mémorise en base les failles déjà signalées : ID d'image → « faille|paquet ».
const vulnKey = "vuln_alerts"

// vulnQualifies : faille au moins du niveau choisi (critical, ou high qui inclut critical) et corrigée
// dans une version plus récente du paquet.
func vulnQualifies(v vulns.Vuln, level string) bool {
	if v.Fixed == "" {
		return false
	}
	switch level {
	case "critical":
		return v.Severity == "critical"
	case "high":
		return v.Severity == "critical" || v.Severity == "high"
	}
	return false
}

// OnVulns prévient, après chaque analyse, des failles corrigeables du niveau choisi : une alerte groupée
// par image (et ses conteneurs), une seule fois par faille et par image, même après un redémarrage.
func (e *Engine) OnVulns(r vulns.Report) {
	set := e.set.Get()
	if !set.VulnScan || set.VulnAlert == "off" {
		return
	}
	sent := map[string][]string{}
	_ = json.Unmarshal([]byte(e.st.Get(e.ctx, vulnKey)), &sent)
	next := map[string][]string{}
	var notes []Notification
	for _, img := range r.Images {
		known := map[string]bool{}
		for _, k := range sent[img.ID] {
			known[k] = true
		}
		// Image corrigée par Vigie (agent 0.7.0) : les failles restantes ont déjà été signalées pour
		// son image d'origine ; elles ne le sont pas une seconde fois.
		base := ""
		if img.Patched != nil {
			base = img.Patched.BaseImage
			for _, k := range sent[base] {
				known[k] = true
			}
		}
		var fresh []vulns.Vuln
		for _, v := range img.Vulns {
			if vulnQualifies(v, set.VulnAlert) && !known[v.ID+"|"+v.Package] {
				fresh = append(fresh, v)
			}
		}
		var names []string
		id := ""
		for _, c := range img.Containers {
			if !e.cfg.Ignore[c.Name] && !set.IsMuted(c.Name) {
				names = append(names, c.Name)
				if id == "" {
					id = c.ID
				}
			}
		}
		keys := append([]string{}, sent[img.ID]...)
		if len(fresh) > 0 && len(names) > 0 {
			// Conteneurs en sourdine ou ignorés : rien n'est noté, l'alerte viendra s'ils sont réactivés.
			for _, v := range fresh {
				keys = append(keys, v.ID+"|"+v.Package)
			}
			notes = append(notes, vulnNotification(img, names, id, fresh))
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			next[img.ID] = keys
		}
		if base != "" {
			// Notées aussi pour l'image d'origine, gardées tant qu'une image corrigée en dérive :
			// une correction refaite (nouvel ID d'image) ne les signale pas de nouveau.
			if all := mergeKeys(next[base], append(append([]string{}, sent[base]...), keys...)); len(all) > 0 {
				next[base] = all
			}
		}
	}
	raw, _ := json.Marshal(next)
	if err := e.st.Set(e.ctx, vulnKey, string(raw)); err != nil {
		e.log.Warn("alertes de failles", "erreur", err)
		return
	}
	for _, n := range notes {
		e.dispatch(n, false, true)
	}
}

// mergeKeys réunit deux listes triées de clés, sans doublon.
func mergeKeys(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range append(append([]string{}, a...), b...) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func vulnNotification(img vulns.Image, names []string, id string, list []vulns.Vuln) Notification {
	critical, high := 0, 0
	for _, v := range list {
		if v.Severity == "critical" {
			critical++
		} else {
			high++
		}
	}
	var what string
	switch {
	case high == 0 && critical == 1:
		what = "1 faille critique corrigée"
	case high == 0:
		what = fmt.Sprintf("%d failles critiques corrigées", critical)
	case critical == 0 && high == 1:
		what = "1 faille de gravité élevée corrigée"
	case critical == 0:
		what = fmt.Sprintf("%d failles de gravité élevée corrigées", high)
	default:
		what = fmt.Sprintf("%d failles (%d critique%s) corrigées", critical+high, critical, vulnPlural(critical))
	}
	target := strings.Join(names, ", ")
	n := Notification{
		Kind: "vuln", Severity: "warning", Target: target, ContainerID: id,
		Title: target + " : " + what + " dans une version plus récente",
	}
	if critical > 0 {
		n.Severity = "critical"
	}
	// Un paquet par ligne logique : « openssl 3.0.11-1 → 3.0.15-1 (CVE-2024-5535) ».
	var parts []string
	seen := map[string]bool{}
	for _, v := range list {
		if seen[v.Package] {
			continue
		}
		seen[v.Package] = true
		ref := v.ID
		if len(v.Aliases) > 0 {
			ref = v.Aliases[0]
		}
		parts = append(parts, fmt.Sprintf("%s %s → %s (%s)", v.Package, v.Version, v.Fixed, ref))
	}
	body := strings.Join(parts, " · ")
	if len(parts) > 3 {
		body = strings.Join(parts[:3], " · ") + fmt.Sprintf(" · et %d autre(s) paquet(s)", len(parts)-3)
	}
	system := false
	for _, v := range list {
		system = system || vulns.SystemPackages(v.Ecosystem)
	}
	switch {
	case img.UpdateAvailable:
		body += ". Une nouvelle image est disponible : mets-la à jour depuis Vigie."
	case system:
		// Agent 0.7.0 : paquets système corrigeables par Vigie dans une image dérivée.
		body += ". Vigie peut mettre ces paquets à niveau : Failles connues › Corriger."
	default:
		body += ". Mets à jour l'image si une nouvelle version est disponible."
	}
	n.Body = body
	return n
}

func vulnPlural(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}
