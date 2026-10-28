package vulns

// Comparaison de versions selon l'écosystème : dpkg (Debian, Ubuntu), apk (Alpine) et SemVer (npm).
// Sert à choisir, parmi les plages d'une faille, la version qui corrige celle installée.

import (
	"strconv"
	"strings"
)

// ecoBase renvoie l'écosystème sans sa version (« Debian:12 » → « Debian »).
func ecoBase(eco string) string {
	b, _, _ := strings.Cut(eco, ":")
	return b
}

// compareVersions compare a et b dans l'écosystème eco (-1, 0, 1).
func compareVersions(eco, a, b string) int {
	var c int
	switch ecoBase(eco) {
	case "Debian", "Ubuntu":
		c = compareDpkg(a, b)
	case "Alpine":
		c = compareApk(a, b)
	case "npm":
		c = compareSemver(a, b)
	default:
		c = verrevcmp(a, b)
	}
	switch {
	case c < 0:
		return -1
	case c > 0:
		return 1
	}
	return 0
}

// ---- dpkg ----

func splitDpkg(v string) (epoch int, upstream, revision string) {
	if e, rest, ok := strings.Cut(v, ":"); ok {
		if n, err := strconv.Atoi(e); err == nil {
			epoch, v = n, rest
		}
	}
	upstream = v
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		upstream, revision = v[:i], v[i+1:]
	}
	return
}

func compareDpkg(a, b string) int {
	ea, ua, ra := splitDpkg(a)
	eb, ub, rb := splitDpkg(b)
	if ea != eb {
		return ea - eb
	}
	if c := verrevcmp(ua, ub); c != 0 {
		return c
	}
	return verrevcmp(ra, rb)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func dpkgOrder(s string, i int) int {
	if i >= len(s) {
		return 0
	}
	c := s[i]
	switch {
	case isDigit(c):
		return 0
	case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		return int(c)
	case c == '~':
		return -1
	}
	return int(c) + 256
}

// verrevcmp est l'algorithme de dpkg : parties non numériques (« ~ » avant tout), puis numériques.
func verrevcmp(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		for i < len(a) && !isDigit(a[i]) || j < len(b) && !isDigit(b[j]) {
			ac, bc := dpkgOrder(a, i), dpkgOrder(b, j)
			if ac != bc {
				return ac - bc
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		first := 0
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if first == 0 {
				first = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if first != 0 {
			return first
		}
	}
	return 0
}

// ---- apk ----

type apkVersion struct {
	nums     []int
	letter   byte
	suffixes [][2]int // rang du suffixe, numéro
	rev      int
}

// Rang des suffixes apk : les préversions avant la version finale (rang 4), les correctifs après.
var apkSuffixRank = map[string]int{"alpha": 0, "beta": 1, "pre": 2, "rc": 3, "cvs": 5, "svn": 6, "git": 7, "hg": 8, "p": 9}

func parseApk(v string) (apkVersion, bool) {
	var out apkVersion
	if base, rev, ok := strings.Cut(v, "-r"); ok {
		n, err := strconv.Atoi(rev)
		if err != nil {
			return out, false
		}
		out.rev, v = n, base
	}
	main, suffixes, _ := strings.Cut(v, "_")
	if main == "" {
		return out, false
	}
	if c := main[len(main)-1]; c >= 'a' && c <= 'z' {
		out.letter, main = c, main[:len(main)-1]
	}
	for _, p := range strings.Split(main, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out.nums = append(out.nums, n)
	}
	if suffixes != "" {
		for _, s := range strings.Split(suffixes, "_") {
			name := strings.TrimRight(s, "0123456789")
			rank, ok := apkSuffixRank[name]
			if !ok {
				return out, false
			}
			n, _ := strconv.Atoi(s[len(name):])
			out.suffixes = append(out.suffixes, [2]int{rank, n})
		}
	}
	return out, true
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareApk(a, b string) int {
	va, okA := parseApk(a)
	vb, okB := parseApk(b)
	if !okA || !okB {
		return verrevcmp(a, b)
	}
	for i := 0; i < max(len(va.nums), len(vb.nums)); i++ {
		if i >= len(va.nums) {
			return -1
		}
		if i >= len(vb.nums) {
			return 1
		}
		if c := cmpInt(va.nums[i], vb.nums[i]); c != 0 {
			return c
		}
	}
	if c := cmpInt(int(va.letter), int(vb.letter)); c != 0 {
		return c
	}
	for i := 0; i < max(len(va.suffixes), len(vb.suffixes)); i++ {
		sa, sb := [2]int{4, 0}, [2]int{4, 0} // sans suffixe : version finale
		if i < len(va.suffixes) {
			sa = va.suffixes[i]
		}
		if i < len(vb.suffixes) {
			sb = vb.suffixes[i]
		}
		if c := cmpInt(sa[0], sb[0]); c != 0 {
			return c
		}
		if c := cmpInt(sa[1], sb[1]); c != 0 {
			return c
		}
	}
	return cmpInt(va.rev, vb.rev)
}

// ---- SemVer (npm) ----

func parseSemver(v string) (nums [3]int, pre []string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	core, p, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nums, nil, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nums, nil, false
		}
		nums[i] = n
	}
	if hasPre {
		pre = strings.Split(p, ".")
	}
	return nums, pre, true
}

func compareSemver(a, b string) int {
	na, pa, okA := parseSemver(a)
	nb, pb, okB := parseSemver(b)
	if !okA || !okB {
		return verrevcmp(a, b)
	}
	for i := range na {
		if c := cmpInt(na[i], nb[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(pa) == 0 && len(pb) == 0:
		return 0
	case len(pa) == 0:
		return 1 // version finale après ses préversions
	case len(pb) == 0:
		return -1
	}
	for i := 0; i < min(len(pa), len(pb)); i++ {
		xa, errA := strconv.Atoi(pa[i])
		xb, errB := strconv.Atoi(pb[i])
		switch {
		case errA == nil && errB == nil:
			if c := cmpInt(xa, xb); c != 0 {
				return c
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(pa[i], pb[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(pa), len(pb))
}
