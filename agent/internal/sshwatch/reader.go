package sshwatch

import (
	"bufio"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	maxPerPoll  = 8 << 20   // octets lus au plus par passage (le reste attend le suivant)
	maxBackfill = 64 << 20  // octets lus au plus d'un fichier à la première lecture (la fin du fichier)
	maxGzip     = 128 << 20 // octets décompressés au plus d'un fichier tourné compressé
	maxLine     = 64 << 10  // une ligne plus longue est ignorée
)

// cursor est la position de lecture d'un fichier, mémorisée en base avec les événements lus.
type cursor struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
	Head   string `json:"head,omitempty"` // début du fichier déjà lu (hexadécimal) : repère une troncature
}

const headLen = 64

// headOf renvoie, en hexadécimal, le début du fichier (64 octets au plus, sans dépasser off).
func headOf(path string, off int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, min(int64(headLen), off))
	n, _ := io.ReadFull(f, buf)
	return hex.EncodeToString(buf[:n])
}

// sameHead indique si le fichier commence toujours par head (sinon il a été tronqué puis réécrit).
func sameHead(path, head string) bool {
	if head == "" {
		return true
	}
	return headOf(path, int64(len(head)/2)) == head
}

func inodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// readLines lit path à partir de off (au plus limit octets) et appelle fn pour chaque ligne complète.
// Une ligne inachevée (en cours d'écriture) est laissée pour la lecture suivante. Avec skipFirst, la
// première ligne, entamée, est ignorée. Renvoie la position qui suit la dernière ligne lue.
func readLines(path string, off, limit int64, skipFirst bool, fn func(string)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return off, err
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off, err
	}
	n, err := scanLines(io.LimitReader(f, limit), skipFirst, fn)
	if n == 0 && err == nil {
		// Aucune ligne complète dans toute la fenêtre : ligne démesurée, on la saute.
		if fi, e := f.Stat(); e == nil && fi.Size()-off >= limit {
			return off + limit, nil
		}
	}
	return off + n, err
}

// scanLines appelle fn pour chaque ligne terminée par « \n » et renvoie le nombre d'octets consommés
// jusqu'à la dernière d'entre elles. Les lignes trop longues sont sautées.
func scanLines(r io.Reader, skipFirst bool, fn func(string)) (int64, error) {
	br := bufio.NewReaderSize(r, maxLine)
	var consumed, committed int64
	long, skip := false, skipFirst
	for {
		chunk, err := br.ReadSlice('\n')
		consumed += int64(len(chunk))
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			long = true
			continue
		case err == nil:
			if !long && !skip {
				fn(strings.TrimRight(string(chunk), "\r\n"))
			}
			long, skip = false, false
			committed = consumed
			continue
		case errors.Is(err, io.EOF):
			return committed, nil
		default:
			return committed, err
		}
	}
}

// readTail lit les limit derniers octets d'un fichier (en entier s'il est plus petit).
func readTail(path string, limit int64, fn func(string)) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	start := max(0, fi.Size()-limit)
	return readLines(path, start, fi.Size()-start, start > 0, fn)
}

// readGzip lit un fichier tourné compressé (taille décompressée bornée).
func readGzip(path string, fn func(string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	_, err = scanLines(io.LimitReader(zr, maxGzip), false, fn)
	return err
}

// hostLocation renvoie le fuseau de l'hôte (/etc/localtime), utilisé pour l'horodatage syslog classique
// qui n'en indique pas. À défaut, le fuseau de l'agent (TZ).
func hostLocation(root string) *time.Location {
	p := filepath.Join(root, "etc", "localtime")
	if target, err := os.Readlink(p); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join("/etc", target)
		}
		name := target
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			name = target[i+len("zoneinfo/"):]
		}
		if data, err := os.ReadFile(filepath.Join(root, target)); err == nil {
			if loc, err := time.LoadLocationFromTZData(name, data); err == nil {
				return loc
			}
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	if data, err := os.ReadFile(p); err == nil {
		if loc, err := time.LoadLocationFromTZData("hôte", data); err == nil {
			return loc
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc", "timezone")); err == nil {
		if loc, err := time.LoadLocation(strings.TrimSpace(string(b))); err == nil {
			return loc
		}
	}
	return time.Local
}
