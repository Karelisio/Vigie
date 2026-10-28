package docker

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

// PublishedPort est un port publié sur l'hôte (« ports: » du compose).
type PublishedPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

// PortContainer est un conteneur en marche vu sous l'angle du réseau (GET /containers/json).
type PortContainer struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	Labels     map[string]string `json:"Labels"`
	Ports      []PublishedPort   `json:"Ports"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"` // bridge, host, container:<id>, nom d'un réseau…
	} `json:"HostConfig"`
}

func (c PortContainer) Name() string {
	if len(c.Names) == 0 {
		return c.ID[:min(12, len(c.ID))]
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// RunningPorts liste les conteneurs en marche avec leurs ports publiés et leur mode réseau.
func (c *Client) RunningPorts(ctx context.Context) ([]PortContainer, error) {
	var out []PortContainer
	err := c.getJSON(ctx, "/containers/json", nil, &out)
	return out, err
}

// ExposedPorts renvoie les ports déclarés par un conteneur (EXPOSE de l'image, « expose: »), ex. « 8080/tcp ».
func (c *Client) ExposedPorts(ctx context.Context, id string) ([]string, error) {
	var in struct {
		Config struct {
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		} `json:"Config"`
	}
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &in); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(in.Config.ExposedPorts))
	for p := range in.Config.ExposedPorts {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Network est un réseau Docker et ses sous-réseaux.
type Network struct {
	Name    string
	Driver  string
	Bridge  string   // interface de l'hôte (docker0, br-…), si connue
	Subnets []string // CIDR
}

// Networks liste les réseaux Docker (GET /networks).
func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	var raw []struct {
		Name    string `json:"Name"`
		ID      string `json:"Id"`
		Driver  string `json:"Driver"`
		Options map[string]string
		IPAM    struct {
			Config []struct {
				Subnet string `json:"Subnet"`
			} `json:"Config"`
		} `json:"IPAM"`
	}
	if err := c.getJSON(ctx, "/networks", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(raw))
	for _, n := range raw {
		nw := Network{Name: n.Name, Driver: n.Driver, Bridge: n.Options["com.docker.network.bridge.name"]}
		if nw.Bridge == "" && n.Driver == "bridge" && len(n.ID) >= 12 {
			nw.Bridge = "br-" + n.ID[:12]
		}
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet != "" {
				nw.Subnets = append(nw.Subnets, cfg.Subnet)
			}
		}
		out = append(out, nw)
	}
	return out, nil
}
