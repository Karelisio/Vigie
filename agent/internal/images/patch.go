package images

// Images corrigées par Vigie (agent 0.7.0) : quand l'agent met à niveau des paquets système dans une
// image dérivée (`docker commit`), il note dans ses labels l'image d'origine et ses digests publiés.
// L'image corrigée n'a pas de digest de registre : sans ces labels, la détection des mises à jour la
// prendrait pour une image locale (état inconnu), ou la remplacerait par l'image d'origine.

import (
	"encoding/json"
	"strings"
	"time"
)

// Labels posés sur une image corrigée par Vigie.
const (
	LabelFixed       = "vigie.vulnfix"              // date de la correction (RFC 3339)
	LabelBaseImage   = "vigie.vulnfix.base-image"   // ID de l'image d'origine (sha256:…)
	LabelBaseDigests = "vigie.vulnfix.base-digests" // digests publiés de l'image d'origine (dépôt@sha256:…), séparés par des virgules
	LabelPackages    = "vigie.vulnfix.packages"     // paquets mis à niveau (JSON : [{"n","f","t"}])
	LabelRef         = "vigie.vulnfix.ref"          // référence configurée sur le conteneur corrigé
)

// maxLabelPackages : paquets au plus listés dans le label (les suivants sont seulement comptés).
const maxLabelPackages = 300

// PackageChange est un paquet mis à niveau dans une image corrigée.
type PackageChange struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Patched décrit une image corrigée par Vigie.
type Patched struct {
	At          int64           `json:"at"`           // date de la correction (ms)
	BaseImage   string          `json:"base_image"`   // image d'origine (non corrigée)
	BaseDigests []string        `json:"base_digests"` // ses digests publiés (dépôt@sha256:…)
	Ref         string          `json:"ref"`
	Packages    []PackageChange `json:"packages"`
}

type labelPackage struct {
	N string `json:"n"`
	F string `json:"f"`
	T string `json:"t"`
}

// Labels renvoie les labels à poser sur l'image corrigée.
func (p *Patched) Labels() map[string]string {
	list := make([]labelPackage, 0, min(len(p.Packages), maxLabelPackages))
	for i, c := range p.Packages {
		if i >= maxLabelPackages {
			break
		}
		list = append(list, labelPackage{c.Name, c.From, c.To})
	}
	b, _ := json.Marshal(list)
	return map[string]string{
		LabelFixed:       time.UnixMilli(p.At).UTC().Format(time.RFC3339),
		LabelBaseImage:   p.BaseImage,
		LabelBaseDigests: strings.Join(p.BaseDigests, ","),
		LabelPackages:    string(b),
		LabelRef:         p.Ref,
	}
}

// PatchedFrom lit les labels d'une image : nil si elle n'a pas été corrigée par Vigie.
func PatchedFrom(labels map[string]string) *Patched {
	at, ok := labels[LabelFixed]
	base := labels[LabelBaseImage]
	if !ok || !strings.HasPrefix(base, "sha256:") {
		return nil
	}
	p := &Patched{BaseImage: base, Ref: labels[LabelRef], BaseDigests: []string{}, Packages: []PackageChange{}}
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		p.At = t.UnixMilli()
	}
	for _, d := range strings.Split(labels[LabelBaseDigests], ",") {
		if d = strings.TrimSpace(d); strings.Contains(d, "@sha256:") {
			p.BaseDigests = append(p.BaseDigests, d)
		}
	}
	var list []labelPackage
	if json.Unmarshal([]byte(labels[LabelPackages]), &list) == nil {
		for _, c := range list {
			if c.N != "" {
				p.Packages = append(p.Packages, PackageChange{c.N, c.F, c.T})
			}
		}
	}
	return p
}

// LabelsOf convertit les labels d'une configuration d'image (JSON générique) en chaînes.
func LabelsOf(cfg map[string]any) map[string]string {
	out := map[string]string{}
	raw, _ := cfg["Labels"].(map[string]any)
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// IsBase indique si l'image imageID (digests : ses RepoDigests) est l'image d'origine de p, publiée
// sur le registre de ref : même ID, ou même digest publié.
func (p *Patched) IsBase(ref, imageID string, digests []string) bool {
	if p == nil {
		return false
	}
	if imageID != "" && imageID == p.BaseImage {
		return true
	}
	d := RepoDigest(ref, digests)
	return d != "" && RepoDigest(ref, p.BaseDigests) == d
}

// EffectiveDigests renvoie les digests à comparer au registre : les RepoDigests de l'image, plus,
// pour une image corrigée par Vigie, ceux de son image d'origine. Tant que le registre publie la
// même version d'origine, aucune mise à jour n'est signalée ; une nouvelle version l'est toujours.
func EffectiveDigests(repoDigests []string, labels map[string]string) []string {
	p := PatchedFrom(labels)
	if p == nil {
		return repoDigests
	}
	out := append([]string{}, repoDigests...)
	return append(out, p.BaseDigests...)
}
