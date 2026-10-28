package backup

// Restauration d'un volume ou d'un dossier monté depuis une copie du stockage, sous le verrou des
// opérations. L'agent (distroless, système de fichiers en lecture seule) passe par un conteneur
// auxiliaire créé à partir de sa propre image, sans réseau ni accès à Docker, qui monte l'élément
// sur /target :
//  1. la copie choisie est déposée dans l'auxiliaire (créé, pas démarré), conteneurs encore en marche ;
//  2. les conteneurs qui utilisent l'élément sont arrêtés ;
//  3. une copie de sécurité de l'état actuel est envoyée au stockage ;
//  4. l'auxiliaire vérifie l'archive, vide l'élément et l'extrait ; s'il échoue après avoir commencé,
//     la copie de sécurité est remise de la même façon ;
//  5. les conteneurs arrêtés sont relancés (toujours, même en cas d'erreur), puis vérifiés.

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/karelisio/vigie/agent/internal/cleanup"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/updater"
)

const (
	// safetyPrefix nomme la copie de sécurité faite avant une restauration (une seule par élément,
	// jamais touchée par la rétention).
	safetyPrefix = "avant-restauration_"
	// maxRestoreSize : taille maximale d'une copie (celle d'une archive envoyée par parties de 16 Mio).
	maxRestoreSize = int64(maxParts) * partSize
	helperMemory   = 256 << 20
)

var (
	safetyRe = regexp.MustCompile(`^avant-restauration_\d{4}-\d{2}-\d{2}_\d{4}(\d{2})?\.tar\.gz$`)

	// ErrVigieRestore : la base de l'agent ne se remplace pas pendant qu'il tourne.
	ErrVigieRestore = errors.New("les données de Vigie ne se restaurent pas depuis l'appli : l'agent les utilise en permanence. " +
		"Suis la marche à suivre du README (Sauvegardes › Restaurer à la main)")
	// ErrRestoreDisabled : VIGIE_ALLOW_UPDATES=false limite l'agent à la lecture et à start/stop/restart.
	ErrRestoreDisabled = fmt.Errorf("restauration impossible : %w", updater.ErrDisabled)
)

// Restore lance la restauration de la copie key (objet du stockage) sur l'élément item.
func (s *Service) Restore(item, key string) (updater.Job, error) {
	if item == vigieID {
		return updater.Job{}, ErrVigieRestore
	}
	folder, err := itemKey(item)
	if err != nil {
		return updater.Job{}, err
	}
	if !s.upd.Enabled() {
		return updater.Job{}, ErrRestoreDisabled
	}
	cfg, sec := s.load(context.Background())
	if err := cfg.storageReady(sec); err != nil {
		return updater.Job{}, err
	}
	dir := path.Join(cfg.Prefix, folder)
	name, ok := strings.CutPrefix(key, dir+"/")
	if !ok || !(stampRe.MatchString(name) || safetyRe.MatchString(name)) {
		return updater.Job{}, errors.New("copie invalide pour cet élément")
	}
	cl, err := newS3(cfg, sec)
	if err != nil {
		return updater.Job{}, err
	}
	return s.upd.Run("restore", "restore", displayName(item), func(ctx context.Context, progress func(float64, string)) (string, error) {
		return s.restore(ctx, cl, item, dir, key, progress)
	})
}

// copyLabel décrit une copie d'après son nom : « du 28/10/2026 à 03:30 ».
func copyLabel(name string) string {
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, safetyPrefix), ".tar.gz")
	for _, layout := range []string{stampLayout, stampLayout + "05"} {
		if t, err := time.Parse(layout, stamp); err == nil {
			return t.Format("du 02/01/2006 à 15:04")
		}
	}
	return name
}

func (s *Service) restore(ctx context.Context, cl *s3Client, id, dir, key string, progress func(float64, string)) (msg string, err error) {
	name, copyName := displayName(id), path.Base(key)
	undo := safetyRe.MatchString(copyName)
	step := func(p float64, m string) { progress(p, name+" : "+m) }

	step(0, "recherche de l'élément…")
	srcs, err := s.sources(ctx)
	if err != nil {
		return "", err
	}
	src := srcs[id]
	if src == nil {
		return "", errors.New(name + " introuvable : plus aucun conteneur ne l'utilise")
	}
	self, err := s.agentContainer(ctx, src.item)
	if err != nil {
		return "", err
	}
	unchanged := func(err error) error { return fmt.Errorf("%w — rien n'a été modifié", err) }

	// 1. Copie choisie déposée dans l'auxiliaire, pendant que les conteneurs tournent encore.
	h, err := s.prepareHelper(ctx, cl, self, src.item, key, func(done, total int64) {
		step(0.02+0.3*float64(done)/float64(max(total, 1)), fmt.Sprintf("téléchargement de la copie (%s sur %s)…", cleanup.HumanBytes(done), cleanup.HumanBytes(total)))
	})
	if err != nil {
		return "", unchanged(err)
	}
	defer s.removeHelper(h)

	// 2. Arrêt des conteneurs qui utilisent l'élément, toujours relancés ensuite.
	stopped, err := s.stopUsers(ctx, src.users, name, func(_ float64, m string) { progress(0.33, m) })
	restarted := false
	restart := func() error {
		if restarted {
			return nil
		}
		restarted = true
		return s.restart(stopped, name, func(_ float64, m string) { progress(0.92, m) })
	}
	defer func() {
		if rerr := restart(); rerr != nil {
			if err != nil {
				err = fmt.Errorf("%w ; %w", err, rerr)
			} else {
				err = rerr
			}
			msg = ""
		}
		s.timeline(src.users, copyName, err)
	}()
	if err != nil {
		return "", unchanged(err)
	}

	// 3. Copie de sécurité de l'état actuel (cohérente : conteneurs arrêtés).
	safety := safetyKey(dir, key, time.Now())
	if err := s.safetyCopy(ctx, cl, src, safety, func(p float64, m string) { progress(0.35+0.3*p, m) }); err != nil {
		return "", unchanged(fmt.Errorf("copie de sécurité impossible : %w", err))
	}

	// 4. Remplacement du contenu, ou retour à l'état d'avant si l'auxiliaire échoue en cours de route.
	step(0.7, "remplacement du contenu…")
	res, err := s.runHelper(ctx, h)
	if err != nil {
		if !res.Touched {
			return "", unchanged(err)
		}
		step(0.8, "échec, remise de l'état d'avant…")
		if rerr := s.rollback(cl, self, src.item, safety); rerr != nil {
			return "", fmt.Errorf("%w ; copie de sécurité non remise (%v) : %s est peut-être incomplet, elle reste dans le stockage (%s)",
				err, rerr, name, path.Base(safety))
		}
		return "", fmt.Errorf("%w — état d'avant remis depuis la copie de sécurité", err)
	}

	// 5. Redémarrage et vérification.
	if err := restart(); err != nil {
		return "", fmt.Errorf("contenu restauré, mais %w", err)
	}
	if err := s.verify(ctx, stopped, func(m string) { progress(0.96, m) }); err != nil {
		return "", err
	}
	// Une seule copie de sécurité par élément : la nouvelle remplace les précédentes.
	s.pruneSafety(ctx, cl, dir, safety)

	if undo {
		msg = fmt.Sprintf("Dernière restauration de %s annulée", name)
	} else {
		msg = fmt.Sprintf("%s restauré : copie %s", name, copyLabel(copyName))
	}
	msg += fmt.Sprintf(" (%s, %s)", plural(res.Files, "fichier", "fichiers"), cleanup.HumanBytes(res.Bytes))
	if len(stopped) > 0 {
		names := make([]string, len(stopped))
		for i, u := range stopped {
			names[i] = u.name
		}
		msg += " ; relancé : " + strings.Join(names, ", ")
	}
	return msg, nil
}

// agentContainer renvoie le conteneur de l'agent (son image sert à l'auxiliaire) et refuse un
// élément que l'agent modifie lui-même.
func (s *Service) agentContainer(ctx context.Context, it Item) (*docker.ContainerJSON, error) {
	if s.opt.SelfID == "" {
		return nil, errors.New("conteneur de l'agent introuvable : la restauration demande que l'agent tourne dans Docker")
	}
	self, err := s.dk.InspectFull(ctx, s.opt.SelfID)
	if err != nil {
		return nil, fmt.Errorf("conteneur de l'agent : %w", err)
	}
	for _, m := range self.Mounts {
		if !m.RW {
			continue
		}
		if (it.Kind == "volume" && m.Type == "volume" && m.Name == it.Name) ||
			(it.Kind == "bind" && m.Type == "bind" && (within(it.Name, m.Source) || within(m.Source, it.Name))) {
			return nil, errors.New("l'agent Vigie utilise cet élément : restauration impossible depuis l'appli")
		}
	}
	return self, nil
}

// within indique si p est dir ou se trouve dessous.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// safetyKey nomme la copie de sécurité, sans jamais reprendre le nom de la copie restaurée (annulation
// d'une restauration faite la même minute).
func safetyKey(dir, chosen string, now time.Time) string {
	for i := 0; ; i++ {
		layout := stampLayout
		if i > 0 {
			layout += "05"
		}
		k := path.Join(dir, safetyPrefix+now.Add(time.Duration(max(0, i-1))*time.Second).Format(layout)+".tar.gz")
		if k != chosen {
			return k
		}
	}
}

// safetyCopy envoie l'état actuel de l'élément sous key, lu comme pour une sauvegarde.
func (s *Service) safetyCopy(ctx context.Context, cl *s3Client, src *source, key string, step func(float64, string)) error {
	from := src.users[0]
	estimate := lastRaw(s.History(ctx), src.item.ID)
	write := func(w io.Writer) error {
		body, err := s.dk.Archive(ctx, from.id, from.dest)
		if err != nil {
			return fmt.Errorf("lecture depuis %s : %w", from.name, err)
		}
		defer body.Close()
		_, err = io.Copy(w, body)
		return err
	}
	name := displayName(src.item.ID)
	step(0, name+" : copie de sécurité…")
	_, _, err := s.send(ctx, cl, key, make([]byte, s.part), write, func(raw, sent int64) {
		p := 0.0
		if estimate > 0 {
			p = min(1, float64(raw)/float64(estimate))
		}
		step(p, fmt.Sprintf("%s : copie de sécurité (%s lus, %s envoyés)…", name, cleanup.HumanBytes(raw), cleanup.HumanBytes(sent)))
	})
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = errors.New("durée maximale de l'opération dépassée")
	}
	return err
}

// pruneSafety supprime les copies de sécurité de l'élément autres que keep.
func (s *Service) pruneSafety(ctx context.Context, cl *s3Client, dir, keep string) {
	objs, err := cl.list(ctx, dir+"/"+safetyPrefix, 1000)
	if err != nil {
		s.log.Warn("restauration : anciennes copies de sécurité non listées", "erreur", err)
		return
	}
	for _, o := range objs {
		if o.Key != keep && safetyRe.MatchString(path.Base(o.Key)) {
			if err := cl.delete(ctx, o.Key); err != nil {
				s.log.Warn("restauration : ancienne copie de sécurité non supprimée", "objet", o.Key, "erreur", err)
			}
		}
	}
}

// rollback remet la copie de sécurité avec un nouvel auxiliaire, dans un délai propre : celui de
// l'opération peut être dépassé.
func (s *Service) rollback(cl *s3Client, self *docker.ContainerJSON, it Item, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	h, err := s.prepareHelper(ctx, cl, self, it, key, func(int64, int64) {})
	if err != nil {
		return err
	}
	defer s.removeHelper(h)
	_, err = s.runHelper(ctx, h)
	return err
}

// verify attend un peu puis contrôle que les conteneurs relancés tournent toujours.
func (s *Service) verify(ctx context.Context, users []user, step func(string)) error {
	if len(users) == 0 {
		return nil
	}
	step("vérification des conteneurs…")
	select {
	case <-ctx.Done():
	case <-time.After(s.settle):
	}
	vctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var down []string
	for _, u := range users {
		in, err := s.dk.InspectFull(vctx, u.id)
		switch {
		case err != nil:
			down = append(down, fmt.Sprintf("%s (%v)", u.name, err))
		case in.State.Restarting:
			down = append(down, u.name+" (redémarre en boucle)")
		case !in.State.Running:
			down = append(down, fmt.Sprintf("%s (arrêté, code %d)", u.name, in.State.ExitCode))
		}
	}
	if len(down) > 0 {
		return fmt.Errorf("contenu restauré, mais ne tourne pas : %s. Regarde ses logs, ou annule la restauration", strings.Join(down, ", "))
	}
	return nil
}

// timeline consigne la restauration dans la chronologie de chaque conteneur qui utilise l'élément.
func (s *Service) timeline(users []user, copyName string, err error) {
	action, detail := "restore", "copie "+copyLabel(copyName)
	if safetyRe.MatchString(copyName) {
		detail = "retour à la copie de sécurité " + copyLabel(copyName)
	}
	if err != nil {
		action, detail = "restore_failed", err.Error()
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
	}
	now := time.Now().UnixMilli()
	for _, u := range users {
		s.st.AddEvent(context.Background(), u.name, action, detail, now)
	}
}

// ---- Conteneur auxiliaire ----

// helper est un conteneur auxiliaire de restauration (supprimé à la fin, même en cas d'erreur).
type helper struct {
	id, name string
	removed  bool
}

// helperSpec prépare l'auxiliaire : image de l'agent, en root mais sans réseau, sans accès à Docker
// ni secret, avec les seules capacités utiles pour conserver propriétaires et droits.
func helperSpec(self *docker.ContainerJSON, it Item) map[string]any {
	target := map[string]any{"Type": it.Kind, "Source": it.Name, "Target": HelperTarget}
	if it.Kind == "volume" {
		target["VolumeOptions"] = map[string]any{"NoCopy": true}
	}
	spec := map[string]any{
		"Image":       self.Image,
		"User":        "0:0",
		"Env":         []string{HelperEnv + "=" + helperArchive},
		"Labels":      map[string]string{"vigie.ignore": "true", "vigie.helper": "true"},
		"Healthcheck": map[string]any{"Test": []string{"NONE"}},
		"HostConfig": map[string]any{
			"Mounts": []any{target},
			// /data est un VOLUME de l'image : un tmpfs évite d'y créer un volume anonyme.
			"Tmpfs":         map[string]string{"/data": "rw,size=1m"},
			"NetworkMode":   "none",
			"CapDrop":       []string{"ALL"},
			"CapAdd":        []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID"},
			"SecurityOpt":   []string{"no-new-privileges:true"},
			"Memory":        helperMemory,
			"NanoCpus":      int64(1e9),
			"PidsLimit":     64,
			"RestartPolicy": map[string]any{"Name": "no"},
			// Journal toujours lisible : le bilan de l'auxiliaire y est écrit.
			"LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m"}},
		},
	}
	if ep, ok := self.Config["Entrypoint"].([]any); ok && len(ep) > 0 {
		spec["Entrypoint"], spec["Cmd"] = ep, []string{}
	}
	return spec
}

func (s *Service) transient(name string, on bool) {
	if s.opt.SetTransient != nil {
		s.opt.SetTransient(name, on)
	}
}

// prepareHelper crée l'auxiliaire et y dépose la copie key, lue en flux depuis le stockage.
func (s *Service) prepareHelper(ctx context.Context, cl *s3Client, self *docker.ContainerJSON, it Item, key string,
	progress func(done, total int64)) (*helper, error) {
	body, size, err := cl.open(ctx, key)
	if err != nil {
		var se *s3Error
		if errors.As(err, &se) && se.Status == 404 {
			return nil, errors.New("copie introuvable dans le stockage")
		}
		return nil, fmt.Errorf("lecture de la copie : %w", err)
	}
	defer body.Close()
	if size > maxRestoreSize {
		return nil, fmt.Errorf("copie trop volumineuse (%s)", cleanup.HumanBytes(size))
	}
	h := &helper{name: fmt.Sprintf("vigie-restauration-%d", time.Now().UnixNano())}
	s.transient(h.name, true)
	h.id, err = s.dk.Create(ctx, h.name, helperSpec(self, it))
	if err != nil {
		s.transient(h.name, false)
		return nil, fmt.Errorf("conteneur auxiliaire : %w", err)
	}
	if err := s.stage(ctx, h, body, size, progress); err != nil {
		s.removeHelper(h)
		return nil, fmt.Errorf("dépôt de la copie dans le conteneur auxiliaire : %w", err)
	}
	return h, nil
}

// progressReader compte les octets lus et signale l'avancement au plus deux fois par seconde.
type progressReader struct {
	r    io.Reader
	n    atomic.Int64
	last time.Time
	tick func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	done := p.n.Add(int64(n))
	if time.Since(p.last) >= 500*time.Millisecond {
		p.last = time.Now()
		p.tick(done)
	}
	return n, err
}

// stage envoie à Docker une archive tar contenant la seule copie (taille connue), en flux.
func (s *Service) stage(ctx context.Context, h *helper, body io.Reader, size int64, progress func(done, total int64)) error {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{
			Name: strings.TrimPrefix(helperArchive, "/"), Typeflag: tar.TypeReg, Mode: 0o600, Size: size, ModTime: time.Now(),
		})
		if err == nil {
			_, err = io.Copy(tw, &progressReader{r: body, tick: func(n int64) { progress(n, size) }})
		}
		if err == nil {
			err = tw.Close() // copie plus courte qu'annoncé : erreur
		}
		pw.CloseWithError(err)
	}()
	err := s.dk.PutArchive(ctx, h.id, "/", pr)
	pr.CloseWithError(err) // Docker a cessé de lire : l'écriture en attente échoue
	<-done
	if err == nil {
		progress(size, size)
	}
	return err
}

// runHelper démarre l'auxiliaire, attend sa fin et lit son bilan.
func (s *Service) runHelper(ctx context.Context, h *helper) (helperResult, error) {
	if err := s.dk.Start(ctx, h.id); err != nil {
		return helperResult{}, fmt.Errorf("démarrage du conteneur auxiliaire : %w", err)
	}
	code, err := s.waitExit(ctx, h.id)
	if err != nil {
		// Délai dépassé ou Docker injoignable : l'auxiliaire est arrêté avant toute autre étape ;
		// l'élément a pu être modifié en partie.
		s.removeHelper(h)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.New("durée maximale de l'opération dépassée")
		}
		return helperResult{Touched: true}, fmt.Errorf("restauration interrompue : %w", err)
	}
	lctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res := parseHelperOutput(s.dk.LogTail(lctx, h.id, 20), code)
	if !res.OK {
		return res, errors.New(res.Error)
	}
	return res, nil
}

// waitExit attend l'arrêt de l'auxiliaire et renvoie son code de sortie.
func (s *Service) waitExit(ctx context.Context, id string) (int, error) {
	for {
		in, err := s.dk.InspectFull(ctx, id)
		if err != nil {
			return 0, err
		}
		if !in.State.Running && in.State.Status != "created" {
			return in.State.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(s.poll):
		}
	}
}

// removeHelper supprime l'auxiliaire (arrêté de force s'il tourne encore) et son éventuel volume anonyme.
func (s *Service) removeHelper(h *helper) {
	if h.removed {
		return
	}
	h.removed = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.dk.RemoveWithVolumes(ctx, h.id); err != nil && !errors.Is(err, docker.ErrNotFound) {
		s.log.Warn("restauration : conteneur auxiliaire non supprimé", "conteneur", h.name, "erreur", err)
	}
	s.transient(h.name, false)
}
