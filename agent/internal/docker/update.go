package docker

// Opérations nécessaires aux mises à jour d'images : téléchargement, inspection complète,
// création, renommage et suppression de conteneurs. Elles ne sont utilisées que par le
// paquet updater, qui recrée un conteneur à l'identique avec l'image déjà configurée.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIError est une erreur renvoyée par le démon Docker.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("Docker %d : %s", e.Code, e.Message) }

func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.Code == http.StatusNotFound
}

// call envoie une requête avec un corps JSON facultatif et renvoie l'erreur détaillée du démon.
func (c *Client) call(ctx context.Context, cl *http.Client, method, path string, q url.Values, body any) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := cl.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		return nil, &APIError{Code: resp.StatusCode, Message: e.Message}
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.call(ctx, c.http, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	return dec.Decode(out)
}

// ContainerJSON est l'inspection complète d'un conteneur. Config et HostConfig restent
// génériques pour être recopiés sans rien perdre, quelle que soit la version de Docker.
type ContainerJSON struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	Image string `json:"Image"` // ID de l'image (sha256:…)
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Restarting bool   `json:"Restarting"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config          map[string]any `json:"Config"`
	HostConfig      map[string]any `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]map[string]any `json:"Networks"`
	} `json:"NetworkSettings"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

func (c ContainerJSON) CleanName() string { return strings.TrimPrefix(c.Name, "/") }

func (c *Client) InspectFull(ctx context.Context, id string) (*ContainerJSON, error) {
	var out ContainerJSON
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImageDetails est l'inspection d'une image locale.
type ImageDetails struct {
	ID          string         `json:"Id"`
	RepoDigests []string       `json:"RepoDigests"`
	Config      map[string]any `json:"Config"`
	Created     string         `json:"Created"` // date de construction (RFC 3339)
}

// Label renvoie un label de l'image ("" s'il est absent).
func (i *ImageDetails) Label(key string) string {
	labels, _ := i.Config["Labels"].(map[string]any)
	v, _ := labels[key].(string)
	return v
}

func (c *Client) ImageDetails(ctx context.Context, ref string) (*ImageDetails, error) {
	var out ImageDetails
	// Comme la CLI Docker : la référence (avec ses « / ») va telle quelle dans le chemin.
	if err := c.do(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PullEvent est une ligne du flux de progression de POST /images/create.
type PullEvent struct {
	Status   string `json:"status"`
	ID       string `json:"id"`
	Progress struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
	Error string `json:"error"`
}

// SplitRef sépare « dépôt:tag ». Sans tag, Docker téléchargerait tous les tags : on force « latest ».
func SplitRef(ref string) (repo, tag string) {
	if strings.Contains(ref, "@") {
		return ref, ""
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// Pull télécharge l'image ref (registres publics) et signale chaque ligne de progression.
func (c *Client) Pull(ctx context.Context, ref string, fn func(PullEvent)) error {
	repo, tag := SplitRef(ref)
	q := url.Values{"fromImage": {repo}}
	if tag != "" {
		q.Set("tag", tag)
	}
	resp, err := c.call(ctx, c.stream, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var ev PullEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return errors.New(ev.Error)
		}
		if fn != nil {
			fn(ev)
		}
	}
	return sc.Err()
}

// Create crée un conteneur et renvoie son ID.
func (c *Client) Create(ctx context.Context, name string, body any) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) Rename(ctx context.Context, id, name string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename", url.Values{"name": {name}}, nil, nil)
}

// Remove supprime un conteneur sans ses volumes.
func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	return c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {strconv.FormatBool(force)}}, nil, nil)
}

// RemoveImage supprime une image locale ; Docker refuse si un conteneur l'utilise encore.
func (c *Client) RemoveImage(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/images/"+id, nil, nil, nil)
}

func (c *Client) ConnectNetwork(ctx context.Context, network, container string, endpoint any) error {
	body := map[string]any{"Container": container, "EndpointConfig": endpoint}
	return c.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", nil, body, nil)
}

// LogTail renvoie les dernières lignes de logs (diagnostic d'un démarrage raté).
func (c *Client) LogTail(ctx context.Context, id string, n int) []string {
	var lines []string
	_ = c.Logs(ctx, id, n, false, false, func(l LogLine) { lines = append(lines, l.Line) })
	return lines
}

// StopWait arrête un conteneur sans limite de durée côté client (le délai d'arrêt peut dépasser 30 s).
func (c *Client) StopWait(ctx context.Context, id string, timeout time.Duration) error {
	resp, err := c.call(ctx, c.stream, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop",
		url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}, nil)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// DiskUsage est un sous-ensemble de GET /system/df.
type DiskUsage struct {
	Images []struct {
		ID         string `json:"Id"`
		Size       int64  `json:"Size"`
		SharedSize int64  `json:"SharedSize"`
		Containers int64  `json:"Containers"`
	} `json:"Images"`
	Volumes []struct {
		Name      string            `json:"Name"`
		Labels    map[string]string `json:"Labels"`
		UsageData *struct {
			Size     int64 `json:"Size"`
			RefCount int64 `json:"RefCount"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size  int64 `json:"Size"`
		InUse bool  `json:"InUse"`
	} `json:"BuildCache"`
}

// DiskUsage peut être long (taille des volumes) : pas de délai côté client.
func (c *Client) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	resp, err := c.call(ctx, c.stream, http.MethodGet, "/system/df", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out DiskUsage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Prune supprime les éléments inutilisés d'un type (images, volumes, build) et renvoie l'espace libéré.
func (c *Client) Prune(ctx context.Context, kind string, q url.Values) (int64, error) {
	resp, err := c.call(ctx, c.stream, http.MethodPost, "/"+kind+"/prune", q, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.SpaceReclaimed, nil
}

// AtLeast indique si l'API du démon est au moins major.minor.
func (v *Version) AtLeast(major, minor int) bool {
	var a, b int
	if _, err := fmt.Sscanf(v.APIVersion, "%d.%d", &a, &b); err != nil {
		return false
	}
	return a > major || (a == major && b >= minor)
}
