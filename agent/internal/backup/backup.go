// Package backup sauvegarde des volumes Docker, des dossiers montés dans les conteneurs et les
// données de Vigie vers un stockage compatible S3 : archive tar lue via l'API Docker, compressée
// et envoyée en flux (mémoire bornée à une partie d'envoi multipart), rétention par élément,
// planification quotidienne et lancement manuel, suivis comme les autres opérations de l'agent.
package backup

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/karelisio/vigie/agent/internal/cleanup"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
)

// ErrNotConfigured : adresse, bucket ou clés du stockage manquants.
var ErrNotConfigured = errors.New("stockage incomplet")

// stampRe reconnaît le nom d'une copie : seules celles-ci sont concernées par la rétention.
var stampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{4}\.tar\.gz$`)

const stampLayout = "2006-01-02_1504"

// Options relie le service au reste de l'agent.
type Options struct {
	DataDir    string
	SelfID     string                    // conteneur de l'agent : jamais arrêté ni proposé
	NoteAction func(name, action string) // évite les fausses alertes pendant un arrêt
}

type Service struct {
	st  *store.Store
	dk  *docker.Client
	upd *updater.Manager
	log *slog.Logger
	opt Options

	part int // taille d'une partie d'envoi (réduite dans les tests)

	mu    sync.Mutex // configuration et historique
	sched schedule
}

func New(st *store.Store, dk *docker.Client, upd *updater.Manager, log *slog.Logger, opt Options) *Service {
	return &Service{st: st, dk: dk, upd: upd, log: log, opt: opt, part: partSize}
}

func (s *Service) note(name, action string) {
	if s.opt.NoteAction != nil {
		s.opt.NoteAction(name, action)
	}
}

// Start lance une sauvegarde (trigger : manual ou schedule) sous le verrou des opérations.
func (s *Service) Start(trigger string) (updater.Job, error) {
	cfg, sec := s.load(context.Background())
	if err := cfg.ready(sec); err != nil {
		return updater.Job{}, err
	}
	cl, err := newS3(cfg, sec)
	if err != nil {
		return updater.Job{}, err
	}
	return s.upd.Run("backup", "backup", "sauvegarde", func(ctx context.Context, progress func(float64, string)) (string, error) {
		return s.run(ctx, cl, cfg, trigger, progress)
	})
}

func (s *Service) run(ctx context.Context, cl *s3Client, cfg Config, trigger string, progress func(float64, string)) (string, error) {
	start := time.Now()
	rec := Record{TS: start.UnixMilli(), Trigger: trigger, Items: []ItemResult{}}
	progress(0, "Recherche des éléments à sauvegarder…")
	srcs, err := s.sources(ctx)
	if err != nil {
		rec.Error = err.Error()
		rec.Duration = time.Since(start).Milliseconds()
		s.addHistory(context.Background(), rec)
		return "", err
	}
	history := s.History(ctx)
	buf := make([]byte, s.part)
	stamp := start.Format(stampLayout)
	n := float64(len(cfg.Items))
	var failed []string
	for i, sel := range cfg.Items {
		step := func(p float64, msg string) { progress((float64(i)+min(p, 1))/n, msg) }
		r := s.backupItem(ctx, cl, cfg, sel, srcs[sel.ID], stamp, buf, lastRaw(history, sel.ID), step)
		rec.Items = append(rec.Items, r)
		rec.Size += r.Size
		if !r.OK {
			failed = append(failed, r.Name+" : "+r.Error)
		}
	}
	rec.Duration = time.Since(start).Milliseconds()
	rec.OK = len(failed) == 0
	summary := fmt.Sprintf("%s, %s envoyés", plural(len(rec.Items), "élément sauvegardé", "éléments sauvegardés"), cleanup.HumanBytes(rec.Size))
	if !rec.OK {
		rec.Error = fmt.Sprintf("%s sur %d en échec — %s", plural(len(failed), "élément", "éléments"), len(rec.Items), strings.Join(failed, " ; "))
	}
	s.addHistory(context.Background(), rec)
	if !rec.OK {
		return "", errors.New(rec.Error)
	}
	return summary, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// backupItem envoie une copie d'un élément puis applique la rétention.
func (s *Service) backupItem(ctx context.Context, cl *s3Client, cfg Config, sel Selection, src *source,
	stamp string, buf []byte, estimate int64, step func(float64, string)) ItemResult {
	r := ItemResult{ID: sel.ID, Name: displayName(sel.ID)}
	folder, err := itemKey(sel.ID)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	key := path.Join(cfg.Prefix, folder, stamp+".tar.gz")
	size, raw, err := s.copyItem(ctx, cl, sel, src, key, buf, estimate, step)
	r.Raw = raw
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Key, r.Size = key, size
	step(0.95, r.Name+" : suppression des copies les plus anciennes…")
	if err := s.prune(ctx, cl, path.Join(cfg.Prefix, folder), cfg.Retention); err != nil {
		r.Error = "copie envoyée, mais les plus anciennes n'ont pas pu être supprimées : " + err.Error()
		return r
	}
	r.OK = true
	return r
}

// copyItem envoie l'archive de l'élément sous key. Avec sel.Stop, les conteneurs qui l'utilisent
// sont arrêtés pendant la copie et toujours relancés juste après, même en cas d'erreur ou de délai
// dépassé.
func (s *Service) copyItem(ctx context.Context, cl *s3Client, sel Selection, src *source, key string,
	buf []byte, estimate int64, step func(float64, string)) (size, raw int64, err error) {
	name := displayName(sel.ID)
	var write func(io.Writer) error
	switch {
	case sel.ID == vigieID:
		write = func(w io.Writer) error { return s.writeVigie(ctx, w) }
	case src == nil:
		return 0, 0, errors.New("introuvable : aucun conteneur ne l'utilise")
	default:
		if sel.Stop {
			stopped, serr := s.stopUsers(ctx, src.users, name, step)
			defer func() {
				if rerr := s.restart(stopped, name, step); rerr != nil {
					if err != nil {
						err = fmt.Errorf("%w ; %w", err, rerr)
					} else {
						err = rerr
					}
				}
			}()
			if serr != nil {
				return 0, 0, serr
			}
		}
		from := src.users[0]
		write = func(w io.Writer) error {
			body, err := s.dk.Archive(ctx, from.id, from.dest)
			if err != nil {
				return fmt.Errorf("lecture depuis %s : %w", from.name, err)
			}
			defer body.Close()
			_, err = io.Copy(w, body)
			return err
		}
	}
	step(0, name+" : copie…")
	size, raw, err = s.send(ctx, cl, key, buf, write, func(raw, sent int64) {
		p := 0.0
		if estimate > 0 {
			p = min(0.9, 0.9*float64(raw)/float64(estimate))
		}
		step(p, fmt.Sprintf("%s : %s lus, %s envoyés", name, cleanup.HumanBytes(raw), cleanup.HumanBytes(sent)))
	})
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = errors.New("durée maximale de l'opération dépassée")
	}
	return size, raw, err
}

// stopUsers arrête les conteneurs en marche qui utilisent l'élément et renvoie ceux qui l'ont été
// (à relancer, même en cas d'erreur).
func (s *Service) stopUsers(ctx context.Context, users []user, item string, step func(float64, string)) ([]user, error) {
	var stopped []user
	for _, u := range users {
		if !u.running {
			continue
		}
		step(0, fmt.Sprintf("%s : arrêt de %s…", item, u.name))
		s.note(u.name, "stop")
		if err := s.dk.StopWait(ctx, u.id, u.stopWait); err != nil {
			return stopped, fmt.Errorf("arrêt de %s : %w", u.name, err)
		}
		stopped = append(stopped, u)
	}
	return stopped, nil
}

// restart relance les conteneurs arrêtés pour la copie, avec un contexte indépendant de celui de
// l'opération (qui peut être annulé ou expiré).
func (s *Service) restart(stopped []user, item string, step func(float64, string)) error {
	var failed []string
	for _, u := range stopped {
		step(1, fmt.Sprintf("%s : redémarrage de %s…", item, u.name))
		s.note(u.name, "start")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := s.dk.Start(ctx, u.id)
		cancel()
		if err != nil {
			s.log.Error("sauvegarde : conteneur non redémarré", "conteneur", u.name, "erreur", err)
			failed = append(failed, fmt.Sprintf("%s (%v)", u.name, err))
		}
	}
	if len(failed) > 0 {
		return errors.New("redémarrage impossible : " + strings.Join(failed, ", "))
	}
	return nil
}

// countWriter compte les octets écrits et signale l'avancement au plus deux fois par seconde.
type countWriter struct {
	w     io.Writer
	n     atomic.Int64
	last  time.Time
	tick  func(raw int64)
	every time.Duration
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	raw := c.n.Add(int64(n))
	if time.Since(c.last) >= c.every {
		c.last = time.Now()
		c.tick(raw)
	}
	return n, err
}

// send compresse en flux (gzip) ce qu'écrit write et l'envoie sous key. Renvoie les tailles
// compressée et brute. La mémoire utilisée se limite à buf et au compresseur.
func (s *Service) send(ctx context.Context, cl *s3Client, key string, buf []byte, write func(io.Writer) error,
	progress func(raw, sent int64)) (int64, int64, error) {
	var sent atomic.Int64
	pr, pw := io.Pipe()
	cw := &countWriter{every: 500 * time.Millisecond, tick: func(raw int64) { progress(raw, sent.Load()) }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Compression rapide : l'agent dispose d'un demi-processeur et les conteneurs arrêtés
		// pour la copie attendent la fin de l'envoi.
		zw, _ := gzip.NewWriterLevel(pw, gzip.BestSpeed)
		cw.w = zw
		err := write(cw)
		if err == nil {
			err = zw.Close()
		}
		pw.CloseWithError(err) // nil : fin du flux
	}()
	size, err := cl.Upload(ctx, key, pr, buf, func(n int64) {
		sent.Store(n)
		progress(cw.n.Load(), n)
	})
	// Envoi interrompu : l'écriture en attente échoue et la goroutine se termine.
	pr.CloseWithError(err)
	<-done
	return size, cw.n.Load(), err
}

// prune garde les keep copies les plus récentes du dossier et supprime les autres. Seuls les
// fichiers nommés comme une copie de Vigie sont concernés.
func (s *Service) prune(ctx context.Context, cl *s3Client, folder string, keep int) error {
	objs, err := cl.list(ctx, folder+"/", 100000)
	if err != nil {
		return err
	}
	var copies []string
	for _, o := range objs {
		if stampRe.MatchString(strings.TrimPrefix(o.Key, folder+"/")) {
			copies = append(copies, o.Key)
		}
	}
	sort.Strings(copies) // l'horodatage du nom suit l'ordre chronologique
	for _, k := range copies[:max(0, len(copies)-keep)] {
		if err := cl.delete(ctx, k); err != nil {
			return fmt.Errorf("%s : %w", k, err)
		}
		s.log.Info("sauvegarde supprimée (rétention)", "objet", k)
	}
	return nil
}

// Objects liste les copies présentes dans le stockage, par élément puis de la plus récente à la
// plus ancienne.
func (s *Service) Objects(ctx context.Context) ([]Object, error) {
	cfg, sec := s.load(ctx)
	if err := cfg.storageReady(sec); err != nil {
		return nil, err
	}
	cl, err := newS3(cfg, sec)
	if err != nil {
		return nil, err
	}
	base := ""
	if cfg.Prefix != "" {
		base = cfg.Prefix + "/"
	}
	objs, err := cl.list(ctx, base, 5000)
	if err != nil {
		return nil, err
	}
	out := []Object{}
	for _, o := range objs {
		item, name, ok := strings.Cut(strings.TrimPrefix(o.Key, base), "/")
		if ok && stampRe.MatchString(name) {
			o.Item = item
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Item != out[j].Item {
			return out[i].Item < out[j].Item
		}
		return out[i].Key > out[j].Key
	})
	return out, nil
}

// Test vérifie l'accès au stockage avec la configuration cfg, pas encore enregistrée (secrets vides
// ou masqués : ceux enregistrés), en écrivant, listant puis supprimant un petit fichier.
func (s *Service) Test(ctx context.Context, cfg Config) (string, error) {
	if err := cfg.normalize(); err != nil {
		return "", err
	}
	saved, old := s.load(ctx)
	sec, err := merge(cfg, saved, old)
	if err != nil {
		return "", err
	}
	if err := cfg.storageReady(sec); err != nil {
		return "", err
	}
	cl, err := newS3(cfg, sec)
	if err != nil {
		return "", err
	}
	key := path.Join(cfg.Prefix, ".vigie-test")
	if err := cl.put(ctx, key, []byte("Vigie : test de connexion\n")); err != nil {
		return "", fmt.Errorf("écriture : %w", err)
	}
	if _, err := cl.list(ctx, key, 1); err != nil {
		return "", fmt.Errorf("liste : %w", err)
	}
	if err := cl.delete(ctx, key); err != nil {
		return "", fmt.Errorf("suppression : %w", err)
	}
	return "Connexion réussie : écriture, liste et suppression autorisées", nil
}
