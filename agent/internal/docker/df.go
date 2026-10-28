package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// DF est le détail de GET /system/df utile au suivi du stockage (sans les conteneurs).
type DF struct {
	// LayersSize : place réelle des images sur le disque (couches partagées comptées une fois).
	LayersSize int64 `json:"LayersSize"`
	Images     []struct {
		Size       int64 `json:"Size"`
		SharedSize int64 `json:"SharedSize"`
	} `json:"Images"`
	Volumes    []DFVolume `json:"Volumes"`
	BuildCache []struct {
		Size   int64 `json:"Size"`
		Shared bool  `json:"Shared"`
	} `json:"BuildCache"`
}

// DFVolume est un volume avec sa taille (Size vaut -1 si Docker ne la connaît pas, ex. pilote distant).
type DFVolume struct {
	Name      string            `json:"Name"`
	Driver    string            `json:"Driver"`
	Labels    map[string]string `json:"Labels"`
	CreatedAt string            `json:"CreatedAt"`
	UsageData *struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	} `json:"UsageData"`
}

// SystemDF mesure images, volumes et cache de construction. Avec typed (API ≥ 1.42), Docker ne calcule
// que ces trois types : la taille des conteneurs, la plus coûteuse, est laissée de côté. Le calcul des
// volumes peut prendre plusieurs minutes : pas de délai côté client, le contexte le borne.
func (c *Client) SystemDF(ctx context.Context, typed bool) (*DF, error) {
	var q url.Values
	if typed {
		q = url.Values{"type": {"image", "volume", "build-cache"}}
	}
	resp, err := c.call(ctx, c.stream, http.MethodGet, "/system/df", q, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out DF
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VolumeUsers renvoie, pour chaque volume nommé monté, les conteneurs (même arrêtés) qui l'utilisent.
func (c *Client) VolumeUsers(ctx context.Context) (map[string][]string, error) {
	var list []struct {
		Names  []string `json:"Names"`
		Mounts []struct {
			Type string `json:"Type"`
			Name string `json:"Name"`
		} `json:"Mounts"`
	}
	if err := c.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &list); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, ct := range list {
		if len(ct.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(ct.Names[0], "/")
		for _, m := range ct.Mounts {
			if m.Type == "volume" && m.Name != "" {
				out[m.Name] = append(out[m.Name], name)
			}
		}
	}
	return out, nil
}
