package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ent est une entrée d'archive de test.
type ent struct {
	name, body, link string
	typ              byte
	mode             int64
	uid, gid         int
}

func dirEnt(name string, mode int64) ent {
	return ent{name: name, typ: tar.TypeDir, mode: mode}
}

func fileEnt(name, body string) ent {
	return ent{name: name, body: body, typ: tar.TypeReg, mode: 0o644}
}

func linkEnt(name, target string) ent {
	return ent{name: name, link: target, typ: tar.TypeSymlink, mode: 0o777}
}

func hardEnt(name, target string) ent {
	return ent{name: name, link: target, typ: tar.TypeLink, mode: 0o644}
}

func withOwner(e ent, uid, gid int, mode int64) ent {
	e.uid, e.gid, e.mode = uid, gid, mode
	return e
}

var stamp = time.Date(2026, 10, 28, 3, 30, 0, 0, time.UTC)

func tarGz(t *testing.T, entries ...ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Uid: e.uid, Gid: e.gid, Linkname: e.link, ModTime: stamp, Format: tar.FormatPAX}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "copie.tar.gz")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runHelper exécute l'auxiliaire et renvoie son code de sortie et son bilan.
func runHelperOn(t *testing.T, archive, target string) (int, helperResult) {
	t.Helper()
	var out bytes.Buffer
	code := RunRestoreHelper(&out, archive, target)
	return code, parseHelperOutput(strings.Split(strings.TrimSpace(out.String()), "\n"), code)
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// outsideDir est un dossier hors de la cible, qui ne doit jamais être touché.
func outsideDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "secret"), "intouchable")
	return dir
}

func checkOutside(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || readFile(t, filepath.Join(dir, "secret")) != "intouchable" {
		t.Fatalf("dossier extérieur modifié : %v", entries)
	}
}

func TestHelperReplacesContent(t *testing.T) {
	outside := outsideDir(t)
	target := t.TempDir()
	// Ancien contenu, avec des liens vers l'extérieur que le vidage ne doit pas suivre.
	mustWrite(t, filepath.Join(target, "ancien.txt"), "à supprimer")
	mustWrite(t, filepath.Join(target, "vieux", "profond", "x"), "à supprimer")
	if err := os.Symlink(outside, filepath.Join(target, "vers-dehors")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(target, "vieux", "fichier-dehors")); err != nil {
		t.Fatal(err)
	}
	// Dossier sans droit d'écriture : vidé grâce à DAC_OVERRIDE, comme l'auxiliaire (root).
	if os.Geteuid() == 0 {
		if err := os.Chmod(filepath.Join(target, "vieux", "profond"), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(target, "lecture-seule"), 0o755) })

	archive := writeArchive(t, tarGz(t,
		withOwner(dirEnt("config/", 0), 1000, 1000, 0o750),
		withOwner(fileEnt("config/config.xml", "<Config/>"), 1000, 1000, 0o640),
		dirEnt("config/logs/", 0o700),
		withOwner(fileEnt("config/logs/sonarr.txt", "journal"), 1001, 1002, 0o600),
		fileEnt("config/bin/outil", "#!/bin/sh"),
		linkEnt("config/actuel", "config.xml"),
		linkEnt("config/absolu", "/etc/passwd"), // simple donnée : jamais suivi
		hardEnt("config/copie.xml", "config/config.xml"),
		ent{name: "config/tube", typ: tar.TypeFifo, mode: 0o644},
		withOwner(fileEnt("config/setuid", "x"), 0, 0, 0o4755),
		dirEnt("config/lecture-seule/", 0o555),
		fileEnt("config/lecture-seule/f", "dedans"),
	))
	code, res := runHelperOn(t, archive, target)
	if code != 0 || !res.OK || !res.Touched || res.Entries != 10 || res.Files != 8 || res.Skipped != 1 || res.Bytes != int64(len("<Config/>journal#!/bin/shxdedans")) {
		t.Fatalf("code %d, bilan %+v", code, res)
	}
	checkOutside(t, outside)

	var names []string
	_ = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if p != target {
			rel, _ := filepath.Rel(target, p)
			names = append(names, rel)
		}
		return nil
	})
	want := "absolu,actuel,bin,bin/outil,config.xml,copie.xml,lecture-seule,lecture-seule/f,logs,logs/sonarr.txt,setuid"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("contenu : %s", got)
	}
	if readFile(t, filepath.Join(target, "actuel")) != "<Config/>" || readFile(t, filepath.Join(target, "logs", "sonarr.txt")) != "journal" {
		t.Error("contenu des fichiers")
	}
	if l, _ := os.Readlink(filepath.Join(target, "absolu")); l != "/etc/passwd" {
		t.Errorf("lien : %q", l)
	}
	a, _ := os.Stat(filepath.Join(target, "config.xml"))
	b, _ := os.Stat(filepath.Join(target, "copie.xml"))
	if !os.SameFile(a, b) {
		t.Error("lien physique non recréé")
	}
	modes := map[string]fs.FileMode{
		".": fs.ModeDir | 0o750, "config.xml": 0o640, "logs": fs.ModeDir | 0o700, "logs/sonarr.txt": 0o600,
		"setuid": fs.ModeSetuid | 0o755, "lecture-seule": fs.ModeDir | 0o555,
	}
	for rel, m := range modes {
		fi, err := os.Lstat(filepath.Join(target, rel))
		if err != nil || fi.Mode() != m {
			t.Errorf("%s : droits %v, attendu %v (%v)", rel, fi.Mode(), m, err)
		}
	}
	if fi, _ := os.Stat(filepath.Join(target, "logs", "sonarr.txt")); !fi.ModTime().Equal(stamp) {
		t.Errorf("date : %v", fi.ModTime())
	}
	if fi, _ := os.Stat(filepath.Join(target, "logs")); !fi.ModTime().Equal(stamp) {
		t.Errorf("date du dossier : %v", fi.ModTime())
	}
	if os.Geteuid() == 0 {
		owners := map[string][2]uint32{".": {1000, 1000}, "config.xml": {1000, 1000}, "logs/sonarr.txt": {1001, 1002}, "bin/outil": {0, 0}}
		for rel, o := range owners {
			fi, _ := os.Lstat(filepath.Join(target, rel))
			st := fi.Sys().(*syscall.Stat_t)
			if st.Uid != o[0] || st.Gid != o[1] {
				t.Errorf("%s : propriétaire %d:%d, attendu %v", rel, st.Uid, st.Gid, o)
			}
		}
	}
}

func TestHelperRefusesUnsafeArchives(t *testing.T) {
	good := tarGz(t, dirEnt("config/", 0o755), fileEnt("config/a", "a"))
	corrupt := append([]byte{}, good...)
	corrupt[len(corrupt)-6] ^= 0xff // somme de contrôle gzip
	cases := []struct {
		name string
		data func(outside string) []byte
		want string
	}{
		{"chemin absolu", func(string) []byte { return tarGz(t, fileEnt("/etc/cron.d/x", "x")) }, "chemin absolu"},
		{"remontée en tête", func(string) []byte { return tarGz(t, fileEnt("../x", "x")) }, "hors du dossier"},
		{"remontée au milieu", func(string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), fileEnt("config/../../x", "x"))
		}, "hors du dossier"},
		{"autre dossier", func(string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), fileEnt("autre/x", "x"))
		}, "entrées hors du dossier"},
		{"écriture via un lien absolu", func(out string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), linkEnt("config/l", out), fileEnt("config/l/secret", "écrasé"))
		}, "passe par le lien symbolique"},
		{"écriture via un lien relatif", func(string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), linkEnt("config/l", "../../.."), fileEnt("config/l/tmp/x", "x"))
		}, "passe par le lien symbolique"},
		{"dossier via un lien", func(out string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), linkEnt("config/l", out), dirEnt("config/l/d/", 0o755))
		}, "passe par le lien symbolique"},
		{"lien physique hors du dossier", func(string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), hardEnt("config/h", "etc/passwd"))
		}, "lien physique hors du dossier"},
		{"lien physique absolu", func(string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), hardEnt("config/h", "/etc/passwd"))
		}, "chemin absolu"},
		{"lien physique via un lien", func(out string) []byte {
			return tarGz(t, dirEnt("config/", 0o755), linkEnt("config/l", out), hardEnt("config/h", "config/l/secret"))
		}, "passant par le lien symbolique"},
		{"dossier de l'élément en lien", func(out string) []byte { return tarGz(t, linkEnt("config", out)) }, "pas un dossier"},
		{"gzip corrompu", func(string) []byte { return corrupt }, "somme de contrôle incorrecte : copie corrompue"},
		{"archive tronquée", func(string) []byte { return good[:len(good)/2] }, "archive tronquée"},
		{"pas une archive", func(string) []byte { return []byte("bonjour, ceci est un simple texte") }, "ce n'est pas une archive gzip"},
		{"archive vide", func(string) []byte { return tarGz(t) }, "archive vide"},
		{"fichier seul pour un dossier", func(string) []byte { return tarGz(t, fileEnt("config.yml", "a: 1")) }, "fichier seul"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outside := outsideDir(t)
			target := t.TempDir()
			mustWrite(t, filepath.Join(target, "existant"), "gardé")
			code, res := runHelperOn(t, writeArchive(t, c.data(outside)), target)
			if code != exitRefused || res.OK || res.Touched || !strings.Contains(res.Error, c.want) {
				t.Fatalf("code %d, bilan %+v", code, res)
			}
			checkOutside(t, outside)
			if readFile(t, filepath.Join(target, "existant")) != "gardé" {
				t.Fatal("cible modifiée alors que l'archive est refusée")
			}
		})
	}
}

// Seconde ligne de défense : même sans la vérification préalable, l'extraction ne sort jamais de
// la cible, y compris via un lien créé par une entrée précédente.
func TestExtractionStaysInTarget(t *testing.T) {
	for _, name := range []string{"lien absolu", "lien relatif"} {
		t.Run(name, func(t *testing.T) {
			outside := outsideDir(t)
			target := t.TempDir()
			link := outside
			if name == "lien relatif" {
				rel, err := filepath.Rel(target, outside) // « ../002 »
				if err != nil {
					t.Fatal(err)
				}
				link = rel
			}
			archive := writeArchive(t, tarGz(t, dirEnt("config/", 0o755), linkEnt("config/l", link),
				fileEnt("config/l/secret", "écrasé"), fileEnt("config/l/nouveau", "x")))
			var res helperResult
			err := restoreDir(archive, target, &res)
			if err == nil || !strings.Contains(err.Error(), "config/l/secret") {
				t.Fatalf("écriture au travers d'un lien : %v", err)
			}
			// Écriture directe sous le lien (dossier parent déjà présent) : refusée par os.Root.
			root, err := os.OpenRoot(target)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if _, err := writeEntry(root, "l/nouveau", strings.NewReader("x"), attrs{mode: 0o644}); err == nil {
				t.Fatal("écriture directe au travers d'un lien acceptée")
			}
			checkOutside(t, outside)
		})
	}
}

// Une erreur après le vidage est signalée comme une modification (la copie de sécurité est remise).
func TestHelperFailureAfterEmptying(t *testing.T) {
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "existant"), "x")
	archive := writeArchive(t, tarGz(t, dirEnt("config/", 0o755), hardEnt("config/h", "config/absent")))
	code, res := runHelperOn(t, archive, target)
	if code != exitFailed || res.OK || !res.Touched || res.Error != "extraction : config/h : lien physique vers absent : introuvable" {
		t.Fatalf("code %d, bilan %+v", code, res)
	}
}

func TestHelperSingleFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.yml")
	mustWrite(t, target, "ancien contenu bien plus long")
	code, res := runHelperOn(t, writeArchive(t, tarGz(t, withOwner(fileEnt("config.yml", "a: 1\n"), 0, 0, 0o600))), target)
	if code != 0 || !res.OK || res.Entries != 1 || res.Files != 1 {
		t.Fatalf("code %d, bilan %+v", code, res)
	}
	fi, _ := os.Stat(target)
	if readFile(t, target) != "a: 1\n" || fi.Mode() != 0o600 || !fi.ModTime().Equal(stamp) {
		t.Fatalf("fichier : %v %v", fi.Mode(), fi.ModTime())
	}
	// Un dossier ne remplace pas un fichier.
	code, res = runHelperOn(t, writeArchive(t, tarGz(t, dirEnt("config/", 0o755))), target)
	if code != exitRefused || res.Touched || readFile(t, target) != "a: 1\n" {
		t.Fatalf("dossier sur un fichier : code %d, bilan %+v", code, res)
	}
}

func TestParseHelperOutput(t *testing.T) {
	ok := parseHelperOutput([]string{"bruit", resultPrefix + `{"ok":true,"touched":true,"entries":3,"bytes":12}`}, 0)
	if !ok.OK || ok.Entries != 3 {
		t.Errorf("bilan : %+v", ok)
	}
	if r := parseHelperOutput(nil, exitRefused); r.OK || r.Touched {
		t.Errorf("refus sans bilan : %+v", r)
	}
	if r := parseHelperOutput([]string{"panique"}, 137); r.OK || !r.Touched || !strings.Contains(r.Error, "137") {
		t.Errorf("arrêt brutal : %+v", r)
	}
	// Bilan réussi mais code d'erreur : échec.
	if r := parseHelperOutput([]string{resultPrefix + `{"ok":true,"touched":true}`}, 1); r.OK || r.Error == "" {
		t.Errorf("code incohérent : %+v", r)
	}
}
