package backup

// Éléments sauvegardables : volumes nommés et dossiers de l'hôte montés dans les conteneurs (lus
// via l'API Docker depuis un conteneur qui les monte), et les données de Vigie elles-mêmes.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const vigieID = "vigie"

// Item est un élément proposé à la sauvegarde.
type Item struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // volume, bind ou vigie
	Name       string   `json:"name"` // nom du volume, chemin sur l'hôte ou « Données de Vigie »
	Key        string   `json:"key"`  // dossier de ses copies dans le stockage
	Path       string   `json:"path,omitempty"`
	Containers []string `json:"containers"`
	Size       int64    `json:"size"` // taille estimée en octets, 0 si inconnue
}

// user est un conteneur qui monte un élément.
type user struct {
	id, name string
	dest     string // point de montage dans le conteneur
	running  bool
	stopWait time.Duration
}

// source associe un élément aux conteneurs qui le montent (triés par nom).
type source struct {
	item  Item
	users []user
}

var (
	volumeRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
	anonymousRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	unsafeKeyRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// itemKey renvoie le dossier d'un élément dans le stockage (et valide son identifiant).
func itemKey(id string) (string, error) {
	switch {
	case id == vigieID:
		return vigieID, nil
	case strings.HasPrefix(id, "volume:"):
		if name := strings.TrimPrefix(id, "volume:"); volumeRe.MatchString(name) {
			return name, nil
		}
	case strings.HasPrefix(id, "bind:"):
		p := strings.TrimPrefix(id, "bind:")
		if len(p) > 1 && len(p) <= 1024 && path.IsAbs(p) && path.Clean(p) == p {
			// « /srv/app/data » → « srv_app_data »
			return unsafeKeyRe.ReplaceAllString(strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", "_"), "-"), nil
		}
	}
	return "", fmt.Errorf("élément invalide : %q", id)
}

func displayName(id string) string {
	if id == vigieID {
		return "Données de Vigie"
	}
	_, name, _ := strings.Cut(id, ":")
	return name
}

// systemPath écarte les montages techniques (pseudo-systèmes de fichiers, sockets, fuseau horaire…).
func systemPath(p string) bool {
	switch p {
	case "/", "/etc/localtime", "/etc/timezone", "/etc/hosts", "/etc/resolv.conf":
		return true
	}
	for _, prefix := range []string{"/proc", "/sys", "/dev", "/run", "/var/run"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return strings.HasSuffix(p, ".sock")
}

// stopDelay respecte le délai d'arrêt du conteneur (stop_grace_period), 10 s par défaut comme Docker.
func stopDelay(cfg map[string]any) time.Duration {
	if n, ok := cfg["StopTimeout"].(json.Number); ok {
		if s, err := n.Int64(); err == nil && s > 0 && s <= 600 {
			return time.Duration(s) * time.Second
		}
	}
	return 10 * time.Second
}

// sources recense les éléments montés dans les conteneurs, l'agent excepté.
func (s *Service) sources(ctx context.Context) (map[string]*source, error) {
	list, err := s.dk.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("liste des conteneurs : %w", err)
	}
	out := map[string]*source{}
	for _, c := range list {
		if c.ID == s.opt.SelfID {
			continue
		}
		in, err := s.dk.InspectFull(ctx, c.ID)
		if err != nil {
			continue // supprimé entre-temps
		}
		for _, m := range in.Mounts {
			var id, kind, name string
			switch {
			case m.Type == "volume" && volumeRe.MatchString(m.Name) && !anonymousRe.MatchString(m.Name):
				id, kind, name = "volume:"+m.Name, "volume", m.Name
			case m.Type == "bind" && path.IsAbs(m.Source) && !systemPath(m.Source):
				id, kind, name = "bind:"+m.Source, "bind", m.Source
			default:
				continue
			}
			key, err := itemKey(id)
			if err != nil {
				continue
			}
			src := out[id]
			if src == nil {
				src = &source{item: Item{ID: id, Kind: kind, Name: name, Key: key}}
				out[id] = src
			}
			src.users = append(src.users, user{
				id: in.ID, name: in.CleanName(), dest: m.Destination,
				running: in.State.Running || in.State.Restarting, stopWait: stopDelay(in.Config),
			})
		}
	}
	for _, src := range out {
		sort.Slice(src.users, func(i, j int) bool { return src.users[i].name < src.users[j].name })
		src.item.Path = src.users[0].dest
		for _, u := range src.users {
			src.item.Containers = append(src.item.Containers, u.name)
		}
	}
	return out, nil
}

// Items liste les éléments sauvegardables : données de Vigie, volumes puis dossiers, par nom.
// Taille : celle des volumes selon Docker, sinon celle de la dernière copie.
func (s *Service) Items(ctx context.Context) ([]Item, error) {
	srcs, err := s.sources(ctx)
	if err != nil {
		return nil, err
	}
	volumes := map[string]int64{}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if du, err := s.dk.DiskUsage(dctx); err == nil {
		for _, v := range du.Volumes {
			if v.UsageData != nil && v.UsageData.Size > 0 {
				volumes[v.Name] = v.UsageData.Size
			}
		}
	}
	history := s.History(ctx)
	out := []Item{{ID: vigieID, Kind: vigieID, Name: displayName(vigieID), Key: vigieID, Containers: []string{}, Size: dirSize(s.opt.DataDir)}}
	var rest []Item
	for _, src := range srcs {
		it := src.item
		if it.Kind == "volume" {
			it.Size = volumes[it.Name]
		}
		if it.Size == 0 {
			it.Size = lastRaw(history, it.ID)
		}
		rest = append(rest, it)
	}
	sort.Slice(rest, func(i, j int) bool {
		if rest[i].Kind != rest[j].Kind {
			return rest[i].Kind == "volume"
		}
		return rest[i].Name < rest[j].Name
	})
	return append(out, rest...), nil
}

// dirSize additionne la taille des fichiers d'un dossier (sans sous-dossiers).
func dirSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if fi, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
	}
	return n
}
