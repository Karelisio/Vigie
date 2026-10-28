// Package system lit les métriques de l'hôte depuis /proc et /sys montés en lecture seule.
package system

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Disk struct {
	Mount string  `json:"mount"`
	Total uint64  `json:"total"`
	Used  uint64  `json:"used"`
	Pct   float64 `json:"pct"`
}

type Snapshot struct {
	TS         int64     `json:"ts"`
	CPU        float64   `json:"cpu"`
	CPUSteal   float64   `json:"cpu_steal"`
	IOWait     float64   `json:"iowait"`
	Cores      []float64 `json:"cores"`
	NumCPU     int       `json:"num_cpu"`
	MemTotal   uint64    `json:"mem_total"`
	MemUsed    uint64    `json:"mem_used"`
	MemPct     float64   `json:"mem"`
	SwapTotal  uint64    `json:"swap_total"`
	SwapUsed   uint64    `json:"swap_used"`
	DiskTotal  uint64    `json:"disk_total"`
	DiskUsed   uint64    `json:"disk_used"`
	DiskPct    float64   `json:"disk"`
	Disks      []Disk    `json:"disks"`
	NetRx      float64   `json:"net_rx"` // octets/s
	NetTx      float64   `json:"net_tx"`
	NetRxTotal uint64    `json:"net_rx_total"`
	NetTxTotal uint64    `json:"net_tx_total"`
	Load1      float64   `json:"load1"`
	Load5      float64   `json:"load5"`
	Load15     float64   `json:"load15"`
	Uptime     int64     `json:"uptime"` // secondes
	Temp       *float64  `json:"temp"`   // °C, nil si indisponible
	Procs      int       `json:"procs"`
}

type HostInfo struct {
	Hostname string `json:"hostname"`
	Kernel   string `json:"kernel"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	NumCPU   int    `json:"num_cpu"`
	CPUModel string `json:"cpu_model"`
}

type cpuTimes struct{ total, idle, iowait, steal uint64 }

type Collector struct {
	proc, sys, root string

	mu       sync.Mutex
	prevCPU  []cpuTimes
	prevNet  [2]uint64
	prevTime time.Time
}

func New(proc, sys, root string) *Collector {
	if _, err := os.Stat(filepath.Join(proc, "stat")); err != nil {
		proc = "/proc" // exécution hors conteneur
	}
	if _, err := os.Stat(filepath.Join(sys, "class")); err != nil {
		sys = "/sys"
	}
	if _, err := os.Stat(root); err != nil {
		root = "/"
	}
	return &Collector{proc: proc, sys: sys, root: root}
}

func (c *Collector) Collect() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	s := Snapshot{TS: now.UnixMilli()}

	if cur := c.readCPU(); len(cur) > 0 {
		s.NumCPU = len(cur) - 1
		if len(c.prevCPU) == len(cur) {
			s.CPU, s.IOWait, s.CPUSteal = usage(c.prevCPU[0], cur[0])
			s.Cores = make([]float64, 0, len(cur)-1)
			for i := 1; i < len(cur); i++ {
				u, _, _ := usage(c.prevCPU[i], cur[i])
				s.Cores = append(s.Cores, round1(u))
			}
		}
		c.prevCPU = cur
	}

	mem := c.readKV("meminfo")
	s.MemTotal = mem["MemTotal"] * 1024
	avail := mem["MemAvailable"] * 1024
	if avail == 0 {
		avail = (mem["MemFree"] + mem["Buffers"] + mem["Cached"]) * 1024
	}
	if s.MemTotal > avail {
		s.MemUsed = s.MemTotal - avail
	}
	s.MemPct = pct(s.MemUsed, s.MemTotal)
	s.SwapTotal = mem["SwapTotal"] * 1024
	if sf := mem["SwapFree"] * 1024; s.SwapTotal > sf {
		s.SwapUsed = s.SwapTotal - sf
	}

	s.Disks = c.readDisks()
	if len(s.Disks) > 0 {
		s.DiskTotal, s.DiskUsed, s.DiskPct = s.Disks[0].Total, s.Disks[0].Used, s.Disks[0].Pct
	}

	rx, tx := c.readNet()
	s.NetRxTotal, s.NetTxTotal = rx, tx
	if !c.prevTime.IsZero() {
		dt := now.Sub(c.prevTime).Seconds()
		if dt > 0 && rx >= c.prevNet[0] && tx >= c.prevNet[1] {
			s.NetRx = float64(rx-c.prevNet[0]) / dt
			s.NetTx = float64(tx-c.prevNet[1]) / dt
		}
	}
	c.prevNet = [2]uint64{rx, tx}
	c.prevTime = now

	if f := strings.Fields(readFile(filepath.Join(c.proc, "loadavg"))); len(f) >= 4 {
		s.Load1, _ = strconv.ParseFloat(f[0], 64)
		s.Load5, _ = strconv.ParseFloat(f[1], 64)
		s.Load15, _ = strconv.ParseFloat(f[2], 64)
		if p := strings.SplitN(f[3], "/", 2); len(p) == 2 {
			s.Procs, _ = strconv.Atoi(p[1])
		}
	}
	if f := strings.Fields(readFile(filepath.Join(c.proc, "uptime"))); len(f) > 0 {
		u, _ := strconv.ParseFloat(f[0], 64)
		s.Uptime = int64(u)
	}
	s.Temp = c.readTemp()
	s.CPU, s.IOWait, s.CPUSteal = round1(s.CPU), round1(s.IOWait), round1(s.CPUSteal)
	return s
}

func usage(a, b cpuTimes) (busy, iowait, steal float64) {
	dt := float64(b.total - a.total)
	if b.total <= a.total || dt == 0 {
		return 0, 0, 0
	}
	idle := float64(b.idle-a.idle) + float64(b.iowait-a.iowait)
	busy = clamp(100 * (1 - idle/dt))
	iowait = clamp(100 * float64(b.iowait-a.iowait) / dt)
	steal = clamp(100 * float64(b.steal-a.steal) / dt)
	return
}

func (c *Collector) readCPU() []cpuTimes {
	f, err := os.Open(filepath.Join(c.proc, "stat"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []cpuTimes
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			break
		}
		fs := strings.Fields(line)
		var t cpuTimes
		for i, v := range fs[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			if i >= 8 { // guest et guest_nice sont déjà inclus dans user/nice
				break
			}
			t.total += n
			switch i {
			case 3:
				t.idle = n
			case 4:
				t.iowait = n
			case 7:
				t.steal = n
			}
		}
		out = append(out, t)
	}
	return out
}

func (c *Collector) readKV(name string) map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open(filepath.Join(c.proc, name))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) > 0 {
			out[k], _ = strconv.ParseUint(fs[0], 10, 64)
		}
	}
	return out
}

var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true, "vfat": false}

func (c *Collector) readDisks() []Disk {
	var disks []Disk
	seen := map[string]bool{}
	add := func(mount, path string) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
			return
		}
		key := strconv.FormatUint(st.Blocks, 10) + "/" + strconv.FormatUint(uint64(st.Bsize), 10) + "/" + strconv.FormatInt(int64(st.Fsid.X__val[0]), 10)
		if seen[key] {
			return
		}
		seen[key] = true
		bs := uint64(st.Bsize)
		total := st.Blocks * bs
		free := st.Bfree * bs
		avail := st.Bavail * bs
		used := total - free
		d := Disk{Mount: mount, Total: used + avail, Used: used}
		d.Pct = pct(used, used+avail)
		disks = append(disks, d)
	}
	add("/", c.root)
	// Autres systèmes de fichiers réels de l'hôte (vus depuis le PID 1).
	mounts := readFile(filepath.Join(c.proc, "1", "mounts"))
	var extra []string
	for _, l := range strings.Split(mounts, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || !realFS[f[2]] || f[1] == "/" || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		if strings.HasPrefix(f[1], "/boot") || strings.HasPrefix(f[1], "/snap") || strings.HasPrefix(f[1], "/var/lib/docker") {
			continue
		}
		extra = append(extra, unescapeMount(f[1]))
	}
	sort.Strings(extra)
	for _, m := range extra {
		if c.root == "/" {
			add(m, m)
		} else {
			add(m, filepath.Join(c.root, m))
		}
	}
	return disks
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

var virtualIf = []string{"lo", "veth", "docker", "br-", "virbr", "vnet", "cali", "flannel", "cni", "tun", "tap", "wg", "tailscale", "zt", "kube", "dummy", "bond-slave"}

func (c *Collector) readNet() (rx, tx uint64) {
	data := readFile(filepath.Join(c.proc, "1", "net", "dev"))
	if data == "" {
		data = readFile(filepath.Join(c.proc, "net", "dev"))
	}
	for _, l := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !c.physical(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return
}

func (c *Collector) physical(name string) bool {
	if _, err := os.Stat(filepath.Join(c.sys, "class", "net", name, "device")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(c.sys, "class", "net", name)); err == nil {
		return false // interface virtuelle connue de sysfs
	}
	for _, p := range virtualIf {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

func (c *Collector) readTemp() *float64 {
	var best float64
	found := false
	check := func(path string) {
		v, err := strconv.ParseFloat(strings.TrimSpace(readFile(path)), 64)
		if err != nil || v <= 0 {
			return
		}
		v /= 1000
		if v > 0 && v < 150 && v > best {
			best, found = v, true
		}
	}
	zones, _ := filepath.Glob(filepath.Join(c.sys, "class", "thermal", "thermal_zone*", "temp"))
	for _, z := range zones {
		check(z)
	}
	if !found {
		inputs, _ := filepath.Glob(filepath.Join(c.sys, "class", "hwmon", "hwmon*", "temp*_input"))
		for _, in := range inputs {
			check(in)
		}
	}
	if !found {
		return nil
	}
	best = round1(best)
	return &best
}

func (c *Collector) HostInfo() HostInfo {
	// /etc/hostname de l'hôte d'abord : /proc/sys/kernel/hostname reflète l'espace UTS du conteneur.
	hostname := strings.TrimSpace(readFile(filepath.Join(c.root, "etc", "hostname")))
	if hostname == "" {
		hostname = strings.TrimSpace(readFile(filepath.Join(c.proc, "sys", "kernel", "hostname")))
	}
	h := HostInfo{
		Hostname: hostname,
		Kernel:   strings.TrimSpace(readFile(filepath.Join(c.proc, "sys", "kernel", "osrelease"))),
		Arch:     runtime.GOARCH,
	}
	for _, l := range strings.Split(readFile(filepath.Join(c.root, "etc", "os-release")), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			h.OS = strings.Trim(v, `"`)
		}
	}
	cpus := 0
	for _, l := range strings.Split(readFile(filepath.Join(c.proc, "cpuinfo")), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cpus++
		case "model name", "Model", "CPU part":
			if h.CPUModel == "" || k == "model name" {
				h.CPUModel = v
			}
		}
	}
	if h.CPUModel == "0xd0c" {
		h.CPUModel = "Ampere Altra (Neoverse N1)"
	}
	h.NumCPU = cpus
	return h
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func pct(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return round1(100 * float64(a) / float64(b))
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
