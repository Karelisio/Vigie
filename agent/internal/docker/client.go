// Package docker est un client minimal de l'API Docker Engine.
// Seules les opérations nécessaires à Vigie sont implémentées : lecture
// (liste, inspection, stats, logs, événements) et start/stop/restart.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	http   *http.Client
	stream *http.Client
	base   string
}

var ErrNotFound = errors.New("conteneur introuvable")

func New(host string) (*Client, error) {
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("DOCKER_HOST invalide : %w", err)
	}
	var dial func(ctx context.Context, _, _ string) (net.Conn, error)
	base := "http://docker"
	switch u.Scheme {
	case "unix":
		path := u.Path
		dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
	case "tcp", "http":
		base = "http://" + u.Host
	default:
		return nil, fmt.Errorf("DOCKER_HOST : schéma %q non supporté", u.Scheme)
	}
	tr := &http.Transport{MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 60 * time.Second}
	if dial != nil {
		tr.DialContext = dial
	}
	return &Client{
		http:   &http.Client{Transport: tr, Timeout: 30 * time.Second},
		stream: &http.Client{Transport: tr},
		base:   base,
	}, nil
}

func (c *Client) req(ctx context.Context, cl *http.Client, method, path string, q url.Values) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cl.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		return nil, fmt.Errorf("docker %s %s : %d %s", method, path, resp.StatusCode, e.Message)
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.req(ctx, c.http, http.MethodGet, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// Container est un élément de GET /containers/json.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Ports   []struct {
		IP          string `json:"IP"`
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

func (c Container) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:12]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// Health renvoie l'état de santé déduit du champ Status ("healthy", "unhealthy", "starting" ou "").
func (c Container) Health() string {
	s := c.Status
	switch {
	case strings.Contains(s, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(s, "(healthy)"):
		return "healthy"
	case strings.Contains(s, "(health: starting)"):
		return "starting"
	}
	return ""
}

func (c *Client) List(ctx context.Context) ([]Container, error) {
	var out []Container
	err := c.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &out)
	return out, err
}

type Inspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Image   string `json:"Image"`
	State   struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		Dead       bool   `json:"Dead"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				Start    string `json:"Start"`
				End      string `json:"End"`
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Tty    bool              `json:"Tty"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		Memory   int64 `json:"Memory"`
		NanoCPUs int64 `json:"NanoCpus"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

func (c *Client) Inspect(ctx context.Context, id string) (*Inspect, error) {
	var out Inspect
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Stats est un sous-ensemble de GET /containers/{id}/stats.
type Stats struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

// MemUsed applique la même règle que `docker stats` (cache inactif exclu).
func (s *Stats) MemUsed() uint64 {
	u := s.MemoryStats.Usage
	for _, k := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := s.MemoryStats.Stats[k]; ok && v < u {
			return u - v
		}
	}
	return u
}

func (c *Client) Stats(ctx context.Context, id string) (*Stats, error) {
	var out Stats
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/stats", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) post(ctx context.Context, path string, q url.Values) error {
	resp, err := c.req(ctx, c.http, http.MethodPost, path, q)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/start", nil)
}

func (c *Client) Stop(ctx context.Context, id string, timeout time.Duration) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/stop", url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}})
}

func (c *Client) Restart(ctx context.Context, id string, timeout time.Duration) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/restart", url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}})
}

type Version struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
	Os         string `json:"Os"`
	Arch       string `json:"Arch"`
}

func (c *Client) Version(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.getJSON(ctx, "/version", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// ImageInspect renvoie les digests de dépôt d'une image locale (lecture seule).
func (c *Client) ImageInspect(ctx context.Context, id string) ([]string, error) {
	var out struct {
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := c.getJSON(ctx, "/images/"+url.PathEscape(id)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return out.RepoDigests, nil
}
