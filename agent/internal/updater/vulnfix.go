package updater

// Primitives utilisées par la correction des failles (paquet vulnfix, agent 0.7.0) : opération sous
// le verrou commun, mise à jour explicite, téléchargement seul d'une nouvelle version pour l'analyser,
// et recréation d'un conteneur avec une image corrigée par Vigie.
//
// L'image corrigée n'a pas de digest de registre. Comme pour le retour arrière, la référence du
// conteneur (ex. ghcr.io/x/y:latest) est reposée localement sur elle avant la recréation, pour que
// Config.Image garde la référence d'origine (Dockhand, compose, détection des mises à jour) ; elle
// revient à l'image en service si la recréation échoue. Le point de retour (Kind « vulnfix ») mène à
// l'image d'origine, non corrigée.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
)

// PointVulnfix : point de retour noté avant la recréation sur une image corrigée par Vigie.
const PointVulnfix = "vulnfix"

// ErrSelf : l'agent ne se corrige pas lui-même (il se met à jour comme une image ordinaire).
var ErrSelf = errors.New("l'agent Vigie ne corrige pas sa propre image : mets-le à jour depuis Réglages")

// Op est une opération en cours, pilotée par un autre paquet.
type Op struct {
	m *Manager
	j *Job
}

// Set change la phase, l'avancement (0 à 1, négatif : inchangé) et le message de l'opération.
func (o *Op) Set(phase string, progress float64, msg string) { o.m.set(o.j, phase, progress, msg) }

// Add ajoute le bilan d'un conteneur.
func (o *Op) Add(r Result) { o.m.addResult(o.j, r) }

// Results renvoie les bilans déjà notés.
func (o *Op) Results() []Result {
	o.m.mu.Lock()
	defer o.m.mu.Unlock()
	return append([]Result{}, o.j.Results...)
}

// Operate lance une opération sous le verrou commun (une seule à la fois, ErrBusy sinon) : progression
// diffusée en direct, journal, notification de fin. fn renvoie une erreur pour un échec ; le message
// final est celui de la dernière phase.
func (m *Manager) Operate(kind, action, target string, fn func(ctx context.Context, op *Op) error) (Job, error) {
	return m.start(kind, action, target, func(ctx context.Context, j *Job) error {
		return fn(ctx, &Op{m: m, j: j})
	})
}

// Busy indique si une opération est en cours.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current != nil
}

// Update met à jour le conteneur comme le bouton de sa fiche (version ignorée comprise), dans op.
func (m *Manager) Update(ctx context.Context, op *Op, c Member, base, span float64) (Result, error) {
	if c.Self || c.ID == m.opt.SelfID {
		return Result{Name: c.Name, Message: ErrSelf.Error()}, ErrSelf
	}
	return m.updateOne(ctx, op.j, c, base, span, true)
}

// Candidate est une nouvelle version téléchargée sans être installée.
type Candidate struct {
	Ref     string // référence configurée
	Current string // image en service
	Image   string // image téléchargée
	Details *docker.ImageDetails
}

// PullCandidate télécharge la nouvelle version de l'image du conteneur sans l'installer : la référence
// revient aussitôt à l'image en service (rien ne change pour Dockhand ni compose). Image vaut Current
// si rien de plus récent n'est publié ; une image corrigée par Vigie dont l'origine est toujours
// publiée est aussi « à jour ».
func (m *Manager) PullCandidate(ctx context.Context, op *Op, c Member, base, span float64) (*Candidate, error) {
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return nil, err
	}
	ref, err := imageRef(in)
	if err != nil {
		return nil, err
	}
	oldImg, _ := m.dk.ImageDetails(ctx, in.Image)
	// Image que la référence désigne localement avant le téléchargement (elle peut différer de celle
	// du conteneur quand plusieurs conteneurs partagent la référence).
	tagged := in.Image
	if img, err := m.dk.ImageDetails(ctx, ref); err == nil {
		tagged = img.ID
	}
	newID, err := m.pull(ctx, op.j, in, base, span)
	if err != nil {
		return nil, err
	}
	cand := &Candidate{Ref: ref, Current: in.Image, Image: newID}
	if newID != tagged {
		// La référence revient où elle était : seule une mise à jour appliquera la nouvelle version.
		if terr := m.dk.Tag(context.WithoutCancel(ctx), tagged, ref); terr != nil {
			m.log.Warn("référence non rétablie après l'analyse de la nouvelle version", "conteneur", c.Name, "erreur", terr)
		}
	}
	cand.Details, _ = m.dk.ImageDetails(ctx, newID)
	if oldImg != nil && newID != in.Image {
		var digests []string
		if cand.Details != nil {
			digests = cand.Details.RepoDigests
		}
		if images.PatchedFrom(images.LabelsOf(oldImg.Config)).IsBase(ref, newID, digests) {
			cand.Image = in.Image
		}
	}
	return cand, nil
}

// Patch décrit une image corrigée par Vigie, à appliquer à des conteneurs.
type Patch struct {
	Image   string               // image corrigée (sha256:…)
	Base    string               // image d'origine, non corrigée : cible du retour arrière
	BaseImg *docker.ImageDetails // ses détails (digests, version), nil si inconnus
	Detail  string               // chronologie : paquets mis à niveau
}

// ApplyPatch recrée le conteneur avec l'image corrigée, en gardant sa référence dans Config.Image, et
// note un point de retour vers l'image d'origine. Si le nouveau conteneur ne démarre pas, l'ancien est
// remis en place avec sa référence. Le message de bilan est laissé à l'appelant.
func (m *Manager) ApplyPatch(ctx context.Context, op *Op, c Member, p Patch, base, span float64) (Result, error) {
	failed := func(err error) (Result, error) { return Result{Name: c.Name, Message: err.Error()}, err }
	if c.Self || c.ID == m.opt.SelfID {
		return failed(ErrSelf)
	}
	in, err := m.dk.InspectFull(ctx, c.ref())
	if err != nil {
		return failed(err)
	}
	if in.ID == m.opt.SelfID {
		return failed(ErrSelf)
	}
	ref, err := imageRef(in)
	if err != nil {
		return failed(err)
	}
	if in.Image == p.Image {
		return Result{Name: c.Name, OK: true, Message: c.Name + " utilise déjà l'image corrigée"}, nil
	}
	if p.BaseImg == nil {
		p.BaseImg, _ = m.dk.ImageDetails(ctx, p.Base)
	}
	prev := m.points.get(c.Name)
	point := &Point{
		Kind: PointVulnfix, Container: c.Name, ImageRef: ref, OldImageID: p.Base, NewImageID: p.Image,
		NewDigests: []string{}, UpdatedAt: time.Now().UnixMilli(),
	}
	if p.BaseImg != nil {
		np := newPoint(c.Name, ref, p.Base, p.BaseImg, p.Image, nil)
		point.OldDigests, point.OldVersion, point.OldCreated = np.OldDigests, np.OldVersion, np.OldCreated
	}
	m.points.put(ctx, point)
	m.set(op.j, "recreating", base, fmt.Sprintf("%s : passage à l'image corrigée…", c.Name))
	if err := m.dk.Tag(ctx, p.Image, ref); err != nil {
		m.points.restore(ctx, c.Name, prev)
		return failed(fmt.Errorf("référence %s : %w", ref, err))
	}
	if _, err := m.Replace(ctx, op.j, in, p.Image, "", base, span); err != nil {
		if terr := m.dk.Tag(context.WithoutCancel(ctx), in.Image, ref); terr != nil {
			m.log.Error("référence non rétablie après l'échec de la correction", "conteneur", c.Name, "erreur", terr)
		}
		m.points.restore(ctx, c.Name, prev)
		var re *restoredError
		if errors.As(err, &re) {
			err = fmt.Errorf("%w ; %s reste sur son image actuelle", re.err, c.Name)
		}
		return failed(err)
	}
	m.event(c.Name, "vulnfix", p.Detail)
	// L'image d'origine reste pour pouvoir y revenir ; une image corrigée remplacée ne sert plus.
	if prev != nil {
		for _, id := range []string{prev.OldImageID, prev.NewImageID} {
			if id != "" && id != p.Base && id != p.Image {
				m.prune(ctx, id)
			}
		}
	}
	if in.Image != p.Base && in.Image != p.Image {
		m.prune(ctx, in.Image)
	}
	return Result{Name: c.Name, OK: true, Updated: true}, nil
}

// FixDeclined indique si la correction du conteneur a été écartée par un retour à l'image d'origine
// (toujours en service) : la correction automatique de nuit ne la refait pas.
func (m *Manager) FixDeclined(name, currentImage string) bool {
	p := m.points.get(name)
	return p != nil && p.Kind == PointVulnfix && p.Ignored && p.RolledBackAt != 0 && p.OldImageID == currentImage
}

// Prune supprime une image devenue inutile si le réglage de suppression des anciennes images est actif.
func (m *Manager) Prune(ctx context.Context, imageID string) { m.prune(ctx, imageID) }

// SetTimings règle les délais de vérification d'un conteneur recréé (essais d'autres paquets).
func (m *Manager) SetTimings(settle, crashGrace, poll time.Duration) {
	m.settle, m.crashGrace, m.poll = settle, crashGrace, poll
}

// vulnfixCounts compte les conteneurs corrigés (image changée), inchangés et en échec.
func (j Job) vulnfixCounts() (fixed, unchanged, failed int) {
	for _, r := range j.Results {
		switch {
		case !r.OK:
			failed++
		case r.Updated:
			fixed++
		default:
			unchanged++
		}
	}
	return
}

// vulnfixTitle : titre de la notification de fin d'une correction des failles.
func (j Job) vulnfixTitle() string {
	if j.Action == "analyze" {
		if j.OK {
			return "Nouvelle version de " + j.Target + " analysée"
		}
		return "Échec de l'analyse de la nouvelle version de " + j.Target
	}
	fixed, unchanged, failed := j.vulnfixCounts()
	var parts []string
	if fixed > 0 {
		parts = append(parts, plural(fixed, "corrigé", "corrigés"))
	}
	if unchanged > 0 {
		parts = append(parts, plural(unchanged, "inchangé", "inchangés"))
	}
	if failed > 0 {
		parts = append(parts, plural(failed, "échec", "échecs"))
	}
	summary := strings.Join(parts, ", ")
	switch j.Target {
	case "nightly":
		if summary == "" {
			return "Correctifs de la nuit : échec"
		}
		return "Correctifs de la nuit : " + summary
	case "all":
		if summary == "" {
			if j.OK {
				return "Correction des failles : rien à corriger"
			}
			return "Correction des failles : échec"
		}
		return "Correction des failles : " + summary
	}
	switch {
	case !j.OK:
		return "Échec de la correction de " + j.Target
	case fixed > 0:
		return j.Target + " : failles corrigées"
	}
	return j.Target + " : rien n'a changé"
}

// vulnfixBody : conteneurs corrigés et en échec (passe de nuit, tout corriger).
func (j Job) vulnfixBody() string {
	var fixed, failed []string
	for _, r := range j.Results {
		if !r.OK {
			failed = append(failed, r.Name)
		} else if r.Updated {
			fixed = append(fixed, r.Name)
		}
	}
	var parts []string
	if len(fixed) > 0 {
		parts = append(parts, "Corrigés : "+strings.Join(fixed, ", ")+".")
	}
	if len(failed) > 0 {
		parts = append(parts, "Échec : "+strings.Join(failed, ", ")+".")
	}
	if len(parts) == 0 {
		return j.Message
	}
	return strings.Join(parts, " ") + " " + j.Message
}
