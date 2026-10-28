package updater

// Retour à la version précédente d'un conteneur.
//
// Avant chaque mise à jour (bouton, stack, tout mettre à jour, nuit), l'image en service est notée :
// c'est le point de retour, un par conteneur, conservé en base. Revenir en arrière recrée le
// conteneur avec cette image par la mécanique habituelle (Replace : réseaux, volumes, conteneurs
// liés, retour automatique s'il ne démarre pas).
//
// Le conteneur doit garder sa référence d'origine (ex. ghcr.io/x/y:latest) dans Config.Image :
// Dockhand et compose le reconnaissent ainsi, et la détection des mises à jour continue. Docker
// résout cette référence à la création ; elle est donc d'abord reposée localement sur l'ancienne
// image (comme `docker tag`), exactement comme un téléchargement l'avait posée sur la nouvelle.
// Créer le conteneur avec l'ID de l'image écrirait « sha256:… » dans Config.Image. Si la
// recréation échoue, la référence revient à l'image en service.
//
// L'ancienne image est gardée après la mise à jour (même avec le réglage de suppression des
// anciennes images, qui ne supprime plus que celle du point précédent). Si elle a disparu (nettoyage
// du disque), elle est re-téléchargée par son digest (dépôt@sha256:…) ; sinon le retour échoue
// clairement. La version écartée est ensuite ignorée par les mises à jour automatiques jusqu'à ce
// qu'une plus récente paraisse.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	pointsKey = "rollback_points" // points de retour, par conteneur, conservés en base
	maxPoints = 200
	// Label OCI de la version d'une image.
	versionLabel = "org.opencontainers.image.version"
)

var (
	ErrNoPoint      = errors.New("aucune version précédente connue pour ce conteneur : seules les mises à jour faites par Vigie en gardent une")
	ErrRolledBack   = errors.New("ce conteneur est déjà revenu à sa version précédente")
	ErrStalePoint   = errors.New("le conteneur a changé d'image depuis sa dernière mise à jour par Vigie : retour impossible")
	ErrRollbackSelf = errors.New("l'agent Vigie n'a pas de retour arrière manuel : il revient seul à l'ancienne version si sa mise à jour échoue")
)

// Point est l'état d'un conteneur juste avant sa dernière mise à jour par Vigie.
type Point struct {
	Container  string   `json:"container"`
	ImageRef   string   `json:"image_ref"`    // référence configurée (ex. ghcr.io/x/y:latest)
	OldImageID string   `json:"old_image_id"` // image d'avant la mise à jour
	OldDigests []string `json:"old_digests"`  // ses RepoDigests (dépôt@sha256:…)
	OldCreated int64    `json:"old_created"`  // date de construction (ms, 0 si inconnue)
	OldVersion string   `json:"old_version"`  // label org.opencontainers.image.version
	NewImageID string   `json:"new_image_id"`
	NewDigests []string `json:"new_digests"`
	NewVersion string   `json:"new_version"`
	UpdatedAt  int64    `json:"updated_at"`
	// Retour effectué (ms, 0 sinon).
	RolledBackAt int64 `json:"rolled_back_at"`
	// Ignored : la version écartée n'est plus réinstallée par les mises à jour automatiques ;
	// IgnoredDigest est son digest publié (sha256:…), quand il est connu.
	Ignored       bool   `json:"ignored"`
	IgnoredDigest string `json:"ignored_digest,omitempty"`
}

// PointView est le point de retour tel que l'appli l'affiche.
type PointView struct {
	Point
	// State : ready (retour possible), rolled_back (déjà revenu) ou stale (le conteneur a changé
	// d'image depuis, par exemple mis à jour depuis Dockhand).
	State string `json:"state"`
	// OldLabel, NewLabel : version (label OCI), sinon début du digest ou de l'ID de l'image.
	OldLabel string `json:"old_label"`
	NewLabel string `json:"new_label"`
	// ImagePresent : l'ancienne image est encore sur le disque ; sinon Downloadable indique si elle
	// peut être re-téléchargée par son digest.
	ImagePresent bool `json:"image_present"`
	Downloadable bool `json:"downloadable"`
}

func newPoint(name, ref, oldID string, oldImg *docker.ImageDetails, newID string, newImg *docker.ImageDetails) *Point {
	p := &Point{Container: name, ImageRef: ref, OldImageID: oldID, NewImageID: newID, UpdatedAt: time.Now().UnixMilli()}
	if oldImg != nil {
		p.OldDigests = oldImg.RepoDigests
		p.OldVersion = oldImg.Label(versionLabel)
		if t, err := time.Parse(time.RFC3339Nano, oldImg.Created); err == nil && t.Year() > 2000 {
			p.OldCreated = t.UnixMilli()
		}
	}
	if newImg != nil {
		p.NewDigests = newImg.RepoDigests
		p.NewVersion = newImg.Label(versionLabel)
	}
	return p
}

func shortDigest(d string) string {
	if _, h, ok := strings.Cut(d, "@"); ok {
		d = h
	}
	return short(d)
}

func label(version, ref, id string, digests []string) string {
	switch {
	case version != "":
		return version
	case images.RepoDigest(ref, digests) != "":
		return short(images.RepoDigest(ref, digests))
	case len(digests) > 0:
		return shortDigest(digests[0])
	}
	return short(id)
}

func (p *Point) OldLabel() string { return label(p.OldVersion, p.ImageRef, p.OldImageID, p.OldDigests) }
func (p *Point) NewLabel() string { return label(p.NewVersion, p.ImageRef, p.NewImageID, p.NewDigests) }

// state situe le point par rapport à l'image en service.
func (p *Point) state(current string) string {
	switch {
	case p.RolledBackAt == 0 && current == p.NewImageID:
		return "ready"
	case p.RolledBackAt != 0 && current == p.OldImageID:
		return "rolled_back"
	}
	return "stale"
}

func (p *Point) check(current string) error {
	switch p.state(current) {
	case "ready":
		return nil
	case "rolled_back":
		return ErrRolledBack
	}
	return ErrStalePoint
}

// ignores indique si l'image téléchargée est la version écartée par le retour arrière.
func (p *Point) ignores(imageID string, img *docker.ImageDetails) bool {
	if p == nil || !p.Ignored {
		return false
	}
	if imageID == p.NewImageID {
		return true
	}
	return img != nil && p.IgnoredDigest != "" && images.RepoDigest(p.ImageRef, img.RepoDigests) == p.IgnoredDigest
}

// pullRefs liste les références par digest de l'ancienne image, celles de son dépôt d'abord.
func (p *Point) pullRefs() []string {
	var first, rest []string
	want := images.RepoDigest(p.ImageRef, p.OldDigests)
	for _, d := range p.OldDigests {
		repo, h, ok := strings.Cut(d, "@")
		if !ok || repo == "" || !strings.HasPrefix(h, "sha256:") {
			continue
		}
		if want != "" && h == want {
			first = append(first, d)
		} else {
			rest = append(rest, d)
		}
	}
	return append(first, rest...)
}

// ---- Conservation ----

type points struct {
	mu   sync.Mutex
	st   *store.Store
	log  *slog.Logger
	list map[string]Point
}

func newPoints(st *store.Store, log *slog.Logger) *points {
	p := &points{st: st, log: log, list: map[string]Point{}}
	if st != nil {
		_ = json.Unmarshal([]byte(st.Get(context.Background(), pointsKey)), &p.list)
		if p.list == nil {
			p.list = map[string]Point{}
		}
	}
	return p
}

func (s *points) get(name string) *Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.list[name]
	if !ok {
		return nil
	}
	p.OldDigests = append([]string(nil), p.OldDigests...)
	p.NewDigests = append([]string(nil), p.NewDigests...)
	return &p
}

func (s *points) put(ctx context.Context, p *Point) {
	s.mu.Lock()
	s.list[p.Container] = *p
	if len(s.list) > maxPoints {
		// Les plus anciens partent (conteneurs supprimés depuis longtemps, le plus souvent).
		names := make([]string, 0, len(s.list))
		for n := range s.list {
			names = append(names, n)
		}
		sort.Slice(names, func(a, b int) bool { return s.list[names[a]].UpdatedAt < s.list[names[b]].UpdatedAt })
		for _, n := range names[:len(names)-maxPoints] {
			delete(s.list, n)
		}
	}
	s.saveLocked(ctx)
	s.mu.Unlock()
}

// restore remet le point d'avant une mise à jour qui a échoué (nil : aucun).
func (s *points) restore(ctx context.Context, name string, prev *Point) {
	if prev != nil {
		s.put(ctx, prev)
		return
	}
	s.mu.Lock()
	delete(s.list, name)
	s.saveLocked(ctx)
	s.mu.Unlock()
}

func (s *points) all() []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Point, 0, len(s.list))
	for _, p := range s.list {
		out = append(out, p)
	}
	return out
}

func (s *points) saveLocked(ctx context.Context) {
	if s.st == nil {
		return
	}
	b, _ := json.Marshal(s.list)
	if err := s.st.Set(context.WithoutCancel(ctx), pointsKey, string(b)); err != nil {
		s.log.Warn("point de retour non enregistré", "erreur", err)
	}
}

// event ajoute un événement à la chronologie du conteneur.
func (m *Manager) event(name, action, detail string) {
	if m.opt.Store != nil {
		m.opt.Store.AddEvent(context.Background(), name, action, detail, time.Now().UnixMilli())
	}
}

// ---- Consultation ----

// RollbackPoint renvoie le point de retour du conteneur (ErrNoPoint s'il n'en a pas).
func (m *Manager) RollbackPoint(ctx context.Context, c Member) (*PointView, error) {
	p := m.points.get(c.Name)
	if p == nil || c.Self || c.ID == m.opt.SelfID {
		return nil, ErrNoPoint
	}
	return m.view(ctx, c, p), nil
}

func (m *Manager) view(ctx context.Context, c Member, p *Point) *PointView {
	v := &PointView{Point: *p, State: "stale", OldLabel: p.OldLabel(), NewLabel: p.NewLabel(), Downloadable: len(p.pullRefs()) > 0}
	if v.OldDigests == nil {
		v.OldDigests = []string{}
	}
	if v.NewDigests == nil {
		v.NewDigests = []string{}
	}
	if in, err := m.dk.InspectFull(ctx, c.ref()); err == nil {
		v.State = p.state(in.Image)
	}
	_, err := m.dk.ImageDetails(ctx, p.OldImageID)
	v.ImagePresent = err == nil
	return v
}

// CanRollback indique si le conteneur peut revenir à sa version précédente (notification de surconsommation).
func (m *Manager) CanRollback(name string) bool {
	p := m.points.get(name)
	if p == nil || !m.opt.Enabled {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	in, err := m.dk.InspectFull(ctx, name)
	return err == nil && in.ID != m.opt.SelfID && p.state(in.Image) == "ready"
}

// IgnoredDigest renvoie le digest de la version écartée par un retour arrière ("" si aucune).
func (m *Manager) IgnoredDigest(name string) string {
	if p := m.points.get(name); p != nil && p.Ignored {
		return p.IgnoredDigest
	}
	return ""
}

// RollbackImages liste les images gardées pour revenir en arrière (points encore utilisables).
func (m *Manager) RollbackImages(ctx context.Context) []string {
	var out []string
	for _, p := range m.points.all() {
		if p.RolledBackAt != 0 || p.OldImageID == "" {
			continue
		}
		if in, err := m.dk.InspectFull(ctx, p.Container); err == nil && p.state(in.Image) == "ready" {
			out = append(out, p.OldImageID)
		}
	}
	return out
}

// Unignore autorise de nouveau la version écartée par le retour arrière.
func (m *Manager) Unignore(ctx context.Context, c Member) (*PointView, error) {
	p := m.points.get(c.Name)
	if p == nil || c.Self || c.ID == m.opt.SelfID {
		return nil, ErrNoPoint
	}
	if p.Ignored {
		p.Ignored, p.IgnoredDigest = false, ""
		m.points.put(ctx, p)
	}
	return m.view(ctx, c, p), nil
}

// ---- Retour arrière ----

// Rollback recrée le conteneur avec l'image qu'il avait avant sa dernière mise à jour par Vigie.
func (m *Manager) Rollback(ctx context.Context, c Member) (Job, error) {
	if c.Self || c.ID == m.opt.SelfID {
		return Job{}, ErrRollbackSelf
	}
	p := m.points.get(c.Name)
	if p == nil {
		return Job{}, ErrNoPoint
	}
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return Job{}, err
	}
	if err := p.check(in.Image); err != nil {
		return Job{}, err
	}
	return m.start("rollback", "rollback", c.Name, func(ctx context.Context, j *Job) error {
		r, err := m.rollbackOne(ctx, j, c, p)
		m.addResult(j, r)
		if err == nil {
			m.set(j, "done", 1, r.Message)
		}
		return err
	})
}

func (m *Manager) rollbackOne(ctx context.Context, j *Job, c Member, p *Point) (Result, error) {
	name := c.Name
	failed := func(err error) (Result, error) { return Result{Name: name, Message: err.Error()}, err }
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return failed(err)
	}
	// Opération lancée juste après une autre : l'état a pu changer entre-temps.
	if err := p.check(in.Image); err != nil {
		return failed(err)
	}
	ref := str(in.Config, "Image")
	if ref != p.ImageRef {
		return failed(ErrStalePoint)
	}
	oldID, err := m.oldImage(ctx, j, name, p)
	if err != nil {
		return failed(err)
	}
	m.set(j, "recreating", 0.5, fmt.Sprintf("%s : retour à la version %s…", name, p.OldLabel()))
	if err := m.dk.Tag(ctx, oldID, ref); err != nil {
		return failed(fmt.Errorf("référence %s : %w", ref, err))
	}
	if _, err := m.Replace(ctx, j, in, oldID, "", 0.5, 0.5); err != nil {
		// La version actuelle est restée en place : la référence lui revient.
		if terr := m.dk.Tag(context.WithoutCancel(ctx), in.Image, ref); terr != nil {
			m.log.Error("référence non rétablie après l'échec du retour arrière", "conteneur", name, "erreur", terr)
		}
		var re *restoredError
		if errors.As(err, &re) {
			err = re.err
		}
		return failed(fmt.Errorf("retour impossible : %w ; %s reste en version %s", err, name, p.NewLabel()))
	}
	p.RolledBackAt = time.Now().UnixMilli()
	p.Ignored = true
	p.IgnoredDigest = images.RepoDigest(p.ImageRef, p.NewDigests)
	m.points.put(ctx, p)
	m.event(name, "rollback", p.NewLabel()+" → "+p.OldLabel())
	// La version écartée ne sert plus (le réglage de suppression des anciennes images s'applique).
	if p.NewImageID != oldID {
		m.prune(ctx, p.NewImageID)
	}
	m.log.Info("retour à la version précédente", "conteneur", name, "version", p.OldLabel(), "écartée", p.NewLabel())
	return Result{Name: name, OK: true, Updated: true, Message: fmt.Sprintf("%s est revenu en version %s", name, p.OldLabel())}, nil
}

// oldImage renvoie l'ID local de l'ancienne image, re-téléchargée par son digest si elle a été supprimée.
func (m *Manager) oldImage(ctx context.Context, j *Job, name string, p *Point) (string, error) {
	m.set(j, "pulling", 0.02, name+" : recherche de l'ancienne image…")
	if img, err := m.dk.ImageDetails(ctx, p.OldImageID); err == nil {
		return img.ID, nil
	} else if !errors.Is(err, docker.ErrNotFound) {
		return "", fmt.Errorf("ancienne image : %w", err)
	}
	refs := p.pullRefs()
	if len(refs) == 0 {
		return "", errors.New("l'ancienne image a été supprimée et ne vient pas d'un registre : impossible de la récupérer")
	}
	var last error
	for _, ref := range refs {
		m.set(j, "pulling", 0.05, fmt.Sprintf("%s : l'ancienne image a été supprimée, nouveau téléchargement (%s)…", name, shortDigest(ref)))
		if err := m.dk.PullDigest(ctx, ref, m.pullProgress(j, name, 0.05, 0.6)); err != nil {
			last = explainPull(err)
			continue
		}
		img, err := m.dk.ImageDetails(ctx, ref)
		if err != nil {
			last = err
			continue
		}
		if img.ID != p.OldImageID {
			m.log.Info("ancienne image re-téléchargée sous un autre ID", "conteneur", name, "attendu", short(p.OldImageID), "obtenu", short(img.ID))
		}
		return img.ID, nil
	}
	return "", fmt.Errorf("l'ancienne image a été supprimée et n'a pas pu être re-téléchargée : %w", last)
}
