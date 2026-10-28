package ports

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/karelisio/vigie/agent/internal/docker"
)

// Port est un port en écoute sur l'hôte (GET /api/v1/ports).
type Port struct {
	Port        int      `json:"port"`
	Proto       string   `json:"proto"`               // tcp ou udp
	Address     string   `json:"address"`             // première adresse d'écoute (0.0.0.0, ::, 10.0.0.5…)
	Addresses   []string `json:"addresses"`           // toutes les adresses d'écoute de ce port et de cette portée
	Scope       string   `json:"scope"`               // public (joignable de l'extérieur) ou local
	Interface   string   `json:"interface,omitempty"` // interface ou réseau Docker de l'adresse, si connu
	Container   string   `json:"container,omitempty"` // conteneur qui publie le port
	ContainerID string   `json:"container_id,omitempty"`
	TargetPort  int      `json:"target_port,omitempty"`  // port dans le conteneur
	HostNetwork bool     `json:"host_network,omitempty"` // conteneur en network_mode: host (rattachement déduit des ports déclarés)
	Process     string   `json:"process,omitempty"`      // nom probable d'un processus de l'hôte (« SSH »…)
	Pangolin    bool     `json:"pangolin"`               // port de la pile Pangolin : attendu
	Allowed     bool     `json:"allowed"`                // dans la liste des ports autorisés
	Bypass      bool     `json:"bypasses_pangolin"`      // public, publié par un autre conteneur que Pangolin
	Alerted     bool     `json:"alerted"`                // alerte « nouveau port » en cours
	Source      string   `json:"source"`                 // socket (vue dans /proc) ou docker (port publié sans socket visible)
	Since       int64    `json:"since"`                  // première détection (ms)
}

// Key renvoie la clé « port/proto » utilisée par la liste des ports autorisés.
func (p Port) Key() string { return strconv.Itoa(p.Port) + "/" + p.Proto }

// Owner nomme ce qui écoute : conteneur, processus connu ou processus de l'hôte.
func (p Port) Owner() string {
	switch {
	case p.Container != "":
		return p.Container
	case p.Process != "":
		return p.Process
	}
	return "processus de l'hôte"
}

// stateKey identifie une ligne de la liste (port, protocole et portée).
func (p Port) stateKey() string { return p.Proto + "/" + strconv.Itoa(p.Port) + "/" + p.Scope }

// ctrInfo résume un conteneur en marche pour le rattachement des ports.
type ctrInfo struct {
	ID, Name    string
	Pangolin    bool
	HostNetwork bool
	Published   []docker.PublishedPort
	Exposed     map[string]bool // « 8080/tcp » (conteneurs en réseau hôte)
}

// pangolinParts : conteneurs de la pile Pangolin (reverse proxy et tunnel), repérés par nom ou image.
var pangolinParts = map[string]bool{"pangolin": true, "gerbil": true, "traefik": true, "newt": true}

// isPangolin indique si un conteneur fait partie de la pile Pangolin : image (fosrl/gerbil, traefik…)
// ou un mot du nom (pangolin, gerbil, pangolin-traefik-1…).
func isPangolin(name, image string) bool {
	img := strings.ToLower(image)
	if i := strings.IndexAny(img, "@"); i >= 0 {
		img = img[:i]
	}
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	if i := strings.Index(img, ":"); i >= 0 {
		img = img[:i]
	}
	if pangolinParts[img] {
		return true
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		if pangolinParts[w] {
			return true
		}
	}
	return false
}

// knownPorts donne un nom probable aux ports courants d'un processus de l'hôte.
var knownPorts = map[string]string{
	"22/tcp":    "SSH",
	"25/tcp":    "SMTP",
	"53/tcp":    "DNS",
	"53/udp":    "DNS",
	"67/udp":    "DHCP",
	"68/udp":    "DHCP",
	"80/tcp":    "HTTP",
	"111/tcp":   "rpcbind",
	"111/udp":   "rpcbind",
	"123/udp":   "NTP",
	"323/udp":   "chrony",
	"443/tcp":   "HTTPS",
	"546/udp":   "DHCPv6",
	"631/tcp":   "CUPS",
	"2375/tcp":  "API Docker (non chiffrée)",
	"2376/tcp":  "API Docker",
	"3306/tcp":  "MySQL",
	"5353/udp":  "mDNS",
	"5355/tcp":  "LLMNR",
	"5355/udp":  "LLMNR",
	"5432/tcp":  "PostgreSQL",
	"6379/tcp":  "Redis",
	"9090/tcp":  "Cockpit / Prometheus",
	"9100/tcp":  "node_exporter",
	"10050/tcp": "agent Zabbix",
	"27017/tcp": "MongoDB",
	"41641/udp": "Tailscale",
	"51820/udp": "WireGuard",
}

// candidate est une socket ou un port publié avant regroupement.
type candidate struct {
	proto       string
	addr        string
	port        int
	scope       string
	where       string
	ctr         *ctrInfo
	target      int
	hostNetwork bool
	source      string
}

// build rattache les sockets aux conteneurs et regroupe le tout par port, protocole et portée.
func build(socks []Socket, ctrs []ctrInfo, hn hostNet, allow map[string]bool) []Port {
	var cands []candidate
	used := map[string]bool{} // ports publiés retrouvés parmi les sockets (« ctrID|ip|port/proto »)
	for _, s := range socks {
		scope, where := hn.scope(s.Addr)
		c := candidate{proto: s.Proto, addr: s.Addr.String(), port: s.Port, scope: scope, where: where, source: "socket"}
		if ctr, pp, ok := matchPublished(s, ctrs); ok {
			c.ctr, c.target = ctr, pp.PrivatePort
			used[ctr.ID+"|"+pp.IP+"|"+strconv.Itoa(pp.PublicPort)+"/"+pp.Type] = true
		} else if ctr := matchHostNetwork(s, ctrs); ctr != nil {
			c.ctr, c.target, c.hostNetwork = ctr, s.Port, true
		}
		cands = append(cands, c)
	}
	// Ports publiés sans socket visible (proxy userland de Docker désactivé : iptables seulement).
	for i := range ctrs {
		ctr := &ctrs[i]
		for _, pp := range ctr.Published {
			if pp.PublicPort == 0 || used[ctr.ID+"|"+pp.IP+"|"+strconv.Itoa(pp.PublicPort)+"/"+pp.Type] {
				continue
			}
			ip := pp.IP
			if ip == "" {
				ip = "0.0.0.0"
			}
			a, err := netip.ParseAddr(ip)
			if err != nil {
				continue
			}
			scope, where := hn.scope(a)
			cands = append(cands, candidate{
				proto: protoOf(pp.Type), addr: a.String(), port: pp.PublicPort, scope: scope, where: where,
				ctr: ctr, target: pp.PrivatePort, source: "docker",
			})
		}
	}

	byKey := map[string]*Port{}
	var keys []string
	for _, c := range cands {
		p := Port{Port: c.port, Proto: c.proto, Scope: c.scope}
		k := p.stateKey()
		cur, ok := byKey[k]
		if !ok {
			cur = &Port{Port: c.port, Proto: c.proto, Scope: c.scope, Address: c.addr, Interface: c.where, Source: c.source}
			byKey[k] = cur
			keys = append(keys, k)
		}
		if !containsStr(cur.Addresses, c.addr) {
			cur.Addresses = append(cur.Addresses, c.addr)
		}
		if c.source == "socket" {
			cur.Source = "socket"
		}
		if cur.Container == "" && c.ctr != nil {
			cur.Container, cur.ContainerID, cur.TargetPort, cur.HostNetwork = c.ctr.Name, c.ctr.ID, c.target, c.hostNetwork
			cur.Pangolin = c.ctr.Pangolin
		}
	}
	out := make([]Port, 0, len(keys))
	for _, k := range keys {
		p := byKey[k]
		sort.Slice(p.Addresses, func(i, j int) bool { return addrLess(p.Addresses[i], p.Addresses[j]) })
		p.Address = p.Addresses[0]
		if p.Container == "" {
			p.Process = knownPorts[p.Key()]
		}
		p.Allowed = allow[p.Key()]
		p.Bypass = p.Scope == "public" && p.Container != "" && !p.Pangolin
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Scope != b.Scope {
			return a.Scope == "public"
		}
		if a.Port != b.Port {
			return a.Port < b.Port
		}
		return a.Proto < b.Proto
	})
	if len(out) > maxPorts {
		out = out[:maxPorts] // les ports publics d'abord
	}
	return out
}

// maxPorts borne la liste renvoyée (sockets UDP éphémères en nombre sur un hôte très actif).
const maxPorts = 1000

// matchPublished cherche le conteneur qui publie ce port : même adresse d'abord, puis toute adresse
// (« 0.0.0.0 » publié peut apparaître en IPv4 et en IPv6).
func matchPublished(s Socket, ctrs []ctrInfo) (*ctrInfo, docker.PublishedPort, bool) {
	var fallback *ctrInfo
	var fbPort docker.PublishedPort
	for i := range ctrs {
		for _, pp := range ctrs[i].Published {
			if pp.PublicPort != s.Port || protoOf(pp.Type) != s.Proto {
				continue
			}
			ip, err := netip.ParseAddr(pp.IP)
			if pp.IP == "" || (err == nil && (ip.Unmap() == s.Addr || (ip.IsUnspecified() && s.Addr.IsUnspecified()))) {
				return &ctrs[i], pp, true
			}
			if fallback == nil {
				fallback, fbPort = &ctrs[i], pp
			}
		}
	}
	return fallback, fbPort, fallback != nil
}

// matchHostNetwork rattache une socket à l'unique conteneur en réseau hôte qui déclare ce port.
func matchHostNetwork(s Socket, ctrs []ctrInfo) *ctrInfo {
	var found *ctrInfo
	key := fmt.Sprintf("%d/%s", s.Port, s.Proto)
	for i := range ctrs {
		if ctrs[i].HostNetwork && ctrs[i].Exposed[key] {
			if found != nil {
				return nil // ambigu
			}
			found = &ctrs[i]
		}
	}
	return found
}

func protoOf(t string) string {
	if strings.EqualFold(t, "udp") {
		return "udp"
	}
	return "tcp"
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// addrLess classe 0.0.0.0 et :: en tête, puis IPv4 avant IPv6.
func addrLess(a, b string) bool {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return a < b
	}
	if pa.IsUnspecified() != pb.IsUnspecified() {
		return pa.IsUnspecified()
	}
	if pa.Is4() != pb.Is4() {
		return pa.Is4()
	}
	return pa.Less(pb)
}
