package backup

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// snapshotName est la copie temporaire de la base, écrite dans le volume de données (pas en
// mémoire : /tmp est un tmpfs compté dans les 192 Mio de l'agent).
const snapshotName = ".backup-vigie.db"

// skipped : la base est lue via sa copie cohérente ; le token d'accès à l'API reste sur l'agent.
var skipped = map[string]bool{
	"vigie.db": true, "vigie.db-wal": true, "vigie.db-shm": true, "vigie.db-journal": true,
	"token": true, snapshotName: true,
}

// writeVigie écrit l'archive tar des données de Vigie (dossier « data/ ») : copie cohérente de la
// base, sans les clés du stockage, et les autres fichiers du dossier de données.
func (s *Service) writeVigie(ctx context.Context, w io.Writer) error {
	dir := s.opt.DataDir
	snap := filepath.Join(dir, snapshotName)
	_ = os.Remove(snap) // reste d'une sauvegarde interrompue : VACUUM INTO refuse d'écraser
	if err := s.st.Snapshot(ctx, snap, secretKey); err != nil {
		return fmt.Errorf("copie de la base : %w", err)
	}
	defer os.Remove(snap)
	tw := tar.NewWriter(w)
	if err := addFile(tw, snap, "data/vigie.db"); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || skipped[e.Name()] {
			continue
		}
		if err := addFile(tw, filepath.Join(dir, e.Name()), "data/"+e.Name()); err != nil {
			return err
		}
	}
	return tw.Close()
}

func addFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}
	hdr.Name = name
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	// Taille figée à l'ouverture : un fichier qui grandit pendant la copie reste cohérent dans l'archive.
	_, err = io.CopyN(tw, f, hdr.Size)
	return err
}
