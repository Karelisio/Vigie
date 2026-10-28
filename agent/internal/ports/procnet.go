package ports

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Socket est une socket en écoute dans l'espace réseau de l'hôte.
type Socket struct {
	Proto string // tcp ou udp
	Addr  netip.Addr
	Port  int
}

const (
	maxProcFile  = 32 << 20 // un fichier /proc/net/* n'est jamais lu au-delà
	maxProcLines = 500_000
	tcpListen    = "0A"
	udpUnconn    = "07" // TCP_CLOSE : socket UDP liée, non connectée
)

// netDir renvoie le dossier net de l'espace réseau de l'hôte : <proc>/1/net (procfs de l'hôte monté
// dans le conteneur), sinon <proc>/net.
func netDir(proc string) string {
	if _, err := os.Stat(filepath.Join(proc, "1", "net", "tcp")); err == nil {
		return filepath.Join(proc, "1", "net")
	}
	return filepath.Join(proc, "net")
}

// readSockets lit les sockets TCP en écoute et UDP non connectées (IPv4 et IPv6), sans doublon.
func readSockets(dir string) ([]Socket, error) {
	var out []Socket
	seen := map[Socket]bool{}
	for _, f := range []struct {
		name, proto string
		required    bool
	}{{"tcp", "tcp", true}, {"tcp6", "tcp", false}, {"udp", "udp", true}, {"udp6", "udp", false}} {
		fh, err := os.Open(filepath.Join(dir, f.name))
		if err != nil {
			if !f.required && errors.Is(err, fs.ErrNotExist) {
				continue // IPv6 désactivé sur l'hôte
			}
			return nil, fmt.Errorf("lecture de %s : %w", filepath.Join(dir, f.name), err)
		}
		list, err := parseTable(io.LimitReader(fh, maxProcFile), f.proto)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s : %w", f.name, err)
		}
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// parseTable décode une table /proc/net/{tcp,tcp6,udp,udp6} et garde les sockets en écoute :
// état LISTEN pour TCP, sockets non connectées pour UDP.
func parseTable(r io.Reader, proto string) ([]Socket, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	var out []Socket
	for n := 0; sc.Scan(); n++ {
		if n == 0 {
			continue // en-tête
		}
		if n > maxProcLines {
			break
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		local, remote, state := f[1], f[2], strings.ToUpper(f[3])
		switch proto {
		case "tcp":
			if state != tcpListen {
				continue
			}
		case "udp":
			if state != udpUnconn {
				continue
			}
			if _, rport, err := decodeAddr(remote); err != nil || rport != 0 {
				continue
			}
		}
		addr, port, err := decodeAddr(local)
		if err != nil || port == 0 {
			continue
		}
		out = append(out, Socket{Proto: proto, Addr: addr, Port: port})
	}
	return out, sc.Err()
}

// decodeAddr décode « 0100007F:0035 » (IPv4) ou « 00000000000000000000000001000000:1F90 » (IPv6).
// Le noyau écrit l'adresse par mots de 32 bits dans l'ordre d'octets de la machine, le port en clair.
func decodeAddr(s string) (netip.Addr, int, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.Addr{}, 0, fmt.Errorf("adresse invalide %q", s)
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("port invalide %q", s)
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, 0, fmt.Errorf("adresse invalide %q", s)
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(b[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	var a netip.Addr
	if len(b) == 4 {
		a = netip.AddrFrom4([4]byte(b))
	} else {
		a = netip.AddrFrom16([16]byte(b)).Unmap()
	}
	return a, int(port), nil
}
