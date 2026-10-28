// Package cleanup estime puis libère l'espace occupé par Docker : images sans conteneur,
// volumes anonymes orphelins et cache de construction. Les conteneurs (même arrêtés) et leurs
// images ne sont jamais touchés.
package cleanup

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"

	"github.com/karelisio/vigie/agent/internal/docker"
)

type Category struct {
	Count int   `json:"count"`
	Size  int64 `json:"size"`
}

type Preview struct {
	Images     Category `json:"images"`
	Volumes    Category `json:"volumes"`
	BuildCache Category `json:"build_cache"`
	// VolumesSupported est faux sur Docker < 23 : son nettoyage des volumes toucherait aussi les volumes nommés.
	VolumesSupported bool `json:"volumes_supported"`
}

type Options struct {
	Images     bool `json:"images"`
	Volumes    bool `json:"volumes"`
	BuildCache bool `json:"build_cache"`
}

// Estimate calcule l'espace récupérable par catégorie.
func Estimate(du *docker.DiskUsage, volumesSupported bool) Preview {
	p := Preview{VolumesSupported: volumesSupported}
	for _, img := range du.Images {
		if img.Containers != 0 {
			continue
		}
		size := img.Size
		if img.SharedSize > 0 && img.SharedSize < size {
			size -= img.SharedSize
		}
		p.Images.Count++
		p.Images.Size += size
	}
	if volumesSupported {
		for _, v := range du.Volumes {
			if _, anonymous := v.Labels["com.docker.volume.anonymous"]; !anonymous || v.UsageData == nil || v.UsageData.RefCount != 0 {
				continue
			}
			p.Volumes.Count++
			if v.UsageData.Size > 0 {
				p.Volumes.Size += v.UsageData.Size
			}
		}
	}
	for _, b := range du.BuildCache {
		if !b.InUse {
			p.BuildCache.Count++
			p.BuildCache.Size += b.Size
		}
	}
	return p
}

// Run nettoie les catégories demandées et renvoie l'espace libéré.
func Run(ctx context.Context, dk *docker.Client, opt Options, volumesSupported bool, progress func(p float64, msg string)) (int64, error) {
	type step struct {
		on    bool
		kind  string
		q     url.Values
		label string
	}
	steps := []step{
		// dangling=false : toutes les images sans conteneur, pas seulement celles sans tag.
		{opt.Images, "images", url.Values{"filters": {`{"dangling":["false"]}`}}, "images inutilisées"},
		// Docker ≥ 23 : seuls les volumes anonymes orphelins sont supprimés.
		{opt.Volumes && volumesSupported, "volumes", nil, "volumes anonymes"},
		{opt.BuildCache, "build", url.Values{"all": {"true"}}, "cache de construction"},
	}
	var todo []step
	for _, s := range steps {
		if s.on {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return 0, fmt.Errorf("rien à nettoyer : choisis au moins une catégorie")
	}
	var total int64
	for i, s := range todo {
		progress(float64(i)/float64(len(todo)), "Suppression : "+s.label+"…")
		n, err := dk.Prune(ctx, s.kind, s.q)
		if err != nil {
			return total, fmt.Errorf("%s : %w", s.label, err)
		}
		total += n
	}
	return total, nil
}

// HumanBytes formate une taille en français (« 1,4 Go »).
func HumanBytes(b int64) string {
	units := []string{"o", "Ko", "Mo", "Go", "To"}
	v := float64(b)
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d o", b)
	}
	s := fmt.Sprintf("%.1f", math.Round(v*10)/10)
	return strings.Replace(strings.TrimSuffix(s, ".0"), ".", ",", 1) + " " + units[i]
}
