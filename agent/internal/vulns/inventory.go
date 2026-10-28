package vulns

// Inventaire des paquets d'une image, lu dans un conteneur qui l'utilise (API « archive » de Docker,
// valable aussi pour un conteneur arrêté) : système (os-release), paquets Debian/Ubuntu (dpkg,
// y compris status.d des images distroless), Alpine (apk) et dépendances npm (package-lock).
// Lecture en flux avec des tailles maximales : la mémoire reste bornée quelle que soit l'image.

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	maxOSRelease   = 64 << 10
	maxDpkgStatus  = 32 << 20
	maxApkDB       = 32 << 20
	maxLockfile    = 64 << 20
	maxDirTotal    = 16 << 20 // status.d : total des fichiers lus
	maxDirEntries  = 5000
	maxPackages    = 20000
	maxSymlinkHops = 4
	archiveTimeout = time.Minute
)

// OSInfo est le contenu utile d'os-release.
type OSInfo struct {
	ID         string
	VersionID  string
	Version    string
	PrettyName string
}

// Package est un paquet tel qu'interrogé dans OSV.
type Package struct {
	Ecosystem string
	Name      string
	Version   string
}

// parseOSRelease lit un fichier os-release (CLÉ=valeur, guillemets facultatifs).
func parseOSRelease(r io.Reader) OSInfo {
	var o OSInfo
	sc := bufio.NewScanner(io.LimitReader(r, maxOSRelease))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		v = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\$`, `$`, "\\`", "`").Replace(v)
		switch k {
		case "ID":
			o.ID = strings.ToLower(v)
		case "VERSION_ID":
			o.VersionID = v
		case "VERSION":
			o.Version = v
		case "PRETTY_NAME":
			o.PrettyName = v
		}
	}
	return o
}

// Name renvoie le nom lisible du système.
func (o OSInfo) Name() string {
	if o.PrettyName != "" {
		return o.PrettyName
	}
	return strings.TrimSpace(o.ID + " " + o.VersionID)
}

// osEcosystem renvoie l'écosystème OSV des paquets du système (« » s'il n'est pas pris en charge) :
// Debian:<version>, Ubuntu:<AA.MM>[:LTS], Alpine:v<majeure.mineure>.
func osEcosystem(o OSInfo) string {
	switch o.ID {
	case "debian":
		major, _, _ := strings.Cut(o.VersionID, ".")
		if major == "" || strings.Trim(major, "0123456789") != "" {
			return "" // testing / sid : pas de version numérotée dans OSV
		}
		return "Debian:" + major
	case "ubuntu":
		if o.VersionID == "" {
			return ""
		}
		eco := "Ubuntu:" + o.VersionID
		lts := strings.Contains(o.Version, "LTS")
		if o.Version == "" {
			// Sans VERSION : les versions AA.04 des années paires sont LTS.
			yy, mm, _ := strings.Cut(o.VersionID, ".")
			n := 0
			_, err := fmt.Sscanf(yy, "%d", &n)
			lts = err == nil && mm == "04" && n%2 == 0
		}
		if lts {
			eco += ":LTS"
		}
		return eco
	case "alpine":
		// « 3.20.3 » → v3.20 ; « 3.21.0_alpha20240807 » (edge) → v3.21.
		parts := strings.SplitN(o.VersionID, ".", 3)
		if len(parts) < 2 {
			return ""
		}
		major, minor := parts[0], strings.SplitN(parts[1], "_", 2)[0]
		if major == "" || minor == "" || strings.Trim(major+minor, "0123456789") != "" {
			return ""
		}
		return "Alpine:v" + major + "." + minor
	}
	return ""
}

// stanzas découpe un fichier « Clé: valeur » en paragraphes séparés par une ligne vide (dpkg).
func stanzas(r io.Reader, fn func(fields map[string]string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	fields := map[string]string{}
	flush := func() {
		if len(fields) > 0 {
			fn(fields)
			fields = map[string]string{}
		}
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			continue // suite d'un champ sur plusieurs lignes (description…)
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch k {
		case "Package", "Status", "Version", "Source", "Architecture":
			fields[k] = strings.TrimSpace(v)
		}
	}
	flush()
	return sc.Err()
}

// Binary est un paquet installé tel que le gestionnaire de paquets le connaît (nom binaire), avec
// le paquet source (Debian, Ubuntu) ou d'origine (Alpine) sous lequel OSV le référence.
type Binary struct {
	Name    string
	Source  string
	Version string
	Arch    string
}

// dpkgPackages lit un fichier status de dpkg (ou un fichier de status.d) : paquets installés,
// indexés par paquet source comme dans OSV. Le champ Source peut préciser la version source.
// bin (facultatif) reçoit chaque paquet binaire installé.
func dpkgPackages(r io.Reader, eco string, add func(Package), bin func(Binary)) (int, error) {
	n := 0
	err := stanzas(r, func(f map[string]string) {
		if f["Package"] == "" || f["Version"] == "" {
			return
		}
		if st := strings.Fields(f["Status"]); len(st) > 0 && st[len(st)-1] != "installed" {
			return // supprimé, fichiers de configuration seuls…
		}
		n++
		name, version := f["Package"], f["Version"]
		if src := f["Source"]; src != "" {
			s, v, hasVersion := strings.Cut(src, " ")
			name = s
			if hasVersion {
				if v = strings.Trim(strings.TrimSpace(v), "()"); v != "" {
					version = v
				}
			}
		}
		add(Package{Ecosystem: eco, Name: name, Version: version})
		if bin != nil {
			bin(Binary{Name: f["Package"], Source: name, Version: f["Version"], Arch: f["Architecture"]})
		}
	})
	return n, err
}

// apkPackages lit /lib/apk/db/installed : P (nom), V (version), o (paquet d'origine, indexé par OSV),
// A (architecture). bin (facultatif) reçoit chaque paquet installé.
func apkPackages(r io.Reader, eco string, add func(Package), bin func(Binary)) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	var name, version, origin, arch string
	flush := func() {
		if name != "" && version != "" {
			n++
			if origin == "" {
				origin = name
			}
			add(Package{Ecosystem: eco, Name: origin, Version: version})
			if bin != nil {
				bin(Binary{Name: name, Source: origin, Version: version, Arch: arch})
			}
		}
		name, version, origin, arch = "", "", "", ""
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if len(line) < 2 || line[1] != ':' {
			continue
		}
		switch line[0] {
		case 'P':
			name = line[2:]
		case 'V':
			version = line[2:]
		case 'o':
			origin = line[2:]
		case 'A':
			arch = line[2:]
		}
	}
	flush()
	return n, sc.Err()
}

// npmPackages lit un package-lock.json (versions 1 à 3) ou node_modules/.package-lock.json, en flux.
// installed : fichier caché de node_modules (paquets réellement présents) ; sinon les dépendances
// de développement sont ignorées (absentes d'une image de production).
func npmPackages(r io.Reader, installed bool, add func(Package)) (int, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return 0, err
	}
	var fromPackages, fromDeps []Package
	for dec.More() {
		key, err := stringToken(dec)
		if err != nil {
			return 0, err
		}
		switch key {
		case "packages":
			if fromPackages, err = npmPackagesMap(dec, installed); err != nil {
				return 0, err
			}
		case "dependencies":
			if err := npmDepsV1(dec, installed, 0, &fromDeps); err != nil {
				return 0, err
			}
		default:
			if err := skipValue(dec); err != nil {
				return 0, err
			}
		}
	}
	list := fromPackages
	if len(list) == 0 {
		list = fromDeps // lockfileVersion 1
	}
	for _, p := range list {
		add(p)
	}
	return len(list), nil
}

type npmEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dev     bool   `json:"dev"`
	Link    bool   `json:"link"`
}

// validNpmVersion écarte les dépendances git, fichiers ou URL.
func validNpmVersion(v string) bool { return v != "" && isDigit(v[0]) }

func npmPackagesMap(dec *json.Decoder, installed bool) ([]Package, error) {
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	var out []Package
	for dec.More() {
		key, err := stringToken(dec)
		if err != nil {
			return nil, err
		}
		var e npmEntry
		if err := dec.Decode(&e); err != nil {
			return nil, err
		}
		i := strings.LastIndex(key, "node_modules/")
		if i < 0 || e.Link || (e.Dev && !installed) || !validNpmVersion(e.Version) || len(out) >= maxPackages {
			continue // racine du projet, espaces de travail, liens…
		}
		name := key[i+len("node_modules/"):]
		if e.Name != "" {
			name = e.Name // alias « npm:vrai-nom@version »
		}
		out = append(out, Package{Ecosystem: "npm", Name: name, Version: e.Version})
	}
	_, err := dec.Token()
	return out, err
}

// npmDepsV1 parcourt l'arbre « dependencies » imbriqué de lockfileVersion 1.
func npmDepsV1(dec *json.Decoder, installed bool, depth int, out *[]Package) error {
	if depth > 64 {
		return errors.New("package-lock trop imbriqué")
	}
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		name, err := stringToken(dec)
		if err != nil {
			return err
		}
		if err := expectDelim(dec, '{'); err != nil {
			return err
		}
		var version string
		dev := false
		for dec.More() {
			field, err := stringToken(dec)
			if err != nil {
				return err
			}
			switch field {
			case "version":
				if version, err = stringToken(dec); err != nil {
					return err
				}
			case "dev":
				if err := dec.Decode(&dev); err != nil {
					return err
				}
			case "dependencies":
				if err := npmDepsV1(dec, installed, depth+1, out); err != nil {
					return err
				}
			default:
				if err := skipValue(dec); err != nil {
					return err
				}
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		if !(dev && !installed) && validNpmVersion(version) && len(*out) < maxPackages {
			*out = append(*out, Package{Ecosystem: "npm", Name: name, Version: version})
		}
	}
	_, err := dec.Token()
	return err
}

func expectDelim(dec *json.Decoder, d json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if t != d {
		return fmt.Errorf("JSON inattendu : %v au lieu de %v", t, d)
	}
	return nil
}

func stringToken(dec *json.Decoder) (string, error) {
	t, err := dec.Token()
	if err != nil {
		return "", err
	}
	s, ok := t.(string)
	if !ok {
		return "", fmt.Errorf("JSON inattendu : %v", t)
	}
	return s, nil
}

// skipValue saute une valeur JSON sans la charger en mémoire.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

// ---- Lecture dans le conteneur ----

// Archiver est la partie du client Docker utilisée pour lire les fichiers d'un conteneur.
type Archiver interface {
	Archive(ctx context.Context, id, path string) (io.ReadCloser, error)
}

var errNotExist = errors.New("fichier absent")

// fileReader lit des fichiers d'un conteneur via des archives tar, liens symboliques suivis.
type fileReader struct {
	dk Archiver
	id string
}

// open renvoie le contenu d'un fichier ordinaire (au plus max octets) ; fn le lit en flux.
func (fr fileReader) read(ctx context.Context, p string, max int64, fn func(io.Reader) error) error {
	for range maxSymlinkHops {
		link, err := fr.readOnce(ctx, p, max, fn)
		if err != nil || link == "" {
			return err
		}
		if !path.IsAbs(link) {
			link = path.Join(path.Dir(p), link)
		}
		p = path.Clean(link)
	}
	return fmt.Errorf("%s : trop de liens symboliques", p)
}

func (fr fileReader) readOnce(ctx context.Context, p string, max int64, fn func(io.Reader) error) (link string, err error) {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	body, err := fr.dk.Archive(ctx, fr.id, p)
	if err != nil {
		if isNotFound(err) {
			return "", errNotExist
		}
		return "", err
	}
	defer body.Close()
	tr := tar.NewReader(body)
	hdr, err := tr.Next()
	if err != nil {
		return "", fmt.Errorf("%s : archive illisible : %w", p, err)
	}
	switch hdr.Typeflag {
	case tar.TypeSymlink:
		return hdr.Linkname, nil
	case tar.TypeReg:
	default:
		return "", errNotExist // dossier ou fichier spécial
	}
	if hdr.Size > max {
		return "", fmt.Errorf("%s trop volumineux (%d Mo, %d au plus)", p, hdr.Size>>20, max>>20)
	}
	return "", fn(io.LimitReader(tr, max))
}

// exists indique si p est un fichier ordinaire du conteneur, liens symboliques suivis (seul l'en-tête
// de l'archive est lu).
func (fr fileReader) exists(ctx context.Context, p string) (bool, error) {
	for range maxSymlinkHops {
		link, regular, err := fr.stat(ctx, p)
		if errors.Is(err, errNotExist) {
			return false, nil
		}
		if err != nil || link == "" {
			return regular, err
		}
		if !path.IsAbs(link) {
			link = path.Join(path.Dir(p), link)
		}
		p = path.Clean(link)
	}
	return false, fmt.Errorf("%s : trop de liens symboliques", p)
}

func (fr fileReader) stat(ctx context.Context, p string) (link string, regular bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	body, err := fr.dk.Archive(ctx, fr.id, p)
	if err != nil {
		if isNotFound(err) {
			return "", false, errNotExist
		}
		return "", false, err
	}
	defer body.Close()
	hdr, err := tar.NewReader(body).Next()
	if err != nil {
		return "", false, fmt.Errorf("%s : archive illisible : %w", p, err)
	}
	switch hdr.Typeflag {
	case tar.TypeSymlink:
		return hdr.Linkname, false, nil
	case tar.TypeReg:
		return "", true, nil
	}
	return "", false, nil
}

// readDir lit les fichiers ordinaires d'un dossier (status.d), dans l'ordre de l'archive.
func (fr fileReader) readDir(ctx context.Context, p string, fn func(name string, r io.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	body, err := fr.dk.Archive(ctx, fr.id, p)
	if err != nil {
		if isNotFound(err) {
			return errNotExist
		}
		return err
	}
	defer body.Close()
	tr := tar.NewReader(body)
	var total int64
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s : archive illisible : %w", p, err)
		}
		if n >= maxDirEntries {
			return fmt.Errorf("%s : trop de fichiers", p)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if total += hdr.Size; total > maxDirTotal {
			return fmt.Errorf("%s trop volumineux", p)
		}
		if err := fn(hdr.Name, tr); err != nil {
			return err
		}
	}
}

// Inventory est l'inventaire d'une image, conservé en base par ID d'image.
type Inventory struct {
	V         int         `json:"v"`
	OS        string      `json:"os,omitempty"`  // nom lisible
	Eco       string      `json:"eco,omitempty"` // écosystème OSV des paquets du système
	Count     int         `json:"count"`         // paquets installés (binaires) et dépendances npm
	Packages  [][3]string `json:"pkgs"`          // écosystème, nom, version (sans doublon)
	Error     string      `json:"err,omitempty"`
	ReadAt    int64       `json:"at"`
	Truncated bool        `json:"trunc,omitempty"`
	// Paquets du gestionnaire de paquets (dpkg ou apk, pas status.d) : nom binaire, source ou origine,
	// version, architecture. Sert à la correction des failles (agent 0.7.0).
	Bins [][4]string `json:"bins,omitempty"`
	// Outils présents dans l'image : sh, apt-get, apk.
	Tools []string `json:"tools,omitempty"`
}

// inventoryVersion 2 : paquets binaires et outils (agent 0.7.0) ; les inventaires plus anciens sont relus.
const inventoryVersion = 2

// HasTool indique si l'outil (sh, apt-get, apk) est présent dans l'image.
func (inv Inventory) HasTool(name string) bool {
	for _, t := range inv.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// Binaries renvoie les paquets binaires installés.
func (inv Inventory) Binaries() []Binary {
	out := make([]Binary, 0, len(inv.Bins))
	for _, b := range inv.Bins {
		out = append(out, Binary{Name: b[0], Source: b[1], Version: b[2], Arch: b[3]})
	}
	return out
}

type inventoryBuilder struct {
	inv  *Inventory
	seen map[[3]string]bool
}

func (b *inventoryBuilder) bin(x Binary) {
	if x.Name == "" || len(b.inv.Bins) >= maxPackages {
		return
	}
	b.inv.Bins = append(b.inv.Bins, [4]string{x.Name, x.Source, x.Version, x.Arch})
}

func (b *inventoryBuilder) add(p Package) {
	k := [3]string{p.Ecosystem, p.Name, p.Version}
	if p.Name == "" || p.Version == "" || b.seen[k] {
		return
	}
	if len(b.inv.Packages) >= maxPackages {
		b.inv.Truncated = true
		return
	}
	b.seen[k] = true
	b.inv.Packages = append(b.inv.Packages, k)
}

// readInventory lit les paquets d'une image dans le conteneur id ; workDirs : dossiers où chercher un package-lock.
func readInventory(ctx context.Context, dk Archiver, id string, workDirs []string, now time.Time) Inventory {
	inv := Inventory{V: inventoryVersion, ReadAt: now.UnixMilli(), Packages: [][3]string{}}
	b := &inventoryBuilder{inv: &inv, seen: map[[3]string]bool{}}
	fr := fileReader{dk: dk, id: id}
	var errs []string
	note := func(err error) {
		if err != nil && !errors.Is(err, errNotExist) && len(errs) < 3 {
			errs = append(errs, err.Error())
		}
	}

	var osInfo OSInfo
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		err := fr.read(ctx, p, maxOSRelease, func(r io.Reader) error { osInfo = parseOSRelease(r); return nil })
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			inv.Error = "analyse interrompue"
			return inv
		}
		note(err)
	}
	inv.OS = osInfo.Name()
	inv.Eco = osEcosystem(osInfo)

	if eco := inv.Eco; eco != "" {
		// Outils nécessaires à la correction des paquets : un shell et le gestionnaire de paquets
		// (absents des images distroless).
		tool := func(name, p string) {
			ok, err := fr.exists(ctx, p)
			note(err)
			if ok {
				inv.Tools = append(inv.Tools, name)
			}
		}
		tool("sh", "/bin/sh")
		switch ecoBase(eco) {
		case "Debian", "Ubuntu":
			note(fr.read(ctx, "/var/lib/dpkg/status", maxDpkgStatus, func(r io.Reader) error {
				n, err := dpkgPackages(r, eco, b.add, b.bin)
				inv.Count += n
				return err
			}))
			note(fr.readDir(ctx, "/var/lib/dpkg/status.d", func(name string, r io.Reader) error {
				if strings.HasSuffix(name, ".md5sums") {
					return nil
				}
				n, err := dpkgPackages(r, eco, b.add, nil)
				inv.Count += n
				return err
			}))
			tool("apt-get", "/usr/bin/apt-get")
		case "Alpine":
			note(fr.read(ctx, "/lib/apk/db/installed", maxApkDB, func(r io.Reader) error {
				n, err := apkPackages(r, eco, b.add, b.bin)
				inv.Count += n
				return err
			}))
			tool("apk", "/sbin/apk")
		}
	}

	// Dépendances npm : fichier caché de node_modules (installé), sinon package-lock.json.
	seenDir := map[string]bool{}
	for _, d := range workDirs {
		if d == "" || !path.IsAbs(d) || seenDir[path.Clean(d)] {
			continue
		}
		d = path.Clean(d)
		seenDir[d] = true
		for _, f := range []struct {
			name      string
			installed bool
		}{{"node_modules/.package-lock.json", true}, {"package-lock.json", false}} {
			err := fr.read(ctx, path.Join(d, f.name), maxLockfile, func(r io.Reader) error {
				n, err := npmPackages(r, f.installed, b.add)
				inv.Count += n
				if err != nil {
					return fmt.Errorf("%s : %w", path.Join(d, f.name), err)
				}
				return nil
			})
			if err == nil {
				break
			}
			note(err)
		}
	}
	if ctx.Err() != nil {
		errs = append(errs, "analyse interrompue")
	}
	sort.Slice(inv.Packages, func(i, j int) bool {
		a, c := inv.Packages[i], inv.Packages[j]
		if a[0] != c[0] {
			return a[0] < c[0]
		}
		if a[1] != c[1] {
			return a[1] < c[1]
		}
		return a[2] < c[2]
	})
	sort.Slice(inv.Bins, func(i, j int) bool {
		return inv.Bins[i][0]+"\x00"+inv.Bins[i][3] < inv.Bins[j][0]+"\x00"+inv.Bins[j][3]
	})
	inv.Error = strings.Join(errs, " ; ")
	return inv
}

// ReadInventory lit l'inventaire de l'image d'un conteneur (aussi arrêté ou seulement créé).
func ReadInventory(ctx context.Context, dk Archiver, containerID string, workDirs []string, now time.Time) Inventory {
	return readInventory(ctx, dk, containerID, workDirs, now)
}
