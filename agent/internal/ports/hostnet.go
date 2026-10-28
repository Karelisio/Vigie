package ports

import (
	"bufio"
	"encoding/hex"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/karelisio/vigie/agent/internal/docker"
)

// hostNet décrit le réseau de l'hôte : routes, adresses locales et réseaux Docker.
type hostNet struct {
	routes       []route
	addrs        map[netip.Addr]string // adresse locale → interface
	defaultIface string
	primary      netip.Addr // première IPv4 de l'interface de la route par défaut (IP privée de la VNIC)
	docker       []dockerNet
}

type route struct {
	iface  string
	prefix netip.Prefix
}

type dockerNet struct {
	name   string
	bridge string
	prefix netip.Prefix
}

// readHostNet lit les routes et adresses de l'hôte (fichiers absents ignorés) et y ajoute les réseaux Docker.
func readHostNet(dir string, nets []docker.Network) hostNet {
	h := hostNet{addrs: map[netip.Addr]string{}}
	if f, err := os.Open(filepath.Join(dir, "route")); err == nil {
		h.routes, h.defaultIface = parseRoutes(io.LimitReader(f, maxProcFile))
		f.Close()
	}
	if f, err := os.Open(filepath.Join(dir, "ipv6_route")); err == nil {
		h.routes = append(h.routes, parseRoutes6(io.LimitReader(f, maxProcFile))...)
		f.Close()
	}
	if f, err := os.Open(filepath.Join(dir, "fib_trie")); err == nil {
		for _, a := range parseFibTrie(io.LimitReader(f, maxProcFile)) {
			h.addrs[a] = h.routeIface(a)
		}
		f.Close()
	}
	if f, err := os.Open(filepath.Join(dir, "if_inet6")); err == nil {
		for a, iface := range parseIfInet6(io.LimitReader(f, maxProcFile)) {
			h.addrs[a] = iface
		}
		f.Close()
	}
	for a, iface := range h.addrs {
		if iface == h.defaultIface && iface != "" && a.Is4() && !a.IsLoopback() {
			if !h.primary.IsValid() || a.Less(h.primary) {
				h.primary = a
			}
		}
	}
	for _, n := range nets {
		for _, s := range n.Subnets {
			if p, err := netip.ParsePrefix(s); err == nil {
				h.docker = append(h.docker, dockerNet{name: n.Name, bridge: n.Bridge, prefix: p.Masked()})
			}
		}
	}
	return h
}

// parseRoutes lit /proc/net/route : interface et réseau de chaque route, et interface de la route par défaut.
func parseRoutes(r io.Reader) ([]route, string) {
	var out []route
	def := ""
	sc := bufio.NewScanner(r)
	for n := 0; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if n == 0 || len(f) < 8 {
			continue
		}
		dst, _, err1 := decodeAddr(f[1] + ":0")
		mask, _, err2 := decodeAddr(f[7] + ":0")
		if err1 != nil || err2 != nil {
			continue
		}
		m := mask.As4()
		bits := 0
		for _, b := range m {
			for ; b&0x80 != 0; b <<= 1 {
				bits++
			}
		}
		if bits == 0 {
			if def == "" {
				def = f[0]
			}
			continue
		}
		if p, err := dst.Prefix(bits); err == nil {
			out = append(out, route{iface: f[0], prefix: p})
		}
	}
	return out, def
}

// parseRoutes6 lit /proc/net/ipv6_route (réseau, longueur, …, interface en dernier), routes par défaut exclues.
func parseRoutes6(r io.Reader) []route {
	var out []route
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		raw, err := hex.DecodeString(f[0])
		bits, err2 := strconv.ParseUint(f[1], 16, 8)
		if err != nil || err2 != nil || len(raw) != 16 || bits == 0 || bits > 128 {
			continue
		}
		if p, err := netip.AddrFrom16([16]byte(raw)).Prefix(int(bits)); err == nil {
			out = append(out, route{iface: f[len(f)-1], prefix: p})
		}
	}
	return out
}

// parseFibTrie extrait les adresses IPv4 locales de /proc/net/fib_trie (lignes « /32 host LOCAL »).
func parseFibTrie(r io.Reader) []netip.Addr {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	var last netip.Addr
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if ip, ok := strings.CutPrefix(line, "|-- "); ok {
			last, _ = netip.ParseAddr(ip)
			continue
		}
		if strings.HasPrefix(line, "/32 host LOCAL") && last.IsValid() && !seen[last] {
			seen[last] = true
			out = append(out, last)
		}
	}
	return out
}

// parseIfInet6 lit /proc/net/if_inet6 : adresse IPv6 (32 chiffres hexadécimaux) et interface.
func parseIfInet6(r io.Reader) map[netip.Addr]string {
	out := map[netip.Addr]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		raw, err := hex.DecodeString(f[0])
		if err != nil || len(raw) != 16 {
			continue
		}
		out[netip.AddrFrom16([16]byte(raw))] = f[5]
	}
	return out
}

// routeIface renvoie l'interface de la route la plus précise qui contient a.
func (h hostNet) routeIface(a netip.Addr) string {
	best, bits := "", -1
	for _, r := range h.routes {
		if r.prefix.Contains(a) && r.prefix.Bits() > bits {
			best, bits = r.iface, r.prefix.Bits()
		}
	}
	return best
}

func dockerIface(name string) bool {
	return name == "docker0" || name == "docker_gwbridge" || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth")
}

// scope classe une adresse d'écoute : « public » (toutes les interfaces, ou une adresse de l'hôte hors
// réseaux Docker) ou « local » (boucle locale, lien local, bridges Docker). Renvoie aussi l'interface
// (ou le réseau Docker) concernée, si connue.
func (h hostNet) scope(a netip.Addr) (scope, where string) {
	switch {
	case a.IsUnspecified():
		return "public", ""
	case a.IsLoopback():
		return "local", "lo"
	case a.IsLinkLocalUnicast():
		return "local", h.addrs[a]
	}
	for _, d := range h.docker {
		if d.prefix.Contains(a) {
			return "local", d.name
		}
	}
	iface, ok := h.addrs[a]
	if !ok {
		iface = h.routeIface(a)
	}
	if dockerIface(iface) {
		return "local", iface
	}
	return "public", iface
}
