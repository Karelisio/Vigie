package ports

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

var littleEndian = binary.NativeEndian.Uint16([]byte{1, 0}) == 1

// procHex écrit une adresse comme le noyau dans /proc/net/tcp : mots de 32 bits dans l'ordre de la machine.
func procHex(ip string, port int) string {
	a := netip.MustParseAddr(ip)
	var b []byte
	if a.Is4() {
		v := a.As4()
		b = v[:]
	} else {
		v := a.As16()
		b = v[:]
	}
	var sb strings.Builder
	for i := 0; i < len(b); i += 4 {
		fmt.Fprintf(&sb, "%08X", binary.NativeEndian.Uint32(b[i:]))
	}
	return fmt.Sprintf("%s:%04X", sb.String(), port)
}

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func row(i int, local, remote, state string) string {
	return fmt.Sprintf("%4d: %s %s %s 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n",
		i, local, remote, state, 10000+i)
}

func TestDecodeAddr(t *testing.T) {
	if littleEndian {
		// Valeurs réelles relevées sur une machine x86/ARM (petit-boutiste).
		for in, want := range map[string]string{
			"0100007F:0035":                         "127.0.0.1:53",
			"00000000:01BB":                         "0.0.0.0:443",
			"0500000A:0016":                         "10.0.0.5:22",
			"00000000000000000000000000000000:1F90": "[::]:8080",
			"00000000000000000000000001000000:0016": "[::1]:22",
			"0000000000000000FFFF00000100007F:1F90": "127.0.0.1:8080", // IPv4 dans IPv6
			"B80D0120000000000000000001000000:0050": "[2001:db8::1]:80",
		} {
			a, p, err := decodeAddr(in)
			if err != nil {
				t.Fatalf("%s : %v", in, err)
			}
			if got := netip.AddrPortFrom(a, uint16(p)).String(); got != want {
				t.Errorf("%s : %s, attendu %s", in, got, want)
			}
		}
	}
	for _, ip := range []string{"192.168.1.20", "::", "fe80::1", "2a01:4f8::abcd"} {
		a, p, err := decodeAddr(procHex(ip, 51820))
		if err != nil || a != netip.MustParseAddr(ip) || p != 51820 {
			t.Errorf("%s : %v %d %v", ip, a, p, err)
		}
	}
	for _, bad := range []string{"", "zz:0016", "0100007F", "0100007F:XYZ", "0100:0016"} {
		if _, _, err := decodeAddr(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}

func TestParseTable(t *testing.T) {
	zero4, zero6 := procHex("0.0.0.0", 0), procHex("::", 0)
	tcp := tcpHeader +
		row(0, procHex("0.0.0.0", 22), zero4, "0A") +
		row(1, procHex("127.0.0.53", 53), zero4, "0A") +
		row(2, procHex("10.0.0.5", 22), procHex("203.0.113.9", 51234), "01") + // connexion établie
		row(3, procHex("10.0.0.5", 41000), procHex("1.1.1.1", 443), "06") + // TIME_WAIT
		row(4, procHex("0.0.0.0", 22), zero4, "0A") // SO_REUSEPORT : doublon
	list, err := parseTable(strings.NewReader(tcp), "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Port != 22 || list[1].Addr.String() != "127.0.0.53" {
		t.Fatalf("tcp : %+v", list)
	}

	udp := tcpHeader +
		row(0, procHex("0.0.0.0", 51820), zero4, "07") + // non connectée : en écoute
		row(1, procHex("10.0.0.5", 40000), procHex("169.254.169.254", 53), "01") + // connectée
		row(2, procHex("10.0.0.5", 41000), procHex("8.8.8.8", 53), "07") // fermée mais avec un pair : client
	list, err = parseTable(strings.NewReader(udp), "udp")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Port != 51820 || list[0].Proto != "udp" {
		t.Fatalf("udp : %+v", list)
	}

	tcp6 := tcpHeader + row(0, procHex("::", 443), zero6, "0A") + row(1, procHex("::1", 631), zero6, "0A") + "garbage\n"
	list, err = parseTable(strings.NewReader(tcp6), "tcp")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Addr.IsUnspecified() || !list[1].Addr.IsLoopback() {
		t.Fatalf("tcp6 : %+v", list)
	}
}

// fakeProc prépare un faux /proc de l'hôte : sockets, routes, adresses.
type fakeProc struct {
	dir  string
	tcp  []string
	tcp6 []string
	udp  []string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "1", "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeProc{dir: dir}
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		routeLine("ens3", "0.0.0.0", "0.0.0.0") +
		routeLine("ens3", "10.0.0.0", "255.255.255.0") +
		routeLine("docker0", "172.17.0.0", "255.255.0.0") +
		routeLine("br-0123456789ab", "172.18.0.0", "255.255.0.0")
	fib := `Main:
  +-- 0.0.0.0/0 3 0 5
     |-- 0.0.0.0
        /0 universe UNICAST
     +-- 10.0.0.0/24 2 0 2
        |-- 10.0.0.0
           /24 link UNICAST
        |-- 10.0.0.5
           /32 host LOCAL
     |-- 172.17.0.1
        /32 host LOCAL
     |-- 172.18.0.1
        /32 host LOCAL
Local:
  +-- 127.0.0.0/8 2 0 2
     |-- 127.0.0.1
        /32 host LOCAL
`
	inet6 := "00000000000000000000000000000001 01 80 10 80       lo\n" +
		"fe800000000000000000000000000001 02 40 20 80     ens3\n"
	f.write(t, "route", route)
	f.write(t, "fib_trie", fib)
	f.write(t, "if_inet6", inet6)
	f.flush(t)
	return f
}

func routeLine(iface, dst, mask string) string {
	h := func(ip string) string { return strings.TrimSuffix(procHex(ip, 0), ":0000") }
	return fmt.Sprintf("%s\t%s\t00000000\t0001\t0\t0\t0\t%s\t0\t0\t0\n", iface, h(dst), h(mask))
}

func (f *fakeProc) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "1", "net", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeProc) listen(proto, ip string, port int) {
	switch {
	case proto == "udp":
		f.udp = append(f.udp, row(len(f.udp), procHex(ip, port), procHex("0.0.0.0", 0), "07"))
	case strings.Contains(ip, ":"):
		f.tcp6 = append(f.tcp6, row(len(f.tcp6), procHex(ip, port), procHex("::", 0), "0A"))
	default:
		f.tcp = append(f.tcp, row(len(f.tcp), procHex(ip, port), procHex("0.0.0.0", 0), "0A"))
	}
}

func (f *fakeProc) reset() { f.tcp, f.tcp6, f.udp = nil, nil, nil }

func (f *fakeProc) flush(t *testing.T) {
	f.write(t, "tcp", tcpHeader+strings.Join(f.tcp, ""))
	f.write(t, "tcp6", tcpHeader+strings.Join(f.tcp6, ""))
	f.write(t, "udp", tcpHeader+strings.Join(f.udp, ""))
	f.write(t, "udp6", tcpHeader)
}

func TestHostNetScope(t *testing.T) {
	f := newFakeProc(t)
	hn := readHostNet(filepath.Join(f.dir, "1", "net"), []docker.Network{
		{Name: "arr_backend", Driver: "bridge", Subnets: []string{"172.20.0.0/16"}},
	})
	if hn.primary.String() != "10.0.0.5" || hn.defaultIface != "ens3" {
		t.Fatalf("IP principale %v, interface %q", hn.primary, hn.defaultIface)
	}
	for ip, want := range map[string]string{
		"0.0.0.0":     "public ",
		"::":          "public ",
		"10.0.0.5":    "public ens3",
		"127.0.0.53":  "local lo",
		"::1":         "local lo",
		"fe80::1":     "local ens3",
		"172.17.0.1":  "local docker0",
		"172.18.0.1":  "local br-0123456789ab",
		"172.20.0.1":  "local arr_backend",
		"100.64.10.1": "public ", // interface inconnue (VPN…) : prudence, publique
	} {
		scope, where := hn.scope(netip.MustParseAddr(ip))
		if got := scope + " " + where; got != want {
			t.Errorf("%s : %q, attendu %q", ip, got, want)
		}
	}
}

func TestIsPangolin(t *testing.T) {
	for _, c := range []struct {
		name, image string
		want        bool
	}{
		{"gerbil", "fosrl/gerbil:1.0.0", true},
		{"pangolin", "fosrl/pangolin:1.5.1", true},
		{"traefik", "traefik:v3.4", true},
		{"newt", "fosrl/newt@sha256:abcd", true},
		{"pangolin-traefik-1", "docker.io/library/traefik:v3.4", true},
		{"proxy", "ghcr.io/fosrl/gerbil:latest", true},
		{"comet", "g0ldyy/comet:latest", false},
		{"newtonsoft", "example/newtonsoft", false},
	} {
		if got := isPangolin(c.name, c.image); got != c.want {
			t.Errorf("%s (%s) : %v", c.name, c.image, got)
		}
	}
}

// fakeDocker simule l'API Docker du scanner.
type fakeDocker struct {
	mu      sync.Mutex
	ctrs    []docker.PortContainer
	exposed map[string][]string
	err     error
}

func (d *fakeDocker) RunningPorts(context.Context) ([]docker.PortContainer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]docker.PortContainer{}, d.ctrs...), d.err
}

func (d *fakeDocker) ExposedPorts(_ context.Context, id string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exposed[id], nil
}

func (d *fakeDocker) Networks(context.Context) ([]docker.Network, error) {
	return []docker.Network{{Name: "bridge", Bridge: "docker0", Subnets: []string{"172.17.0.0/16"}}}, nil
}

func ctr(id, name, image, mode string, ports ...docker.PublishedPort) docker.PortContainer {
	c := docker.PortContainer{ID: id, Names: []string{"/" + name}, Image: image, Ports: ports}
	c.HostConfig.NetworkMode = mode
	return c
}

func pub(ip string, public, private int, proto string) docker.PublishedPort {
	return docker.PublishedPort{IP: ip, PublicPort: public, PrivatePort: private, Type: proto}
}

func TestBuildAttachesContainers(t *testing.T) {
	f := newFakeProc(t)
	f.listen("tcp", "0.0.0.0", 443)
	f.listen("tcp", "::", 443)
	f.listen("tcp", "0.0.0.0", 80)
	f.listen("udp", "0.0.0.0", 51820)
	f.listen("tcp", "0.0.0.0", 8080)
	f.listen("tcp", "0.0.0.0", 22)
	f.listen("tcp", "::", 22)
	f.listen("tcp", "127.0.0.53", 53)
	f.listen("udp", "127.0.0.53", 53)
	f.listen("tcp", "0.0.0.0", 9000)
	f.listen("tcp", "172.17.0.1", 9100)
	f.flush(t)
	ctrs := []ctrInfo{
		{ID: "g1", Name: "gerbil", Pangolin: true, Published: []docker.PublishedPort{
			pub("0.0.0.0", 443, 443, "tcp"), pub("::", 443, 443, "tcp"), pub("0.0.0.0", 80, 80, "tcp"), pub("0.0.0.0", 51820, 51820, "udp"),
		}},
		{ID: "c1", Name: "comet", Published: []docker.PublishedPort{pub("0.0.0.0", 8080, 8000, "tcp"), pub("", 0, 3000, "tcp")}},
		{ID: "p1", Name: "portainer", HostNetwork: true, Exposed: map[string]bool{"9000/tcp": true}},
		// Publié sur la boucle locale, sans socket visible (proxy userland désactivé).
		{ID: "r1", Name: "redis", Published: []docker.PublishedPort{pub("127.0.0.1", 6379, 6379, "tcp")}},
	}
	dir := filepath.Join(f.dir, "1", "net")
	socks, err := readSockets(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := build(socks, ctrs, readHostNet(dir, nil), map[string]bool{"8080/tcp": true})
	got := map[string]Port{}
	var order []string
	for _, p := range list {
		got[p.stateKey()] = p
		order = append(order, p.Key()+"/"+p.Scope)
	}
	want := "22/tcp/public 80/tcp/public 443/tcp/public 8080/tcp/public 9000/tcp/public 51820/udp/public 53/tcp/local 53/udp/local 6379/tcp/local 9100/tcp/local"
	if strings.Join(order, " ") != want {
		t.Fatalf("ordre : %s", strings.Join(order, " "))
	}
	https := got["tcp/443/public"]
	if https.Container != "gerbil" || !https.Pangolin || https.Bypass || len(https.Addresses) != 2 || https.Address != "0.0.0.0" {
		t.Errorf("443 : %+v", https)
	}
	if wg := got["udp/51820/public"]; wg.Container != "gerbil" || !wg.Pangolin {
		t.Errorf("51820/udp : %+v", wg)
	}
	comet := got["tcp/8080/public"]
	if comet.Container != "comet" || comet.TargetPort != 8000 || !comet.Bypass || !comet.Allowed || comet.Pangolin {
		t.Errorf("8080 : %+v", comet)
	}
	ssh := got["tcp/22/public"]
	if ssh.Container != "" || ssh.Process != "SSH" || ssh.Bypass || ssh.Owner() != "SSH" {
		t.Errorf("22 : %+v", ssh)
	}
	if p := got["tcp/9000/public"]; p.Container != "portainer" || !p.HostNetwork || !p.Bypass {
		t.Errorf("9000 : %+v", p)
	}
	if p := got["tcp/6379/local"]; p.Container != "redis" || p.Source != "docker" || p.Bypass {
		t.Errorf("6379 : %+v", p)
	}
	if p := got["tcp/9100/local"]; p.Interface != "docker0" || p.Process != "node_exporter" {
		t.Errorf("9100 : %+v", p)
	}
	if p := got["udp/53/local"]; p.Process != "DNS" || p.Interface != "lo" {
		t.Errorf("53/udp : %+v", p)
	}
}

type harness struct {
	t     *testing.T
	f     *fakeProc
	dk    *fakeDocker
	st    *store.Store
	set   settings.Settings
	now   time.Time
	sent  []string
	s     *Scanner
	setMu sync.Mutex
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{t: t, f: newFakeProc(t), dk: &fakeDocker{exposed: map[string][]string{}}, st: st,
		set: settings.Settings{PortAlert: true}, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	h.start()
	return h
}

// start crée un scanner neuf (comme après un redémarrage de l'agent).
func (h *harness) start() {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.s = New(h.f.dir, h.dk, h.st, func() settings.Settings {
		h.setMu.Lock()
		defer h.setMu.Unlock()
		return h.set
	}, func(c Change) {
		verb := "fermé"
		if c.Open {
			verb = "ouvert"
		}
		h.sent = append(h.sent, verb+" "+c.Port.Key()+" "+c.Port.Owner())
	}, log)
	h.s.now = func() time.Time { return h.now }
}

func (h *harness) scan(after time.Duration) Report {
	h.now = h.now.Add(after)
	h.f.flush(h.t)
	return h.s.Scan(context.Background())
}

func (h *harness) expect(want ...string) {
	h.t.Helper()
	if strings.Join(h.sent, " | ") != strings.Join(want, " | ") {
		h.t.Fatalf("alertes : %q\nattendu : %q", h.sent, want)
	}
	h.sent = nil
}

func TestAlertsBaselineNewAndClosed(t *testing.T) {
	h := newHarness(t)
	h.f.listen("tcp", "0.0.0.0", 22)
	h.f.listen("tcp", "0.0.0.0", 443)
	h.f.listen("tcp", "0.0.0.0", 8080)
	h.dk.ctrs = []docker.PortContainer{
		ctr("g1", "gerbil", "fosrl/gerbil", "bridge", pub("0.0.0.0", 443, 443, "tcp")),
		ctr("c1", "comet", "g0ldyy/comet", "bridge", pub("0.0.0.0", 8080, 8000, "tcp")),
	}
	// Premier passage : base, pas d'alerte en rafale, mais le contournement reste signalé.
	r := h.scan(0)
	h.expect()
	var bypass []string
	for _, p := range r.Ports {
		if p.Bypass {
			bypass = append(bypass, p.Key())
		}
	}
	if strings.Join(bypass, ",") != "8080/tcp" || r.HostIP != "10.0.0.5" || strings.Join(r.Pangolin, ",") != "gerbil" || !r.Docker {
		t.Fatalf("base : %+v", r)
	}

	// Nouveau conteneur publié : alerte immédiate, une seule fois.
	h.f.listen("tcp", "0.0.0.0", 3000)
	h.dk.ctrs = append(h.dk.ctrs, ctr("a1", "aiostreams", "viren070/aiostreams", "bridge", pub("0.0.0.0", 3000, 3000, "tcp")))
	r = h.scan(time.Minute)
	h.expect("ouvert 3000/tcp aiostreams")
	for _, p := range r.Ports {
		if p.Key() == "3000/tcp" && (!p.Alerted || p.Since != h.now.UnixMilli()) {
			t.Fatalf("3000 : %+v", p)
		}
	}
	h.scan(5 * time.Minute)
	h.expect()

	// Redémarrage de l'agent : rien n'est oublié, pas de nouvelle base.
	h.start()
	h.scan(5 * time.Minute)
	h.expect()

	// Processus de l'hôte : confirmé au passage suivant seulement (socket éphémère sinon).
	h.f.listen("udp", "0.0.0.0", 40000)
	h.scan(time.Minute)
	h.expect()
	h.scan(5 * time.Minute)
	h.expect("ouvert 40000/udp processus de l'hôte")

	// Fermeture brève (redémarrage du conteneur) : rien. Fermeture durable : alerte résolue.
	h.f.reset()
	h.f.listen("tcp", "0.0.0.0", 22)
	h.f.listen("tcp", "0.0.0.0", 443)
	h.f.listen("tcp", "0.0.0.0", 8080)
	h.f.listen("udp", "0.0.0.0", 40000)
	h.dk.ctrs = h.dk.ctrs[:2]
	h.scan(time.Minute)
	h.expect()
	h.scan(5 * time.Minute)
	h.expect()
	h.scan(6 * time.Minute)
	h.expect("fermé 3000/tcp aiostreams")

	// Réouverture plus tard : nouvelle alerte.
	h.f.listen("tcp", "0.0.0.0", 3000)
	h.dk.ctrs = append(h.dk.ctrs, ctr("a2", "aiostreams", "viren070/aiostreams", "bridge", pub("0.0.0.0", 3000, 3000, "tcp")))
	h.scan(time.Minute)
	h.expect("ouvert 3000/tcp aiostreams")
}

func TestAlertsAllowListPangolinAndLocal(t *testing.T) {
	h := newHarness(t)
	h.scan(0) // base vide
	h.set.PortAllow = []string{"8443/tcp"}
	h.f.listen("tcp", "0.0.0.0", 8443)    // autorisé
	h.f.listen("tcp", "0.0.0.0", 81)      // Pangolin
	h.f.listen("tcp", "127.0.0.1", 5432)  // local
	h.f.listen("tcp", "172.17.0.1", 9100) // bridge Docker
	h.dk.ctrs = []docker.PortContainer{
		ctr("x1", "app", "example/app", "bridge", pub("0.0.0.0", 8443, 443, "tcp")),
		ctr("n1", "pangolin", "fosrl/pangolin", "bridge", pub("0.0.0.0", 81, 80, "tcp")),
		ctr("d1", "postgres", "postgres:16", "bridge", pub("127.0.0.1", 5432, 5432, "tcp")),
	}
	r := h.scan(time.Minute)
	h.expect()
	for _, p := range r.Ports {
		switch p.Key() {
		case "8443/tcp":
			if !p.Allowed || !p.Bypass || p.Alerted {
				t.Errorf("8443 : %+v", p)
			}
		case "81/tcp":
			if !p.Pangolin || p.Bypass {
				t.Errorf("81 : %+v", p)
			}
		case "5432/tcp", "9100/tcp":
			if p.Scope != "local" {
				t.Errorf("%s : %+v", p.Key(), p)
			}
		}
	}

	// Alerte désactivée : un nouveau port est mémorisé sans alerte, ni plus tard une fois réactivée.
	h.set.PortAlert = false
	h.f.listen("tcp", "0.0.0.0", 9999)
	h.dk.ctrs = append(h.dk.ctrs, ctr("y1", "debug", "example/debug", "bridge", pub("0.0.0.0", 9999, 80, "tcp")))
	h.scan(time.Minute)
	h.set.PortAlert = true
	h.scan(time.Minute)
	h.expect()

	// Port signalé puis autorisé : l'alerte en cours est oubliée sans bruit, pas de « fermé » ensuite.
	h.f.listen("tcp", "0.0.0.0", 7000)
	h.dk.ctrs = append(h.dk.ctrs, ctr("z1", "grafana", "grafana/grafana", "bridge", pub("0.0.0.0", 7000, 3000, "tcp")))
	h.scan(time.Minute)
	h.expect("ouvert 7000/tcp grafana")
	h.set.PortAllow = []string{"7000/tcp", "8443/tcp"}
	r = h.scan(time.Minute)
	for _, p := range r.Ports {
		if p.Key() == "7000/tcp" && (p.Alerted || !p.Allowed) {
			t.Errorf("7000 : %+v", p)
		}
	}
	h.f.reset()
	h.dk.ctrs = nil
	h.scan(time.Minute)
	h.scan(11 * time.Minute)
	h.expect()
}

func TestServerRebootIsNotAClosure(t *testing.T) {
	h := newHarness(t)
	h.scan(0) // base vide
	h.f.listen("tcp", "0.0.0.0", 8080)
	h.dk.ctrs = []docker.PortContainer{ctr("c1", "comet", "g0ldyy/comet", "bridge", pub("0.0.0.0", 8080, 8000, "tcp"))}
	h.scan(time.Minute)
	h.expect("ouvert 8080/tcp comet")
	// Serveur arrêté 2 h : au redémarrage, comet n'est pas encore lancé, puis revient.
	h.f.reset()
	h.dk.ctrs = nil
	h.start()
	h.scan(2 * time.Hour)
	h.expect()
	h.f.listen("tcp", "0.0.0.0", 8080)
	h.dk.ctrs = []docker.PortContainer{ctr("c2", "comet", "g0ldyy/comet", "bridge", pub("0.0.0.0", 8080, 8000, "tcp"))}
	r := h.scan(2 * time.Minute)
	h.expect()
	for _, p := range r.Ports {
		if p.Key() == "8080/tcp" && (!p.Alerted || p.Since != h.now.Add(-2*time.Hour-2*time.Minute).UnixMilli()) {
			t.Fatalf("8080 : %+v", p)
		}
	}
}

func TestDockerUnavailableSkipsAlerts(t *testing.T) {
	h := newHarness(t)
	h.dk.err = fmt.Errorf("docker injoignable")
	h.f.listen("tcp", "0.0.0.0", 8080)
	r := h.scan(0)
	if r.Docker || r.Error == "" || len(r.Ports) != 1 {
		t.Fatalf("rapport : %+v", r)
	}
	// La base n'est pas fixée sans Docker : le premier passage complet la fixe, sans alerte.
	h.dk.err = nil
	h.scan(time.Minute)
	h.expect()
	h.f.listen("tcp", "0.0.0.0", 8081)
	h.scan(time.Minute)
	h.expect()
	h.scan(5 * time.Minute)
	h.expect("ouvert 8081/tcp processus de l'hôte")
}

func TestMissingProc(t *testing.T) {
	h := newHarness(t)
	if err := os.Remove(filepath.Join(h.f.dir, "1", "net", "tcp")); err != nil {
		t.Fatal(err)
	}
	r := h.s.Scan(context.Background())
	if r.Error == "" || !strings.Contains(r.Error, "/proc:/host/proc:ro") {
		t.Fatalf("erreur attendue : %+v", r)
	}
}

func TestRunRescansOnKick(t *testing.T) {
	defer func(d time.Duration) { debounce = d }(debounce)
	debounce = 10 * time.Millisecond
	h := newHarness(t)
	h.f.listen("tcp", "0.0.0.0", 22)
	h.f.flush(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.s.Run(ctx)
		close(done)
	}()
	h.s.OnEvent(docker.Event{Action: "start"})
	h.s.OnEvent(docker.Event{Action: "exec_start"}) // ignoré
	deadline := time.Now().Add(10 * time.Second)
	for h.s.Report().TS == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if h.s.Report().TS == 0 {
		t.Fatal("pas d'analyse après l'événement Docker")
	}
}
