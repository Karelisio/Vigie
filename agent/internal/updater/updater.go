// Package updater met à jour les conteneurs depuis l'appli : téléchargement de la nouvelle image,
// puis recréation à l'identique (nom, réseaux, volumes, labels compose), avec retour à l'ancien
// conteneur si le nouveau ne démarre pas. Seule l'image déjà configurée sur le conteneur est
// téléchargée : aucune image ni configuration ne vient de l'appli.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
)

var (
	ErrBusy     = errors.New("une mise à jour est déjà en cours, attends qu'elle se termine")
	ErrDisabled = errors.New("les mises à jour sont désactivées sur cet agent (VIGIE_ALLOW_UPDATES=false)")
	ErrPinned   = errors.New("image épinglée par digest : rien à mettre à jour")
)

// Result est le bilan d'un conteneur dans une opération.
type Result struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Updated bool   `json:"updated"`
	Message string `json:"message"`
}

// Job décrit une opération en cours ou terminée, diffusée en direct à l'appli.
type Job struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`   // update, pull, self, stack, batch
	Action     string   `json:"action"` // update, pull, restart, start, stop
	Target     string   `json:"target"` // conteneur, stack ou « all »
	Phase      string   `json:"phase"`  // queued, pulling, recreating, verifying, done, failed
	Progress   float64  `json:"progress"`
	Message    string   `json:"message"`
	Done       bool     `json:"done"`
	OK         bool     `json:"ok"`
	Error      string   `json:"error,omitempty"`
	Results    []Result `json:"results"`
	StartedAt  int64    `json:"started_at"`
	FinishedAt int64    `json:"finished_at,omitempty"`
}

// Member est un conteneur concerné par une opération de groupe.
type Member struct {
	ID      string
	Name    string
	Self    bool
	Running bool
}

// Hooks relie le gestionnaire au reste de l'agent (tous facultatifs).
type Hooks struct {
	NoteAction   func(name, action string)  // évite les fausses alertes pendant l'opération
	SetTransient func(name string, on bool) // masque l'ancien conteneur renommé
	Publish      func(Job)                  // diffusion WebSocket
	Finished     func(Job)                  // journal, notification
	Changed      func()                     // revérifier les images, rafraîchir la liste
}

type Options struct {
	Enabled     bool
	SelfID      string
	PruneImages func() bool
	DataDir     string
	Version     string
}

type Manager struct {
	dk    *docker.Client
	log   *slog.Logger
	hooks Hooks
	opt   Options

	// Délais de vérification (raccourcis dans les tests).
	settle, crashGrace, poll, helperDelay time.Duration

	mu       sync.Mutex
	current  *Job
	recent   []Job
	lastPub  time.Time
	multiNet *bool
}

func New(dk *docker.Client, log *slog.Logger, hooks Hooks, opt Options) *Manager {
	return &Manager{
		dk: dk, log: log, hooks: hooks, opt: opt,
		settle: 8 * time.Second, crashGrace: 2 * time.Second, poll: time.Second, helperDelay: 3 * time.Second,
	}
}

func (m *Manager) Enabled() bool { return m.opt.Enabled }

// Jobs renvoie l'opération en cours puis les plus récentes.
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Job{}
	if m.current != nil {
		out = append(out, *m.current)
	}
	for i := len(m.recent) - 1; i >= 0; i-- {
		out = append(out, m.recent[i])
	}
	return out
}

// ---- Démarrage des opérations ----

func (m *Manager) start(kind, action, target string, run func(ctx context.Context, j *Job) error) (Job, error) {
	// Redémarrer, démarrer ou arrêter une stack reste possible même sans les mises à jour d'images.
	if (action == "update" || action == "pull" || action == "cleanup") && !m.opt.Enabled {
		return Job{}, ErrDisabled
	}
	m.mu.Lock()
	if m.current != nil {
		m.mu.Unlock()
		return Job{}, ErrBusy
	}
	j := &Job{
		ID: strconv.FormatInt(time.Now().UnixNano(), 36), Kind: kind, Action: action, Target: target,
		Phase: "queued", Message: "En file d'attente…", Results: []Result{}, StartedAt: time.Now().UnixMilli(),
	}
	m.current = j
	snapshot := *j
	m.mu.Unlock()
	m.publish(true)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		err := run(ctx, j)
		m.mu.Lock()
		j.Done = true
		j.FinishedAt = time.Now().UnixMilli()
		if err != nil {
			j.OK = false
			j.Phase = "failed"
			j.Error = err.Error()
			if j.Message == "" || !strings.HasPrefix(j.Message, "Échec") {
				j.Message = "Échec : " + err.Error()
			}
		} else {
			j.OK = true
			j.Phase = "done"
		}
		j.Progress = 1
		done := *j
		m.current = nil
		m.recent = append(m.recent, done)
		if len(m.recent) > 20 {
			m.recent = m.recent[len(m.recent)-20:]
		}
		m.mu.Unlock()
		if m.hooks.Publish != nil {
			m.hooks.Publish(done)
		}
		if m.hooks.Finished != nil {
			m.hooks.Finished(done)
		}
		if m.hooks.Changed != nil {
			m.hooks.Changed()
		}
		m.log.Info("opération terminée", "type", done.Kind, "cible", done.Target, "ok", done.OK, "message", done.Message)
	}()
	return snapshot, nil
}

// set modifie l'opération en cours et la diffuse (au plus 4 fois par seconde, sauf changement de phase).
func (m *Manager) set(j *Job, phase string, progress float64, msg string) {
	m.mu.Lock()
	changed := j.Phase != phase
	j.Phase = phase
	if progress >= 0 {
		j.Progress = math.Min(1, progress)
	}
	if msg != "" {
		j.Message = msg
	}
	m.mu.Unlock()
	m.publish(changed)
}

func (m *Manager) addResult(j *Job, r Result) {
	m.mu.Lock()
	j.Results = append(j.Results, r)
	m.mu.Unlock()
}

func (m *Manager) publish(force bool) {
	m.mu.Lock()
	if m.current == nil || (!force && time.Since(m.lastPub) < 250*time.Millisecond) {
		m.mu.Unlock()
		return
	}
	m.lastPub = time.Now()
	j := *m.current
	j.Results = append([]Result{}, j.Results...)
	m.mu.Unlock()
	if m.hooks.Publish != nil {
		m.hooks.Publish(j)
	}
}

func (m *Manager) note(name, action string) {
	if m.hooks.NoteAction != nil {
		m.hooks.NoteAction(name, action)
	}
}

// Run exécute une opération quelconque (ex. nettoyage) sous le même verrou que les mises à jour.
// fn renvoie le message de bilan affiché à la fin.
func (m *Manager) Run(kind, action, target string, fn func(ctx context.Context, progress func(p float64, msg string)) (string, error)) (Job, error) {
	return m.start(kind, action, target, func(ctx context.Context, j *Job) error {
		msg, err := fn(ctx, func(p float64, s string) { m.set(j, "running", p, s) })
		if err == nil {
			m.set(j, "done", 1, msg)
		}
		return err
	})
}

// UpdateContainer télécharge l'image du conteneur puis le recrée (ou se contente du téléchargement).
func (m *Manager) UpdateContainer(c Member, pullOnly bool) (Job, error) {
	switch {
	case pullOnly:
		return m.start("pull", "pull", c.Name, func(ctx context.Context, j *Job) error {
			r, err := m.pullOne(ctx, j, c, 0, 1)
			m.addResult(j, r)
			if err == nil {
				m.set(j, "done", 1, r.Message)
			}
			return err
		})
	case c.Self || c.ID == m.opt.SelfID:
		return m.start("self", "update", c.Name, func(ctx context.Context, j *Job) error {
			return m.updateSelf(ctx, j, c.ID, 0, 1)
		})
	default:
		return m.start("update", "update", c.Name, func(ctx context.Context, j *Job) error {
			r, err := m.updateOne(ctx, j, c, 0, 1)
			m.addResult(j, r)
			if err == nil {
				m.set(j, "done", 1, r.Message)
			}
			return err
		})
	}
}

// Group applique une action à plusieurs conteneurs (une stack compose, ou toutes les mises à jour).
func (m *Manager) Group(kind, target, action string, members []Member) (Job, error) {
	switch action {
	case "update", "pull", "restart", "start", "stop":
	default:
		return Job{}, fmt.Errorf("action inconnue %q", action)
	}
	if len(members) == 0 {
		return Job{}, errors.New("aucun conteneur concerné")
	}
	return m.start(kind, action, target, func(ctx context.Context, j *Job) error {
		return m.runGroup(ctx, j, action, members)
	})
}

func (m *Manager) runGroup(ctx context.Context, j *Job, action string, members []Member) error {
	ordered := m.order(ctx, members)
	n := float64(len(ordered))
	var self *Member
	failed, updated := 0, 0
	for i, c := range ordered {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.Self || c.ID == m.opt.SelfID {
			cc := c
			self = &cc
			continue
		}
		base, span := float64(i)/n, 1/n
		var r Result
		var err error
		switch action {
		case "update":
			r, err = m.updateOne(ctx, j, c, base, span)
		case "pull":
			r, err = m.pullOne(ctx, j, c, base, span)
		default:
			r, err = m.simple(ctx, j, c, action, base)
		}
		if err != nil {
			failed++
		}
		if r.Updated {
			updated++
		}
		m.addResult(j, r)
	}
	summary := groupSummary(action, len(ordered), updated, failed)
	if self != nil {
		switch action {
		case "update":
			// L'agent passe en dernier : il redémarre à la fin de l'opération.
			if err := m.updateSelf(ctx, j, self.ID, (n-1)/n, 1/n); err != nil {
				failed++
				m.addResult(j, Result{Name: self.Name, Message: err.Error()})
			} else {
				m.addResult(j, Result{Name: self.Name, OK: true, Updated: true, Message: "l'agent redémarre avec la nouvelle version"})
			}
			summary = groupSummary(action, len(ordered), updated, failed)
		case "pull":
			r, err := m.pullOne(ctx, j, *self, (n-1)/n, 1/n)
			if err != nil {
				failed++
			}
			m.addResult(j, r)
		case "restart":
			m.addResult(j, Result{Name: self.Name, OK: true, Message: "ignoré : redémarre l'agent seul"})
		default:
			m.addResult(j, Result{Name: self.Name, OK: true, Message: "ignoré (agent Vigie)"})
		}
	}
	m.set(j, "done", 1, summary)
	if failed > 0 {
		return fmt.Errorf("%s", summary)
	}
	return nil
}

func groupSummary(action string, total, updated, failed int) string {
	var parts []string
	switch action {
	case "update":
		if updated > 0 {
			parts = append(parts, plural(updated, "mis à jour", "mis à jour"))
		}
		if rest := total - updated - failed; rest > 0 {
			parts = append(parts, plural(rest, "déjà à jour", "déjà à jour"))
		}
	default:
		if ok := total - failed; ok > 0 {
			parts = append(parts, plural(ok, "réussi", "réussis"))
		}
	}
	if failed > 0 {
		parts = append(parts, plural(failed, "échec", "échecs"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// order place les conteneurs qui partagent le réseau d'un autre après celui-ci.
func (m *Manager) order(ctx context.Context, members []Member) []Member {
	dependent := map[string]bool{}
	for _, c := range members {
		if in, err := m.dk.InspectFull(ctx, c.ID); err == nil && strings.HasPrefix(str(in.HostConfig, "NetworkMode"), "container:") {
			dependent[c.ID] = true
		}
	}
	out := append([]Member{}, members...)
	sort.SliceStable(out, func(a, b int) bool {
		if dependent[out[a].ID] != dependent[out[b].ID] {
			return !dependent[out[a].ID]
		}
		return out[a].Name < out[b].Name
	})
	return out
}

func (m *Manager) simple(ctx context.Context, j *Job, c Member, action string, base float64) (Result, error) {
	verbs := map[string]string{"restart": "redémarrage", "start": "démarrage", "stop": "arrêt"}
	m.set(j, "running", base, fmt.Sprintf("%s : %s…", c.Name, verbs[action]))
	m.note(c.Name, action)
	var err error
	switch action {
	case "restart":
		err = m.dk.Restart(ctx, c.ref(), 15*time.Second)
	case "start":
		if c.Running {
			return Result{Name: c.Name, OK: true, Message: "déjà démarré"}, nil
		}
		err = m.dk.Start(ctx, c.ref())
	case "stop":
		if !c.Running {
			return Result{Name: c.Name, OK: true, Message: "déjà arrêté"}, nil
		}
		err = m.stop(ctx, c.ref(), 15*time.Second)
	}
	if err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	done := map[string]string{"restart": "redémarré", "start": "démarré", "stop": "arrêté"}
	return Result{Name: c.Name, OK: true, Message: done[action]}, nil
}

// ---- Téléchargement ----

func imageRef(c *docker.ContainerJSON) (string, error) {
	ref := str(c.Config, "Image")
	if ref == "" || strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
		return "", ErrPinned
	}
	return ref, nil
}

type layer struct{ dlCur, dlTotal, exCur, exTotal int64 }

// pull télécharge l'image du conteneur et renvoie l'ID de l'image obtenue.
func (m *Manager) pull(ctx context.Context, j *Job, c *docker.ContainerJSON, base, span float64) (string, error) {
	ref, err := imageRef(c)
	if err != nil {
		return "", err
	}
	name := c.CleanName()
	m.set(j, "pulling", base, fmt.Sprintf("%s : téléchargement de %s…", name, ref))
	layers := map[string]*layer{}
	err = m.dk.Pull(ctx, ref, func(ev docker.PullEvent) {
		if ev.ID == "" {
			return
		}
		l := layers[ev.ID]
		if l == nil {
			l = &layer{}
			layers[ev.ID] = l
		}
		switch ev.Status {
		case "Downloading":
			l.dlCur, l.dlTotal = ev.Progress.Current, ev.Progress.Total
		case "Extracting":
			l.exCur, l.exTotal = ev.Progress.Current, ev.Progress.Total
		case "Download complete":
			l.dlCur = l.dlTotal
		case "Pull complete", "Already exists":
			l.dlCur, l.exCur = l.dlTotal, l.exTotal
			if l.dlTotal == 0 {
				l.dlCur, l.dlTotal = 1, 1
			}
		default:
			return
		}
		var cur, total int64
		for _, x := range layers {
			cur += x.dlCur + x.exCur
			total += x.dlTotal + x.exTotal
		}
		if total > 0 {
			p := float64(cur) / float64(total)
			m.set(j, "pulling", base+span*0.7*p, fmt.Sprintf("%s : téléchargement %d %%", name, int(p*100)))
		}
	})
	if err != nil {
		return "", fmt.Errorf("téléchargement de %s : %w", ref, explainPull(err))
	}
	img, err := m.dk.ImageDetails(ctx, ref)
	if err != nil {
		return "", err
	}
	return img.ID, nil
}

func explainPull(err error) error {
	msg := err.Error()
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "denied") || strings.Contains(msg, "authentication required") {
		return fmt.Errorf("%w (image privée ou construite localement : mets-la à jour depuis Dockhand)", err)
	}
	return err
}

// ref désigne le conteneur par son nom : son ID change s'il a été recréé entre-temps
// (conteneur lié au réseau d'un autre, déjà mis à jour plus tôt dans la même opération).
func (c Member) ref() string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

func (m *Manager) pullOne(ctx context.Context, j *Job, c Member, base, span float64) (Result, error) {
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	id, err := m.pull(ctx, j, in, base, span)
	if err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	if id == in.Image {
		return Result{Name: c.Name, OK: true, Message: c.Name + " : image déjà à jour"}, nil
	}
	return Result{Name: c.Name, OK: true, Updated: true,
		Message: c.Name + " : nouvelle image téléchargée, appliquée à la prochaine mise à jour"}, nil
}

// ---- Mise à jour d'un conteneur ----

func (m *Manager) updateOne(ctx context.Context, j *Job, c Member, base, span float64) (Result, error) {
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	newImage, err := m.pull(ctx, j, in, base, span)
	if err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	if newImage == in.Image {
		return Result{Name: c.Name, OK: true, Message: c.Name + " : déjà à jour"}, nil
	}
	if _, err := m.Replace(ctx, j, in, newImage, "", base+span*0.7, span*0.3); err != nil {
		return Result{Name: c.Name, Message: err.Error()}, err
	}
	m.prune(ctx, in.Image)
	return Result{Name: c.Name, OK: true, Updated: true, Message: c.Name + " mis à jour"}, nil
}

func (m *Manager) prune(ctx context.Context, imageID string) {
	if m.opt.PruneImages == nil || !m.opt.PruneImages() {
		return
	}
	// Docker refuse si un autre conteneur utilise encore l'image : c'est voulu.
	if err := m.dk.RemoveImage(ctx, imageID); err == nil {
		m.log.Info("ancienne image supprimée", "image", short(imageID))
	}
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (m *Manager) supportsMultiNet(ctx context.Context) bool {
	m.mu.Lock()
	if m.multiNet != nil {
		v := *m.multiNet
		m.mu.Unlock()
		return v
	}
	m.mu.Unlock()
	v := false
	if ver, err := m.dk.Version(ctx); err == nil {
		v = apiAtLeast(ver.APIVersion, 1, 44)
	}
	m.mu.Lock()
	m.multiNet = &v
	m.mu.Unlock()
	return v
}

func apiAtLeast(v string, major, minor int) bool {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return false
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return a > major || (a == major && b >= minor)
}

func stopTimeout(c *docker.ContainerJSON) time.Duration {
	if n, ok := c.Config["StopTimeout"].(json.Number); ok {
		if s, err := n.Int64(); err == nil && s > 0 && s <= 300 {
			return time.Duration(s) * time.Second
		}
	}
	return 15 * time.Second
}

func (m *Manager) stop(ctx context.Context, id string, timeout time.Duration) error {
	return m.dk.StopWait(ctx, id, timeout)
}

// dependents renvoie les conteneurs qui utilisent le réseau de c (network_mode: service:… / container:…).
func (m *Manager) dependents(ctx context.Context, c *docker.ContainerJSON) []*docker.ContainerJSON {
	list, err := m.dk.List(ctx)
	if err != nil {
		return nil
	}
	var out []*docker.ContainerJSON
	for _, x := range list {
		if x.ID == c.ID {
			continue
		}
		in, err := m.dk.InspectFull(ctx, x.ID)
		if err != nil {
			continue
		}
		mode := str(in.HostConfig, "NetworkMode")
		if mode == "container:"+c.ID || mode == "container:"+c.CleanName() {
			out = append(out, in)
		}
	}
	return out
}

// Replace recrée old avec la configuration d'origine et l'image désormais associée à sa référence.
// En cas d'échec, l'ancien conteneur est remis en place et relancé.
func (m *Manager) Replace(ctx context.Context, j *Job, old *docker.ContainerJSON, newImageID, networkMode string, base, span float64) (string, error) {
	name := old.CleanName()
	wasRunning := old.State.Running || old.State.Restarting
	var oldImage map[string]any
	if img, err := m.dk.ImageDetails(ctx, old.Image); err == nil {
		oldImage = img.Config
	}
	spec := buildCreate(old, oldImage, newImageID, networkMode, m.supportsMultiNet(ctx))
	deps := m.dependents(ctx, old)

	m.note(name, "update")
	for _, d := range deps {
		m.note(d.CleanName(), "update")
		if d.State.Running {
			_ = m.stop(ctx, d.ID, stopTimeout(d))
		}
	}
	restartDeps := func() {
		for _, d := range deps {
			if d.State.Running {
				_ = m.dk.Start(context.Background(), d.ID)
			}
		}
	}

	m.set(j, "recreating", base, name+" : arrêt de l'ancien conteneur…")
	if wasRunning {
		if err := m.stop(ctx, old.ID, stopTimeout(old)); err != nil {
			restartDeps()
			return "", fmt.Errorf("arrêt de %s : %w", name, err)
		}
	}
	tmp := fmt.Sprintf("%s-vigie-old-%d", name, time.Now().Unix())
	if m.hooks.SetTransient != nil {
		m.hooks.SetTransient(tmp, true)
		defer m.hooks.SetTransient(tmp, false)
	}
	if err := m.dk.Rename(ctx, old.ID, tmp); err != nil {
		if wasRunning {
			_ = m.dk.Start(context.Background(), old.ID)
		}
		restartDeps()
		return "", fmt.Errorf("renommage de %s : %w", name, err)
	}

	newID := ""
	fail := func(err error) (string, error) {
		m.log.Warn("mise à jour annulée, retour à l'ancien conteneur", "conteneur", name, "erreur", err)
		m.set(j, "rollback", -1, name+" : échec, retour à l'ancienne version…")
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if newID != "" {
			_ = m.dk.Remove(rctx, newID, true)
		}
		if err := m.dk.Rename(rctx, old.ID, name); err != nil {
			m.log.Error("impossible de rendre son nom à l'ancien conteneur", "conteneur", tmp, "erreur", err)
		}
		if wasRunning {
			_ = m.dk.Start(rctx, old.ID)
		}
		restartDeps()
		return "", fmt.Errorf("%w (ancienne version rétablie)", err)
	}

	m.set(j, "recreating", base+span*0.3, name+" : création du nouveau conteneur…")
	id, err := m.dk.Create(ctx, name, spec.Body)
	if err != nil {
		return fail(fmt.Errorf("création : %w", err))
	}
	newID = id
	for net, ep := range spec.Extra {
		if err := m.dk.ConnectNetwork(ctx, net, newID, ep); err != nil {
			return fail(fmt.Errorf("réseau %s : %w", net, err))
		}
	}
	if wasRunning {
		m.set(j, "verifying", base+span*0.5, name+" : démarrage…")
		if err := m.dk.Start(ctx, newID); err != nil {
			return fail(fmt.Errorf("démarrage : %w", err))
		}
		if err := m.waitReady(ctx, j, newID, name, base+span*0.6); err != nil {
			return fail(err)
		}
	}

	// Les conteneurs qui partageaient son réseau sont rattachés au nouveau. Si l'un d'eux échoue,
	// tout est annulé : un conteneur privé de réseau serait pire qu'une mise à jour reportée.
	var moved []string
	for _, d := range deps {
		nid, err := m.Replace(ctx, j, d, "", "container:"+newID, base+span*0.9, 0)
		if err != nil {
			_, ferr := fail(fmt.Errorf("conteneur lié %s : %w", d.CleanName(), err))
			for _, id := range moved {
				if in, e := m.dk.InspectFull(context.Background(), id); e == nil {
					if _, e := m.Replace(context.Background(), j, in, "", "container:"+old.ID, base, 0); e != nil {
						m.log.Error("conteneur lié non rattaché à l'ancienne version", "conteneur", in.CleanName(), "erreur", e)
					}
				}
			}
			return "", ferr
		}
		moved = append(moved, nid)
	}
	if err := m.dk.Remove(ctx, old.ID, false); err != nil {
		m.log.Warn("ancien conteneur non supprimé", "conteneur", tmp, "erreur", err)
	}
	m.log.Info("conteneur recréé", "conteneur", name, "image", short(newImageID))
	return newID, nil
}

// waitReady attend que le nouveau conteneur tourne (et soit en bonne santé s'il a un healthcheck).
func (m *Manager) waitReady(ctx context.Context, j *Job, id, name string, progress float64) error {
	start := time.Now()
	for {
		in, err := m.dk.InspectFull(ctx, id)
		if err != nil {
			return err
		}
		elapsed := time.Since(start)
		switch {
		case in.State.Restarting:
			return m.startFailure(ctx, id, name, "redémarre en boucle")
		case !in.State.Running && elapsed > m.crashGrace:
			return m.startFailure(ctx, id, name, fmt.Sprintf("s'est arrêté (code %d)", in.State.ExitCode))
		case in.State.Health != nil:
			switch in.State.Health.Status {
			case "healthy":
				return nil
			case "unhealthy":
				return m.startFailure(ctx, id, name, "healthcheck en échec")
			}
			m.set(j, "verifying", progress, name+" : attente du healthcheck…")
			if elapsed > 120*time.Second {
				return nil // toujours « starting » : on laisse le healthcheck se poursuivre
			}
		case in.State.Running && elapsed > m.settle:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.poll):
		}
	}
}

func (m *Manager) startFailure(ctx context.Context, id, name, what string) error {
	msg := fmt.Sprintf("le nouveau %s %s", name, what)
	if lines := m.dk.LogTail(ctx, id, 3); len(lines) > 0 {
		msg += " — " + strings.Join(lines, " | ")
	}
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return errors.New(msg)
}

// ---- Mise à jour de l'agent lui-même ----

// SelfUpdateResult est écrit par le conteneur auxiliaire, puis lu par l'agent redémarré.
type SelfUpdateResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	From  string `json:"from"`
	TS    int64  `json:"ts"`
}

const resultFile = "self-update.json"

func (m *Manager) updateSelf(ctx context.Context, j *Job, selfID string, base, span float64) error {
	self, err := m.dk.InspectFull(ctx, selfID)
	if err != nil {
		return err
	}
	newImage, err := m.pull(ctx, j, self, base, span)
	if err != nil {
		return err
	}
	if newImage == self.Image {
		m.set(j, "done", base+span, "L'agent est déjà à jour")
		return nil
	}
	m.set(j, "recreating", base+span*0.8, "Lancement du remplacement de l'agent…")
	prune := m.opt.PruneImages != nil && m.opt.PruneImages()
	helper := buildHelper(self, "VIGIE_HELPER_PRUNE="+strconv.FormatBool(prune))
	name := fmt.Sprintf("%s-maj-%d", self.CleanName(), time.Now().Unix())
	id, err := m.dk.Create(ctx, name, helper)
	if err != nil {
		return fmt.Errorf("conteneur de mise à jour : %w", err)
	}
	if err := m.dk.Start(ctx, id); err != nil {
		_ = m.dk.Remove(ctx, id, true)
		return fmt.Errorf("conteneur de mise à jour : %w", err)
	}
	m.set(j, "restarting", base+span, "L'agent redémarre avec la nouvelle version…")
	return nil
}

// RunHelper est exécuté par le conteneur auxiliaire : il remplace l'ancien agent, vérifie que le
// nouveau démarre, et sinon rétablit l'ancien.
func (m *Manager) RunHelper(ctx context.Context, oldID string) error {
	time.Sleep(m.helperDelay) // laisse l'ancien agent répondre à l'appli
	old, err := m.dk.InspectFull(ctx, oldID)
	if err != nil {
		return m.writeResult(err)
	}
	ref, err := imageRef(old)
	if err != nil {
		return m.writeResult(err)
	}
	img, err := m.dk.ImageDetails(ctx, ref)
	if err != nil {
		return m.writeResult(err)
	}
	j := &Job{}
	_, err = m.Replace(ctx, j, old, img.ID, "", 0, 1)
	if err == nil {
		m.prune(ctx, old.Image)
	}
	return m.writeResult(err)
}

func (m *Manager) writeResult(err error) error {
	r := SelfUpdateResult{OK: err == nil, From: m.opt.Version, TS: time.Now().UnixMilli()}
	if err != nil {
		r.Error = err.Error()
	}
	if m.opt.DataDir != "" {
		b, _ := json.Marshal(r)
		_ = os.WriteFile(filepath.Join(m.opt.DataDir, resultFile), b, 0o600)
	}
	return err
}

// LastSelfUpdate lit le résultat de la dernière mise à jour de l'agent (nil si aucune).
func LastSelfUpdate(dataDir string) *SelfUpdateResult {
	b, err := os.ReadFile(filepath.Join(dataDir, resultFile))
	if err != nil {
		return nil
	}
	var r SelfUpdateResult
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// Title résume l'opération pour une notification.
func (j Job) Title() string {
	switch j.Kind {
	case "self":
		if j.OK {
			return "Mise à jour de l'agent lancée"
		}
		return "Échec de la mise à jour de l'agent"
	case "pull":
		if j.OK {
			return "Image de " + j.Target + " téléchargée"
		}
		return "Échec du téléchargement pour " + j.Target
	case "stack":
		if j.OK {
			return "Stack " + j.Target + " : terminé"
		}
		return "Stack " + j.Target + " : échec"
	case "batch":
		if j.OK {
			return "Mises à jour terminées"
		}
		return "Mises à jour : échec"
	case "cleanup":
		if j.OK {
			return "Nettoyage terminé"
		}
		return "Échec du nettoyage"
	}
	if !j.OK {
		return "Échec de la mise à jour de " + j.Target
	}
	if len(j.Results) == 1 && !j.Results[0].Updated {
		return j.Target + " est déjà à jour"
	}
	return j.Target + " mis à jour"
}
