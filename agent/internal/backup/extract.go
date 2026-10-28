package backup

// Côté conteneur auxiliaire de restauration (VIGIE_HELPER_RESTORE) : l'archive déposée par l'agent
// est d'abord vérifiée en entier (gzip, tar, noms des entrées) sans rien toucher, puis la cible est
// vidée et l'archive extraite. Les archives sont celles de GET /containers/{id}/archive : leur
// premier niveau est le nom du point de montage (« config/… »), retiré à l'extraction.
//
// Sûreté : chemins absolus et « .. » refusés, aucune entrée ne peut passer par un lien symbolique
// de l'archive (vérifié avant de vider la cible), et toutes les écritures passent par os.Root, qui
// refuse de sortir de la cible. Le vidage ne suit aucun lien.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"
)

const (
	// HelperEnv active le mode auxiliaire du binaire de l'agent ; sa valeur est le chemin de l'archive.
	HelperEnv = "VIGIE_HELPER_RESTORE"
	// HelperTargetEnv remplace la cible par défaut (essais hors Docker uniquement).
	HelperTargetEnv = "VIGIE_HELPER_TARGET"
	// HelperTarget est le point de montage de l'élément à restaurer dans l'auxiliaire.
	HelperTarget = "/target"
	// helperArchive est l'archive déposée dans l'auxiliaire par PUT /containers/{id}/archive.
	helperArchive = "/vigie-restore.tar.gz"
	// resultPrefix précède le bilan JSON écrit par l'auxiliaire (dernière ligne de sa sortie).
	resultPrefix = "vigie-restore "

	exitRefused = 2 // archive ou cible refusée : rien n'a été modifié
	exitFailed  = 3 // échec après avoir commencé à modifier la cible

	maxNameLen = 4096
	maxLinks   = 1 << 20 // liens symboliques suivis pendant la vérification
)

// helperResult est le bilan de l'auxiliaire.
type helperResult struct {
	OK      bool   `json:"ok"`
	Touched bool   `json:"touched"` // le contenu de la cible a été modifié
	Error   string `json:"error,omitempty"`
	Entries int    `json:"entries"` // entrées extraites (dossiers compris)
	Files   int    `json:"files"`   // fichiers et liens
	Bytes   int64  `json:"bytes"`   // contenu des fichiers écrits
	Skipped int    `json:"skipped"` // fichiers spéciaux ignorés (périphériques, tubes)
}

// RunRestoreHelperFromEnv est le point d'entrée du conteneur auxiliaire. Il renvoie le code de sortie.
func RunRestoreHelperFromEnv(w io.Writer) int {
	target := os.Getenv(HelperTargetEnv)
	if target == "" {
		target = HelperTarget
	}
	return RunRestoreHelper(w, os.Getenv(HelperEnv), target)
}

// RunRestoreHelper remplace le contenu de target par celui de l'archive et écrit son bilan sur w.
func RunRestoreHelper(w io.Writer, archive, target string) int {
	res := restoreInto(archive, target)
	b, _ := json.Marshal(res)
	fmt.Fprintf(w, "%s%s\n", resultPrefix, b)
	switch {
	case res.OK:
		return 0
	case res.Touched:
		return exitFailed
	default:
		return exitRefused
	}
}

func restoreInto(archive, target string) (res helperResult) {
	fail := func(err error) helperResult {
		res.Error = err.Error()
		return res
	}
	fi, err := os.Lstat(target)
	if err != nil {
		return fail(fmt.Errorf("cible : %w", explain(err)))
	}
	single, err := checkArchive(archive)
	if err != nil {
		return fail(fmt.Errorf("archive refusée : %w", err))
	}
	switch {
	case fi.IsDir() && single:
		return fail(errors.New("l'archive contient un fichier seul mais la cible est un dossier"))
	case fi.Mode().IsRegular() && !single:
		return fail(errors.New("l'archive contient un dossier mais la cible est un fichier"))
	case !fi.IsDir() && !fi.Mode().IsRegular():
		return fail(errors.New("type de cible non géré"))
	}
	res.Touched = true
	if single {
		err = restoreFile(archive, target, &res)
	} else {
		err = restoreDir(archive, target, &res)
	}
	if err != nil {
		return fail(err)
	}
	res.OK = true
	return res
}

// splitEntry valide le nom d'une entrée et renvoie son premier composant et le reste (« . » pour
// le dossier de l'élément lui-même). Refuse les chemins absolus, vides, « . » et « .. ».
func splitEntry(name string) (top, rel string, err error) {
	if len(name) > maxNameLen || strings.ContainsRune(name, 0) {
		return "", "", fmt.Errorf("nom d'entrée invalide (%d octets)", len(name))
	}
	if strings.HasPrefix(name, "/") {
		return "", "", fmt.Errorf("chemin absolu refusé : %q", name)
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	for _, p := range parts {
		switch p {
		case "..":
			return "", "", fmt.Errorf("chemin hors du dossier refusé : %q", name)
		case "", ".":
			return "", "", fmt.Errorf("nom d'entrée invalide : %q", name)
		}
	}
	if len(parts) == 1 {
		return parts[0], ".", nil
	}
	return parts[0], strings.Join(parts[1:], "/"), nil
}

// walkArchive parcourt l'archive tar.gz et appelle fn pour chaque entrée (rel : chemin sous le
// dossier de l'élément), puis lit le flux gzip jusqu'au bout pour en vérifier la somme de contrôle.
func walkArchive(archive string, fn func(h *tar.Header, rel string, r io.Reader) error) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("lecture de la copie : %w", explain(err))
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip : %w", explain(err))
	}
	tr := tar.NewReader(zr)
	top := ""
	for first := true; ; first = false {
		h, err := tr.Next()
		if err == io.EOF {
			if first {
				return errors.New("archive vide")
			}
			break
		}
		if err != nil {
			return fmt.Errorf("tar : %w", explain(err))
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		t, rel, err := splitEntry(h.Name)
		if err != nil {
			return err
		}
		if top == "" {
			top = t
		} else if t != top {
			return fmt.Errorf("entrées hors du dossier %q : %q", top, h.Name)
		}
		if err := fn(h, rel, tr); err != nil {
			return fmt.Errorf("%s : %w", h.Name, err)
		}
	}
	// Somme de contrôle et taille vérifiées par le lecteur gzip à la fin du flux.
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return fmt.Errorf("gzip : %w", explain(err))
	}
	return nil
}

// errnoFR traduit les erreurs système les plus courantes de l'extraction.
var errnoFR = map[syscall.Errno]string{
	syscall.ENOENT: "introuvable", syscall.EEXIST: "existe déjà", syscall.EACCES: "accès refusé",
	syscall.EPERM: "opération non permise", syscall.ENOSPC: "plus de place sur le disque",
	syscall.EDQUOT: "quota disque dépassé", syscall.EROFS: "système de fichiers en lecture seule",
	syscall.ELOOP: "lien symbolique refusé", syscall.ENOTDIR: "un élément du chemin n'est pas un dossier",
	syscall.EISDIR: "c'est un dossier", syscall.EIO: "erreur d'entrée-sortie", syscall.ENAMETOOLONG: "nom trop long",
	syscall.EMLINK: "trop de liens physiques", syscall.ENOTEMPTY: "dossier non vide",
}

// explain remplace une erreur système ou de lecture d'archive par un message en français (sans
// le chemin, déjà donné par l'appelant).
func explain(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gzip.ErrChecksum):
		return errors.New("somme de contrôle incorrecte : copie corrompue")
	case errors.Is(err, gzip.ErrHeader):
		return errors.New("ce n'est pas une archive gzip")
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return errors.New("archive tronquée")
	case errors.Is(err, tar.ErrHeader):
		return errors.New("en-tête tar invalide")
	case strings.Contains(err.Error(), "path escapes from parent"):
		return errors.New("sortirait du dossier de l'élément")
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if msg, ok := errnoFR[errno]; ok {
			return errors.New(msg)
		}
	}
	return err
}

// linkRel valide la cible d'un lien physique (même dossier d'élément) et renvoie son chemin relatif.
func linkRel(h *tar.Header, top string) (string, error) {
	t, rel, err := splitEntry(h.Linkname)
	if err != nil {
		return "", err
	}
	if t != top || rel == "." {
		return "", fmt.Errorf("lien physique hors du dossier refusé : %q", h.Linkname)
	}
	return rel, nil
}

// throughLink indique si rel passe par un lien symbolique déclaré plus tôt dans l'archive.
func throughLink(links map[string]bool, rel string) (string, bool) {
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && links[rel[:i]] {
			return rel[:i], true
		}
	}
	return "", false
}

// checkArchive vérifie toute l'archive sans rien écrire et indique si elle contient un fichier seul
// (élément monté comme fichier) plutôt qu'un dossier.
func checkArchive(archive string) (single bool, err error) {
	links := map[string]bool{}
	top, n := "", 0
	err = walkArchive(archive, func(h *tar.Header, rel string, r io.Reader) error {
		n++
		if n == 1 {
			top, _, _ = splitEntry(h.Name)
			single = rel == "." && (h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA)
		}
		switch {
		case single && n > 1:
			return errors.New("archive d'un fichier seul avec plusieurs entrées")
		case rel == "." && !single && h.Typeflag != tar.TypeDir:
			return errors.New("le dossier de l'élément n'est pas un dossier")
		case len(h.Linkname) > maxNameLen || strings.ContainsRune(h.Linkname, 0):
			return errors.New("cible de lien invalide")
		}
		if via, ok := throughLink(links, rel); ok {
			return fmt.Errorf("passe par le lien symbolique %q", via)
		}
		switch h.Typeflag {
		case tar.TypeDir, tar.TypeReg, tar.TypeRegA, tar.TypeChar, tar.TypeBlock, tar.TypeFifo, tar.TypeSymlink:
		case tar.TypeLink:
			old, err := linkRel(h, top)
			if err != nil {
				return err
			}
			if via, ok := throughLink(links, old); ok {
				return fmt.Errorf("lien physique passant par le lien symbolique %q", via)
			}
		default:
			return fmt.Errorf("type d'entrée non géré (%q)", h.Typeflag)
		}
		if h.Typeflag == tar.TypeSymlink {
			if len(links) >= maxLinks {
				return errors.New("trop de liens symboliques")
			}
			links[rel] = true
		} else {
			delete(links, rel)
		}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return explain(err)
		}
		return nil
	})
	return single, err
}

// attrs sont les propriétaires, droits et dates appliqués à la fin (dossiers) ou tout de suite.
type attrs struct {
	rel        string
	mode       fs.FileMode
	uid, gid   int
	atime, mod time.Time
}

func attrsOf(h *tar.Header, rel string) attrs {
	mode := fs.FileMode(h.Mode).Perm()
	if h.Mode&04000 != 0 {
		mode |= fs.ModeSetuid
	}
	if h.Mode&02000 != 0 {
		mode |= fs.ModeSetgid
	}
	if h.Mode&01000 != 0 {
		mode |= fs.ModeSticky
	}
	at := h.AccessTime
	if at.IsZero() {
		at = h.ModTime
	}
	return attrs{rel: rel, mode: mode, uid: h.Uid, gid: h.Gid, atime: at, mod: h.ModTime}
}

// sameOwner : propriétaires conservés seulement pour root (comme tar), l'auxiliaire l'étant.
var sameOwner = os.Geteuid() == 0

func restoreDir(archive, target string, res *helperResult) error {
	root, err := os.OpenRoot(target)
	if err != nil {
		return fmt.Errorf("cible : %w", explain(err))
	}
	defer root.Close()
	if err := emptyRoot(root); err != nil {
		return fmt.Errorf("vidage : %w", err)
	}
	var dirs []attrs
	var top string
	err = walkArchive(archive, func(h *tar.Header, rel string, r io.Reader) error {
		if top == "" {
			top, _, _ = splitEntry(h.Name)
		}
		if rel == "." {
			dirs = append(dirs, attrsOf(h, "."))
			return nil
		}
		if dir := path.Dir(rel); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("création du dossier parent : %w", explain(err))
			}
		}
		// Entrée déjà présente (archive qui répète un nom) : remplacée, sauf dossier sur dossier.
		if fi, err := root.Lstat(rel); err == nil && !(fi.IsDir() && h.Typeflag == tar.TypeDir) {
			if err := root.RemoveAll(rel); err != nil {
				return fmt.Errorf("remplacement : %w", explain(err))
			}
		}
		a := attrsOf(h, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.Mkdir(rel, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("création du dossier : %w", explain(err))
			}
			dirs = append(dirs, a)
		case tar.TypeReg, tar.TypeRegA:
			n, err := writeEntry(root, rel, r, a)
			res.Bytes += n
			if err != nil {
				return fmt.Errorf("écriture : %w", explain(err))
			}
		case tar.TypeSymlink:
			if err := root.Symlink(h.Linkname, rel); err != nil {
				return fmt.Errorf("création du lien : %w", explain(err))
			}
			if sameOwner {
				if err := root.Lchown(rel, a.uid, a.gid); err != nil {
					return fmt.Errorf("propriétaire : %w", explain(err))
				}
			}
		case tar.TypeLink:
			old, err := linkRel(h, top)
			if err != nil {
				return err
			}
			if err := root.Link(old, rel); err != nil {
				return fmt.Errorf("lien physique vers %s : %w", old, explain(err))
			}
		default: // périphériques et tubes : jamais recréés
			res.Skipped++
			return nil
		}
		res.Entries++
		if h.Typeflag != tar.TypeDir {
			res.Files++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("extraction : %w", err)
	}
	// Dossiers en dernier (les plus profonds d'abord) : leurs dates ne bougent plus et des droits
	// restrictifs n'empêchent pas d'y écrire pendant l'extraction.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := applyDir(root, dirs[i]); err != nil {
			return fmt.Errorf("%s : droits : %w", dirs[i].rel, explain(err))
		}
	}
	return nil
}

// writeEntry crée le fichier rel (jamais au travers d'un lien) et y copie r.
func writeEntry(root *os.Root, rel string, r io.Reader, a attrs) (int64, error) {
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if err == nil {
		err = applyFile(f, a)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chtimes(rel, a.atime, a.mod)
	}
	return n, err
}

// applyFile règle propriétaire puis droits (chown efface les bits setuid/setgid).
func applyFile(f *os.File, a attrs) error {
	if sameOwner {
		if err := f.Chown(a.uid, a.gid); err != nil {
			return err
		}
	}
	return f.Chmod(a.mode)
}

func applyDir(root *os.Root, a attrs) error {
	// Remplacé depuis par un fichier ou un lien (nom répété dans l'archive) : rien à régler.
	if fi, err := root.Lstat(a.rel); err != nil || !fi.IsDir() {
		return nil
	}
	f, err := root.Open(a.rel)
	if err != nil {
		return err
	}
	err = applyFile(f, a)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && !a.mod.IsZero() {
		err = root.Chtimes(a.rel, a.atime, a.mod)
	}
	return err
}

// emptyRoot supprime tout le contenu du dossier, sans suivre aucun lien.
func emptyRoot(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return explain(err)
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return explain(err)
	}
	for _, n := range names {
		if err := root.RemoveAll(n); err != nil {
			return fmt.Errorf("%s : %w", n, explain(err))
		}
	}
	return nil
}

// restoreFile remplace le contenu d'un élément monté comme fichier.
func restoreFile(archive, target string, res *helperResult) error {
	return walkArchive(archive, func(h *tar.Header, _ string, r io.Reader) error {
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		a := attrsOf(h, ".")
		n, err := io.Copy(f, r)
		res.Bytes += n
		if err == nil {
			err = applyFile(f, a)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chtimes(target, a.atime, a.mod)
		}
		if err == nil {
			res.Entries, res.Files = 1, 1
		}
		return explain(err)
	})
}

// parseHelperOutput retrouve le bilan de l'auxiliaire dans ses dernières lignes de sortie ; sans
// bilan lisible, il est déduit du code de sortie.
func parseHelperOutput(lines []string, code int) helperResult {
	for i := len(lines) - 1; i >= 0; i-- {
		if raw, ok := strings.CutPrefix(lines[i], resultPrefix); ok {
			var r helperResult
			if json.Unmarshal([]byte(raw), &r) == nil {
				r.OK = r.OK && code == 0
				if !r.OK && r.Error == "" {
					r.Error = fmt.Sprintf("arrêt inattendu (code %d)", code)
				}
				return r
			}
		}
	}
	switch code {
	case 0:
		return helperResult{OK: true}
	case exitRefused:
		return helperResult{Error: "archive refusée"}
	case 137:
		return helperResult{Touched: true, Error: "arrêté de force (code 137 : mémoire insuffisante ?)"}
	}
	return helperResult{Touched: true, Error: fmt.Sprintf("arrêt inattendu (code %d)", code)}
}
