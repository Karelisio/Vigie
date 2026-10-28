// Package vulnfix corrige les failles connues trouvées dans les images des conteneurs (agent 0.7.0) :
// pour chaque image vulnérable, la meilleure voie est choisie et expliquée (nouvelle version de
// l'image analysée avant installation, correction des paquets système par Vigie, ou rien à faire),
// puis appliquée à la demande ou, pour les conteneurs choisis, la nuit après l'analyse des failles.
// Toutes les opérations passent par le verrou commun des mises à jour (une seule à la fois).
package vulnfix

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
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/updater"
	"github.com/karelisio/vigie/agent/internal/vulns"
)

const (
	candidatesKey = "vulnfix_candidates" // analyses des nouvelles versions, par image en service
	outcomesKey   = "vulnfix_outcomes"   // bilans des corrections, par image obtenue
	nightlyKey    = "vulnfix_nightly"    // dernière passe de nuit
	candidateTTL  = 3 * 24 * time.Hour
	maxOutcomes   = 100
	nightRetries  = 3
)

var (
	// ErrNotFound : image ou conteneur absent de la dernière analyse des failles.
	ErrNotFound = errors.New("image introuvable dans la dernière analyse des failles : lance une analyse")
	// ErrNothing : rien à corriger.
	ErrNothing = errors.New("rien à corriger : aucune image n'a de correction possible")
	// ErrRoute : voie de correction inconnue.
	ErrRoute = errors.New(`voie inconnue (« patch », « update » ou vide)`)
	// errNoNewVersion : le registre ne publie rien de plus récent.
	errNoNewVersion = errors.New("aucune nouvelle version publiée pour cette image")
)

// NotFixableError : la correction demandée n'est pas possible pour cette image (raison expliquée).
type NotFixableError struct{ Reason string }

func (e *NotFixableError) Error() string { return e.Reason }

// Monitor est la partie du moniteur utilisée.
type Monitor interface {
	Containers() []monitor.Container
	IsStoppedByUser(name string) bool
	NoteAction(name, action string)
	SetTransient(name string, on bool)
}

// Scanner est la partie de l'analyse des failles utilisée.
type Scanner interface {
	Report(ctx context.Context) vulns.Report
	StoredInventory(ctx context.Context, imageID string) (vulns.Inventory, bool)
	SaveInventory(ctx context.Context, imageID string, inv vulns.Inventory)
	Assess(ctx context.Context, inv vulns.Inventory) ([]vulns.Vuln, error)
	Rescan()
	ImagesUpdated()
}

// Options du service.
type Options struct {
	SelfID string
	// Network : réseau du conteneur temporaire de mise à niveau (VIGIE_VULNFIX_NETWORK, défaut bridge).
	Network string
}

// Outcome est le bilan d'une correction.
type Outcome struct {
	At        int64        `json:"at"`
	Route     string       `json:"route"` // patch, update
	From      string       `json:"from"`  // image d'avant
	Image     string       `json:"image"` // image obtenue
	Packages  int          `json:"packages"`
	Fixed     vulns.Counts `json:"fixed"`     // failles corrigées
	Added     vulns.Counts `json:"added"`     // failles apparues (nouvelle version)
	Remaining vulns.Counts `json:"remaining"` // failles restantes
	Message   string       `json:"message"`
}

// NightlyState décrit la correction automatique de nuit.
type NightlyState struct {
	Enabled bool   `json:"enabled"`
	Level   string `json:"level"`
	All     bool   `json:"all"`
	// Next : prochaine analyse de nuit, suivie de la correction (ms, 0 si désactivée).
	Next    int64  `json:"next"`
	LastRun int64  `json:"last_run"`
	Message string `json:"message,omitempty"`
}

// Plans est la réponse de GET /api/v1/vulns/fixes.
type Plans struct {
	Enabled  bool         `json:"enabled"` // mises à jour autorisées sur l'agent
	Busy     bool         `json:"busy"`    // une opération est en cours
	Scanning bool         `json:"scanning"`
	LastScan int64        `json:"last_scan"`
	Totals   PlanTotals   `json:"totals"`
	Images   []Plan       `json:"images"`
	Nightly  NightlyState `json:"nightly"`
}

// PlanTotals compte les images par voie et les failles que les corrections traiteraient.
type PlanTotals struct {
	Patch   int          `json:"patch"`
	Update  int          `json:"update"`
	None    int          `json:"none"`
	Fixable int          `json:"fixable"` // images corrigeables (patch ou update)
	Fixes   vulns.Counts `json:"fixes"`   // failles corrigées par les corrections Vigie (patch)
}

// FixRequest désigne l'image à corriger : par son ID ou par un de ses conteneurs.
type FixRequest struct {
	ImageID   string `json:"image_id"`
	Container string `json:"container"`
	Route     string `json:"route"` // patch, update ; vide : voie proposée
}

// Service planifie et applique les corrections.
type Service struct {
	ctx      context.Context
	dk       *docker.Client
	upd      *updater.Manager
	vs       Scanner
	mon      Monitor
	settings func() settings.Settings
	st       *store.Store
	log      *slog.Logger
	opt      Options
	b        *builder
	now      func() time.Time
	retry    time.Duration // nouvel essai de la passe de nuit si une opération est en cours

	mu         sync.Mutex
	candidates map[string]Candidate
	outcomes   map[string]Outcome
	nightly    NightlyState
	configs    map[string]map[string]any // configuration des images (immuable)
}

// New crée le service ; l'état (analyses, bilans) est relu en base.
func New(ctx context.Context, dk *docker.Client, upd *updater.Manager, vs Scanner, mon Monitor, set func() settings.Settings,
	st *store.Store, log *slog.Logger, opt Options) *Service {
	s := &Service{
		ctx: ctx, dk: dk, upd: upd, vs: vs, mon: mon, settings: set, st: st, log: log, opt: opt,
		now: time.Now, retry: 10 * time.Minute,
		candidates: map[string]Candidate{}, outcomes: map[string]Outcome{}, configs: map[string]map[string]any{},
	}
	s.b = &builder{
		dk: dk, log: log, network: opt.Network, poll: time.Second, timeout: 20 * time.Minute,
		transient: mon.SetTransient, note: mon.NoteAction, now: func() time.Time { return s.now() },
	}
	if st != nil {
		_ = json.Unmarshal([]byte(st.Get(ctx, candidatesKey)), &s.candidates)
		_ = json.Unmarshal([]byte(st.Get(ctx, outcomesKey)), &s.outcomes)
		_ = json.Unmarshal([]byte(st.Get(ctx, nightlyKey)), &s.nightly)
	}
	if s.candidates == nil {
		s.candidates = map[string]Candidate{}
	}
	if s.outcomes == nil {
		s.outcomes = map[string]Outcome{}
	}
	return s
}

func (s *Service) save(key string, v any) {
	if s.st == nil {
		return
	}
	b, _ := json.Marshal(v)
	if err := s.st.Set(context.WithoutCancel(s.ctx), key, string(b)); err != nil {
		s.log.Warn("correction des failles : état non enregistré", "clé", key, "erreur", err)
	}
}

// ---- Plans ----

// imageConfig renvoie la configuration d'une image (mise en cache : elle ne change jamais).
func (s *Service) imageConfig(ctx context.Context, id string) (map[string]any, bool) {
	s.mu.Lock()
	cfg, ok := s.configs[id]
	s.mu.Unlock()
	if ok {
		return cfg, true
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	img, err := s.dk.ImageDetails(ctx, id)
	if err != nil {
		return nil, false
	}
	s.mu.Lock()
	if len(s.configs) > 500 {
		s.configs = map[string]map[string]any{}
	}
	s.configs[id] = img.Config
	s.mu.Unlock()
	return img.Config, true
}

func pinned(ref string) bool {
	return ref == "" || strings.Contains(ref, "@") || strings.HasPrefix(ref, "sha256:")
}

// input rassemble ce que le plan d'une image demande.
func (s *Service) input(ctx context.Context, img vulns.Image, byID map[string]monitor.Container) planInput {
	in := planInput{img: img, enabled: s.upd.Enabled()}
	in.inv, in.hasInv = s.vs.StoredInventory(ctx, img.ID)
	allPinned := len(img.Containers) > 0
	for _, c := range img.Containers {
		mc, ok := byID[c.ID]
		if c.ID == s.opt.SelfID || (ok && mc.Self) {
			in.self = true
		}
		if !ok || !pinned(mc.ImageRef) {
			allPinned = false
		}
		if s.upd.FixDeclined(c.Name, img.ID) {
			in.declined = true
		}
	}
	in.pinned = allPinned
	if cfg, ok := s.imageConfig(ctx, img.ID); ok {
		in.noCommand = isEmpty(cfg["Entrypoint"]) && isEmpty(cfg["Cmd"])
	}
	s.mu.Lock()
	if c, ok := s.candidates[img.ID]; ok && s.now().Sub(time.UnixMilli(c.AnalyzedAt)) < candidateTTL {
		cc := c
		in.candidate = &cc
	}
	s.mu.Unlock()
	return in
}

// Plans renvoie la correction proposée pour chaque image de la dernière analyse.
func (s *Service) Plans(ctx context.Context) Plans {
	rep := s.vs.Report(ctx)
	byID := map[string]monitor.Container{}
	for _, c := range s.mon.Containers() {
		byID[c.ID] = c
	}
	out := Plans{
		Enabled: s.upd.Enabled(), Busy: s.upd.Busy(), Scanning: rep.Scanning, LastScan: rep.LastScan,
		Images: make([]Plan, 0, len(rep.Images)),
	}
	for _, img := range rep.Images {
		p := buildPlan(s.input(ctx, img, byID))
		s.mu.Lock()
		if o, ok := s.outcomes[img.ID]; ok {
			oo := o
			p.Last = &oo
		}
		s.mu.Unlock()
		switch p.Route {
		case RoutePatch:
			out.Totals.Patch++
			out.Totals.Fixes = out.Totals.Fixes.Plus(p.Patch.Fixes)
		case RouteUpdate:
			out.Totals.Update++
		default:
			out.Totals.None++
		}
		out.Images = append(out.Images, p)
	}
	out.Totals.Fixable = out.Totals.Patch + out.Totals.Update
	set := s.settings()
	s.mu.Lock()
	out.Nightly = s.nightly
	s.mu.Unlock()
	out.Nightly.Enabled, out.Nightly.Level, out.Nightly.All = set.VulnFixAuto, set.VulnFixLevel, set.VulnFixAll
	out.Nightly.Next = 0
	if set.VulnFixAuto && set.VulnScan && rep.NextScan > 0 {
		out.Nightly.Next = rep.NextScan
	}
	return out
}

// ---- Cibles ----

type target struct {
	img     vulns.Image
	plan    Plan
	members []updater.Member
}

// find retrouve l'image (par ID, préfixe d'ID ou un de ses conteneurs) dans la dernière analyse.
func (s *Service) find(ctx context.Context, imageID, container string) (target, error) {
	rep := s.vs.Report(ctx)
	byID := map[string]monitor.Container{}
	for _, c := range s.mon.Containers() {
		byID[c.ID] = c
	}
	container = strings.TrimPrefix(strings.TrimSpace(container), "/")
	imageID = strings.TrimSpace(imageID)
	if imageID == "" && container == "" {
		return target{}, ErrNotFound
	}
	for _, img := range rep.Images {
		match := false
		switch {
		case imageID != "":
			match = img.ID == imageID || img.ID == "sha256:"+imageID ||
				(len(imageID) >= 12 && strings.HasPrefix(strings.TrimPrefix(img.ID, "sha256:"), strings.TrimPrefix(imageID, "sha256:")))
		default:
			for _, c := range img.Containers {
				if c.Name == container || c.ID == container || (len(container) >= 12 && strings.HasPrefix(c.ID, container)) {
					match = true
				}
			}
		}
		if match {
			t := target{img: img, plan: buildPlan(s.input(ctx, img, byID))}
			t.members = s.members(img, byID, nil)
			return t, nil
		}
	}
	return target{}, ErrNotFound
}

// members : conteneurs de l'image à recréer (jamais l'agent), filtrés par keep si donné.
func (s *Service) members(img vulns.Image, byID map[string]monitor.Container, keep func(monitor.Container) bool) []updater.Member {
	var out []updater.Member
	for _, c := range img.Containers {
		mc, ok := byID[c.ID]
		if !ok {
			mc = monitor.Container{ID: c.ID, Name: c.Name, State: c.State}
		}
		if mc.Self || c.ID == s.opt.SelfID || vulns.Temporary(mc.Name) {
			continue
		}
		if keep != nil && !keep(mc) {
			continue
		}
		out = append(out, updater.Member{ID: mc.ID, Name: mc.Name, Running: mc.State == "running" || mc.State == "restarting"})
	}
	return out
}

func names(ms []updater.Member) string {
	n := make([]string, len(ms))
	for i, m := range ms {
		n[i] = m.Name
	}
	return strings.Join(n, ", ")
}

// check valide la voie demandée pour la cible.
func check(t target, route string) (string, error) {
	p := t.plan
	if route == "" {
		route = p.Route
	}
	if p.Self {
		return "", updater.ErrSelf
	}
	switch route {
	case RoutePatch:
		if !p.Patch.Possible {
			reason := p.Patch.Reason
			if reason == "" {
				reason = p.Explain
			}
			return "", &NotFixableError{reason}
		}
	case RouteUpdate:
		if !p.Update.Available {
			return "", &NotFixableError{"Aucune nouvelle version publiée pour cette image."}
		}
	case RouteNone:
		return "", &NotFixableError{p.Explain}
	default:
		return "", ErrRoute
	}
	if len(t.members) == 0 {
		return "", &NotFixableError{"Aucun conteneur à recréer pour cette image."}
	}
	return route, nil
}

// ---- Opérations ----

// Fix corrige une image par la voie proposée (ou celle demandée) : opération « vulnfix » (202).
func (s *Service) Fix(ctx context.Context, req FixRequest) (updater.Job, error) {
	t, err := s.find(ctx, req.ImageID, req.Container)
	if err != nil {
		return updater.Job{}, err
	}
	route, err := check(t, req.Route)
	if err != nil {
		return updater.Job{}, err
	}
	// Le conteneur désigné d'abord : il nomme l'opération (bannière, notification).
	for i, m := range t.members {
		if i > 0 && (m.Name == req.Container || m.ID == req.Container) {
			t.members[0], t.members[i] = t.members[i], t.members[0]
		}
	}
	return s.upd.Operate("vulnfix", "vulnfix", t.members[0].Name, func(ctx context.Context, op *updater.Op) error {
		o, err := s.fixImage(ctx, op, t, route, "low", req.Route != "", 0, 1)
		if err != nil {
			return err
		}
		op.Set("done", 1, o.Message)
		return nil
	})
}

// Analyze télécharge la nouvelle version de l'image sans l'installer et la compare (opération « vulnfix »,
// action « analyze »).
func (s *Service) Analyze(ctx context.Context, req FixRequest) (updater.Job, error) {
	t, err := s.find(ctx, req.ImageID, req.Container)
	if err != nil {
		return updater.Job{}, err
	}
	if t.plan.Self {
		return updater.Job{}, updater.ErrSelf
	}
	if !t.plan.Update.Available || len(t.members) == 0 {
		return updater.Job{}, &NotFixableError{"Aucune nouvelle version publiée pour cette image."}
	}
	return s.upd.Operate("vulnfix", "analyze", t.members[0].Name, func(ctx context.Context, op *updater.Op) error {
		c, err := s.analyzeCandidate(ctx, op, t, t.members[0], 0, 1)
		if errors.Is(err, errNoNewVersion) {
			op.Set("done", 1, "Aucune nouvelle version publiée : l'image en service est la plus récente")
			return nil
		}
		if err != nil {
			return err
		}
		op.Set("done", 1, updateExplain(c))
		return nil
	})
}

// item est une image à traiter dans une passe (tout corriger, nuit).
type item struct {
	t     target
	route string
}

// FixAll corrige, l'une après l'autre, toutes les images qui peuvent l'être (voie proposée).
func (s *Service) FixAll(ctx context.Context) (updater.Job, error) {
	rep := s.vs.Report(ctx)
	byID := map[string]monitor.Container{}
	for _, c := range s.mon.Containers() {
		byID[c.ID] = c
	}
	var items []item
	for _, img := range rep.Images {
		p := buildPlan(s.input(ctx, img, byID))
		if p.Route == RouteNone || p.Self {
			continue
		}
		members := s.members(img, byID, func(c monitor.Container) bool { return !c.Ignored })
		if len(members) == 0 {
			continue
		}
		items = append(items, item{t: target{img: img, plan: p, members: members}, route: p.Route})
	}
	if len(items) == 0 {
		return updater.Job{}, ErrNothing
	}
	return s.upd.Operate("vulnfix", "vulnfix", "all", func(ctx context.Context, op *updater.Op) error {
		return s.runPass(ctx, op, items, "low")
	})
}

// runPass traite les images l'une après l'autre ; une erreur si au moins une a échoué.
func (s *Service) runPass(ctx context.Context, op *updater.Op, items []item, level string) error {
	var total Outcome
	failed, changed := 0, 0
	for i, it := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n := float64(len(items))
		o, err := s.fixImage(ctx, op, it.t, it.route, level, false, float64(i)/n, 1/n)
		if err != nil {
			failed++
			continue
		}
		if o.Image != "" && o.Image != o.From {
			changed++
		}
		total.Fixed = total.Fixed.Plus(o.Fixed)
		total.Remaining = total.Remaining.Plus(o.Remaining)
	}
	parts := []string{"aucune image corrigée"}
	if changed > 0 {
		parts[0] = plural(changed, "image corrigée", "images corrigées")
	}
	if failed > 0 {
		parts = append(parts, plural(failed, "échec", "échecs"))
	}
	msg := capitalize(strings.Join(parts, ", "))
	if total.Fixed.Total() > 0 {
		msg += " · " + plural(total.Fixed.Total(), "faille corrigée", "failles corrigées")
	}
	op.Set("done", 1, msg)
	if failed > 0 {
		return errors.New(msg)
	}
	return nil
}

// fixImage applique la voie choisie à une image ; les bilans par conteneur sont ajoutés à op.
func (s *Service) fixImage(ctx context.Context, op *updater.Op, t target, route, level string, explicit bool, base, span float64) (Outcome, error) {
	var (
		o   Outcome
		err error
	)
	if route == RouteUpdate {
		o, err = s.updateImage(ctx, op, t, explicit, level, base, span)
	} else {
		o, err = s.patchImage(ctx, op, t, level, base, span)
	}
	if err != nil {
		s.log.Warn("correction des failles : échec", "image", t.img.Ref, "conteneurs", names(t.members), "erreur", err)
	} else {
		s.log.Info("correction des failles", "image", t.img.Ref, "conteneurs", names(t.members), "bilan", o.Message)
	}
	return o, err
}

// addAll note le même bilan pour chaque conteneur de la cible (sauf ceux déjà notés en échec).
func addAll(op *updater.Op, ms []updater.Member, results map[string]updater.Result, msg string, updated bool) {
	for _, m := range ms {
		if r, ok := results[m.Name]; ok && !r.OK {
			op.Add(r)
			continue
		}
		op.Add(updater.Result{Name: m.Name, OK: true, Updated: updated, Message: m.Name + " : " + msg})
	}
}

// patchImage corrige les paquets système de l'image et recrée ses conteneurs (level : gravité minimale
// des failles dont les paquets sont mis à niveau).
func (s *Service) patchImage(ctx context.Context, op *updater.Op, t target, level string, base, span float64) (Outcome, error) {
	label := t.members[0].Name
	if len(t.members) > 1 {
		label = names(t.members)
	}
	set := func(p float64, msg string) { op.Set("running", base+span*p, label+" : "+msg) }
	fail := func(err error) (Outcome, error) {
		for _, m := range t.members {
			op.Add(updater.Result{Name: m.Name, Message: err.Error()})
		}
		return Outcome{}, err
	}
	set(0, "préparation de la correction…")
	cur, err := s.dk.ImageDetails(ctx, t.img.ID)
	if err != nil {
		return fail(fmt.Errorf("image en service : %w", err))
	}
	inv, ok := s.vs.StoredInventory(ctx, t.img.ID)
	if !ok {
		return fail(errors.New("inventaire des paquets à relire : relance une analyse"))
	}
	manager := managerFor(inv.Eco)
	if manager == "" || !inv.HasTool("sh") || !inv.HasTool(toolFor(manager)) {
		return fail(errors.New("image sans shell ni gestionnaire de paquets : Vigie ne peut pas la corriger"))
	}
	sources := map[string]bool{}
	for _, f := range t.img.Fixes {
		if vulns.SystemPackages(f.Ecosystem) && f.Counts.CountAtLeast(level) > 0 {
			sources[f.Package] = true
		}
	}
	if len(sources) == 0 {
		return fail(errors.New("aucun paquet système vulnérable à mettre à niveau"))
	}
	ref := t.img.Ref
	for _, c := range s.mon.Containers() {
		if c.Name == t.members[0].Name && c.ImageRef != "" {
			ref = c.ImageRef
		}
	}

	// Image de départ : l'image d'origine si l'image en service est déjà corrigée par Vigie (les paquets
	// déjà mis à niveau le sont de nouveau), sinon l'image en service.
	prev := images.PatchedFrom(images.LabelsOf(cur.Config))
	origin := images.Patched{BaseImage: cur.ID, BaseDigests: cur.RepoDigests, Ref: ref}
	start, startImg, cumulate := cur.ID, cur, false
	var extra []string
	if prev != nil {
		origin.BaseImage, origin.BaseDigests = prev.BaseImage, prev.BaseDigests
		for _, c := range prev.Packages {
			if c.To != "" {
				extra = append(extra, c.Name)
			}
		}
		if b := s.originImage(ctx, op, prev, label, base, span); b != nil {
			start, startImg = b.ID, b
			origin.BaseImage = b.ID // re-téléchargée : son ID local fait foi
			if len(b.RepoDigests) > 0 {
				origin.BaseDigests = b.RepoDigests
			}
		} else {
			cumulate = true
			origin.Packages = prev.Packages
		}
	}
	if origin.BaseDigests == nil {
		origin.BaseDigests = []string{}
	}
	args, rejected := selectBinaries(manager, inv.Binaries(), sources, extra)
	if len(rejected) > 0 {
		s.log.Warn("correction : noms de paquets invalides écartés", "image", t.img.Ref, "paquets", strings.Join(rejected, " "))
	}
	if len(args) == 0 {
		return fail(errors.New("paquets vulnérables introuvables dans la base de paquets de l'image"))
	}
	spec := buildSpec{Base: start, BaseCfg: startImg.Config, Manager: manager, Args: args, Patched: origin, Cumulate: cumulate}
	if start != cur.ID {
		spec.Current = inv.Binaries()
	}
	res, err := s.b.build(ctx, spec, func(p float64, msg string) { set(p*0.7, msg) })
	if err != nil {
		return fail(err)
	}
	if res.Image == "" {
		msg := "rien n'a changé : " + res.Note
		addAll(op, t.members, nil, msg, false)
		set(1, msg)
		o := Outcome{At: s.now().UnixMilli(), Route: RoutePatch, From: t.img.ID, Image: t.img.ID, Message: capitalize(msg)}
		s.putOutcome(o)
		return o, nil
	}
	s.vs.SaveInventory(ctx, res.Image, res.After)

	// Recréation des conteneurs : point de retour vers l'image d'origine, non corrigée.
	originImg := startImg
	if cumulate || start != origin.BaseImage {
		originImg = &docker.ImageDetails{ID: origin.BaseImage, RepoDigests: origin.BaseDigests}
	}
	results := map[string]updater.Result{}
	applied := 0
	var firstErr error
	n := float64(len(t.members))
	for i, m := range t.members {
		r, err := s.upd.ApplyPatch(ctx, op, m, updater.Patch{
			Image: res.Image, Base: origin.BaseImage, BaseImg: originImg, Detail: describeChanges(res.Changes, 4),
		}, base+span*(0.72+0.22*float64(i)/n), span*0.22/n)
		results[m.Name] = r
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		applied++
	}
	if applied == 0 {
		if rerr := s.dk.RemoveImage(context.WithoutCancel(ctx), res.Image); rerr != nil {
			s.log.Warn("correction : image corrigée inutilisée non supprimée", "image", res.Image, "erreur", rerr)
		}
		for _, m := range t.members {
			op.Add(results[m.Name])
		}
		return Outcome{}, firstErr
	}

	set(0.96, "nouvelle analyse de l'image corrigée…")
	o := s.compare(ctx, inv, res.After).Outcome
	o.Route, o.From, o.Image, o.Packages = RoutePatch, t.img.ID, res.Image, len(res.Changes)
	o.Message = fmt.Sprintf("%s mis à niveau — %s", plural(len(res.Changes), "paquet", "paquets"), o.Message)
	addAll(op, t.members, results, o.Message, true)
	s.putOutcome(o)
	s.vs.Rescan()
	set(1, o.Message)
	if firstErr != nil {
		return o, firstErr
	}
	return o, nil
}

// originImage renvoie l'image d'origine d'une image corrigée (présente, ou re-téléchargée par son
// digest) ; nil si elle est introuvable.
func (s *Service) originImage(ctx context.Context, op *updater.Op, prev *images.Patched, label string, base, span float64) *docker.ImageDetails {
	if img, err := s.dk.ImageDetails(ctx, prev.BaseImage); err == nil {
		return img
	}
	for _, d := range prev.BaseDigests {
		op.Set("pulling", base+span*0.02, label+" : nouveau téléchargement de l'image d'origine…")
		if err := s.dk.PullDigest(ctx, d, nil); err != nil {
			s.log.Info("correction : image d'origine non re-téléchargée", "digest", d, "erreur", err)
			continue
		}
		if img, err := s.dk.ImageDetails(ctx, d); err == nil {
			return img
		}
	}
	return nil
}

// updateImage analyse puis installe la nouvelle version de l'image (mise à jour ordinaire, avec point
// de retour), pour chacun de ses conteneurs. Si l'analyse montre qu'elle ne corrige aucune faille, elle
// n'est pas installée (sauf voie demandée explicitement) : correction des paquets à la place quand
// elle est possible.
func (s *Service) updateImage(ctx context.Context, op *updater.Op, t target, explicit bool, level string, base, span float64) (Outcome, error) {
	c, err := s.analyzeCandidate(ctx, op, t, t.members[0], base, span*0.4)
	switch {
	case errors.Is(err, errNoNewVersion):
		msg := "aucune nouvelle version publiée"
		if t.plan.Patch.Possible && !explicit {
			return s.patchImage(ctx, op, t, level, base+span*0.4, span*0.6)
		}
		addAll(op, t.members, nil, msg, false)
		return Outcome{Route: RouteUpdate, From: t.img.ID, Image: t.img.ID, Message: capitalize(msg)}, nil
	case err != nil && c == nil && !errors.Is(err, errAssess):
		for _, m := range t.members {
			op.Add(updater.Result{Name: m.Name, Message: err.Error()})
		}
		return Outcome{}, err
	case c != nil && c.Fixed.Total() == 0 && !explicit:
		if t.plan.Patch.Possible {
			op.Set("running", base+span*0.4, t.members[0].Name+" : la nouvelle version ne corrige rien, correction des paquets à la place…")
			return s.patchImage(ctx, op, t, level, base+span*0.4, span*0.6)
		}
		msg := "la nouvelle version ne corrige aucune de ces failles : elle n'est pas installée"
		addAll(op, t.members, nil, msg, false)
		return Outcome{Route: RouteUpdate, From: t.img.ID, Image: t.img.ID, Message: capitalize(msg)}, nil
	}
	var firstErr error
	updated := 0
	n := float64(len(t.members))
	results := map[string]updater.Result{}
	for i, m := range t.members {
		r, err := s.upd.Update(ctx, op, m, base+span*(0.4+0.55*float64(i)/n), span*0.55/n)
		results[m.Name] = r
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if r.Updated {
			updated++
		}
	}
	o := Outcome{At: s.now().UnixMilli(), Route: RouteUpdate, From: t.img.ID}
	if c != nil {
		o.Image, o.Fixed, o.Added, o.Remaining = c.ImageID, c.Fixed, c.Added, c.Remaining
		o.Message = updateOutcomeText(c)
	} else {
		o.Message = "nouvelle version installée (analyse impossible : " + err.Error() + ")"
	}
	if updated == 0 && firstErr == nil {
		o.Message = "déjà à jour"
	}
	for _, m := range t.members {
		r := results[m.Name]
		if r.OK && r.Updated {
			r.Message = m.Name + " : " + o.Message
		}
		op.Add(r)
	}
	if updated > 0 && o.Image != "" {
		s.putOutcome(o)
		// Analyse différée, comme après une mise à jour : l'état « nouvelle version publiée » des
		// conteneurs est d'abord revu (sinon l'alerte de failles l'annoncerait encore).
		s.vs.ImagesUpdated()
	}
	op.Set("running", base+span, capitalize(o.Message))
	o.Message = capitalize(o.Message)
	return o, firstErr
}

// updateOutcomeText : « version 1.5.0 installée : 3 failles corrigées (dont 1 critique), 1 nouvelle, 2 restantes ».
func updateOutcomeText(c *Candidate) string {
	s := "nouvelle version installée : "
	if c.Version != "" {
		s = "version " + c.Version + " installée : "
	}
	if c.Fixed.Total() == 0 {
		s += "aucune faille corrigée"
	} else {
		s += plural(c.Fixed.Total(), "faille corrigée", "failles corrigées")
		if c.Fixed.Critical > 0 {
			s += " (dont " + plural(c.Fixed.Critical, "critique", "critiques") + ")"
		}
	}
	if c.Added.Total() > 0 {
		s += ", " + plural(c.Added.Total(), "nouvelle", "nouvelles")
	}
	if c.Remaining.Total() == 0 {
		return s + ", aucune restante"
	}
	return s + ", " + plural(c.Remaining.Total(), "restante", "restantes")
}

// errAssess : la nouvelle version a été téléchargée mais pas analysée (OSV injoignable…).
var errAssess = errors.New("analyse des failles impossible")

// analyzeCandidate télécharge la nouvelle version (sans l'installer), lit ses paquets dans un conteneur
// créé mais jamais démarré, puis la compare à l'image en service.
func (s *Service) analyzeCandidate(ctx context.Context, op *updater.Op, t target, m updater.Member, base, span float64) (*Candidate, error) {
	pc, err := s.upd.PullCandidate(ctx, op, m, base, span*0.7)
	if err != nil {
		return nil, err
	}
	if pc.Image == pc.Current {
		return nil, errNoNewVersion
	}
	op.Set("running", base+span*0.75, m.Name+" : lecture des paquets de la nouvelle version…")
	inv, err := s.readImage(ctx, pc.Image, pc.Details)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", errAssess, err)
	}
	s.vs.SaveInventory(ctx, pc.Image, inv)
	op.Set("running", base+span*0.85, m.Name+" : comparaison des failles…")
	cur, ok := s.vs.StoredInventory(ctx, t.img.ID)
	if !ok {
		return nil, fmt.Errorf("%w : inventaire de l'image en service à relire", errAssess)
	}
	o := s.compare(ctx, cur, inv)
	if o.err != nil {
		return nil, fmt.Errorf("%w : %v", errAssess, o.err)
	}
	c := Candidate{
		Current: t.img.ID, ImageID: pc.Image, AnalyzedAt: s.now().UnixMilli(),
		Fixed: o.Fixed, Added: o.Added, Remaining: o.Remaining,
	}
	if pc.Details != nil {
		c.Version = pc.Details.Label("org.opencontainers.image.version")
	}
	s.mu.Lock()
	for k, v := range s.candidates {
		if s.now().Sub(time.UnixMilli(v.AnalyzedAt)) > candidateTTL {
			delete(s.candidates, k)
		}
	}
	s.candidates[c.Current] = c
	snapshot := make(map[string]Candidate, len(s.candidates))
	for k, v := range s.candidates {
		snapshot[k] = v
	}
	s.mu.Unlock()
	s.save(candidatesKey, snapshot)
	return &c, nil
}

// readImage lit l'inventaire d'une image dans un conteneur créé pour l'occasion, jamais démarré.
func (s *Service) readImage(ctx context.Context, id string, det *docker.ImageDetails) (vulns.Inventory, error) {
	var cfg map[string]any
	if det != nil {
		cfg = det.Config
	}
	spec := map[string]any{
		"Image":  id,
		"Labels": map[string]string{"vigie.ignore": "true", "vigie.helper": "true"},
		"HostConfig": map[string]any{
			"NetworkMode": "none", "RestartPolicy": map[string]any{"Name": "no"},
		},
	}
	if isEmpty(cfg["Entrypoint"]) && isEmpty(cfg["Cmd"]) {
		spec["Cmd"] = []string{"/vigie-inventaire"} // jamais exécuté : Docker exige une commande
	}
	name := fmt.Sprintf("%s%d", vulns.TempAnalyzePrefix, s.now().UnixNano())
	s.mon.SetTransient(name, true)
	defer s.mon.SetTransient(name, false)
	cid, err := s.dk.Create(ctx, name, spec)
	if err != nil {
		return vulns.Inventory{}, fmt.Errorf("conteneur temporaire : %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.dk.RemoveWithVolumes(rctx, cid); err != nil && !errors.Is(err, docker.ErrNotFound) {
			s.log.Warn("analyse : conteneur temporaire non supprimé", "conteneur", name, "erreur", err)
		}
	}()
	dirs := []string{"/app"}
	if wd, _ := cfg["WorkingDir"].(string); wd != "" {
		dirs = []string{wd, "/app"}
	}
	inv := vulns.ReadInventory(ctx, s.dk, cid, dirs, s.now())
	if inv.Error != "" {
		return inv, errors.New(inv.Error)
	}
	return inv, nil
}

// comparison : failles d'avant et d'après.
type comparison struct {
	Outcome
	err error
}

// compare recherche les failles des deux inventaires et les compare (par faille et paquet).
func (s *Service) compare(ctx context.Context, before, after vulns.Inventory) comparison {
	var c comparison
	c.At = s.now().UnixMilli()
	old, err := s.vs.Assess(ctx, before)
	if err == nil {
		var now []vulns.Vuln
		if now, err = s.vs.Assess(ctx, after); err == nil {
			key := func(v vulns.Vuln) string { return v.ID + "|" + v.Package }
			was, is := map[string]bool{}, map[string]bool{}
			for _, v := range old {
				was[key(v)] = true
			}
			var fixed, added []vulns.Vuln
			for _, v := range now {
				is[key(v)] = true
				if !was[key(v)] {
					added = append(added, v)
				}
			}
			for _, v := range old {
				if !is[key(v)] {
					fixed = append(fixed, v)
				}
			}
			c.Fixed, c.Added, c.Remaining = vulns.CountVulns(fixed), vulns.CountVulns(added), vulns.CountVulns(now)
			c.Message = outcomeText(c.Fixed, c.Remaining, now)
			return c
		}
	}
	c.err = err
	c.Message = "nouvelle analyse impossible (" + err.Error() + ")"
	return c
}

// outcomeText : « 7 failles corrigées, 3 restantes (sans correctif disponible) ».
func outcomeText(fixed, remaining vulns.Counts, now []vulns.Vuln) string {
	s := plural(fixed.Total(), "faille corrigée", "failles corrigées")
	if fixed.Critical > 0 {
		s += " (dont " + plural(fixed.Critical, "critique", "critiques") + ")"
	}
	if remaining.Total() == 0 {
		return s + ", aucune restante"
	}
	sysFix, appFix := 0, 0
	for _, v := range now {
		if v.Fixed == "" {
			continue
		}
		if vulns.SystemPackages(v.Ecosystem) {
			sysFix++
		} else {
			appFix++
		}
	}
	unfixed := remaining.Total() - sysFix - appFix
	kinds := []struct {
		n          int
		only, some string // seule catégorie restante, ou parmi d'autres
	}{
		{unfixed, "sans correctif disponible", "%d sans correctif"},
		{sysFix, "correctif pas encore dans les dépôts", "%d dont le correctif n'est pas encore dans les dépôts"},
		{appFix, "à corriger par une nouvelle version de l'image", "%d à corriger par une nouvelle version de l'image"},
	}
	var detail []string
	for _, k := range kinds {
		switch {
		case k.n == remaining.Total():
			detail = append(detail, k.only)
		case k.n > 0:
			detail = append(detail, fmt.Sprintf(k.some, k.n))
		}
	}
	return fmt.Sprintf("%s, %s (%s)", s, plural(remaining.Total(), "restante", "restantes"), strings.Join(detail, ", "))
}

func (s *Service) putOutcome(o Outcome) {
	if o.At == 0 {
		o.At = s.now().UnixMilli()
	}
	s.mu.Lock()
	s.outcomes[o.Image] = o
	if len(s.outcomes) > maxOutcomes {
		keys := make([]string, 0, len(s.outcomes))
		for k := range s.outcomes {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return s.outcomes[keys[i]].At < s.outcomes[keys[j]].At })
		for _, k := range keys[:len(keys)-maxOutcomes] {
			delete(s.outcomes, k)
		}
	}
	snapshot := make(map[string]Outcome, len(s.outcomes))
	for k, v := range s.outcomes {
		snapshot[k] = v
	}
	s.mu.Unlock()
	s.save(outcomesKey, snapshot)
}

// ---- Nuit ----

// Nightly lance la correction automatique, après l'analyse de la nuit (appelée par l'analyse des
// failles). Une seule passe séquentielle ; si une opération est en cours, nouvel essai 10 min plus tard
// (3 fois au plus).
func (s *Service) Nightly() { s.nightlyAttempt(0) }

func (s *Service) nightlyAttempt(try int) {
	set := s.settings()
	if !set.VulnFixAuto || !s.upd.Enabled() || s.ctx.Err() != nil {
		return
	}
	now := s.now()
	if set.InMaintenance(now) {
		s.log.Info("correction de nuit : serveur en maintenance, rien n'est corrigé")
		s.setNightly(now, "Mode maintenance : aucune correction cette nuit")
		return
	}
	items := s.nightlyItems(s.ctx, set)
	if len(items) == 0 {
		s.log.Info("correction de nuit : rien à corriger")
		s.setNightly(now, "Rien à corriger")
		return
	}
	_, err := s.upd.Operate("vulnfix", "vulnfix", "nightly", func(ctx context.Context, op *updater.Op) error {
		err := s.runPass(ctx, op, items, set.VulnFixLevel)
		fixed, failed := 0, 0
		for _, r := range op.Results() {
			if !r.OK {
				failed++
			} else if r.Updated {
				fixed++
			}
		}
		msg := plural(fixed, "conteneur corrigé", "conteneurs corrigés")
		if failed > 0 {
			msg += ", " + plural(failed, "échec", "échecs")
		}
		s.setNightly(s.now(), capitalize(msg))
		return err
	})
	switch {
	case errors.Is(err, updater.ErrBusy) && try < nightRetries:
		s.log.Info("correction de nuit reportée : une opération est en cours", "essai", try+1)
		time.AfterFunc(s.retry, func() { s.nightlyAttempt(try + 1) })
	case err != nil:
		s.log.Warn("correction de nuit abandonnée", "erreur", err)
		s.setNightly(now, "Abandonnée : "+err.Error())
		if s.st != nil {
			s.st.Audit(s.ctx, "agent", "vulnfix-auto", "nuit", "refusé : "+err.Error(), "vigie")
		}
	default:
		if s.st != nil {
			s.st.Audit(s.ctx, "agent", "vulnfix-auto", "nuit", "lancé", "vigie")
		}
	}
}

func (s *Service) setNightly(at time.Time, msg string) {
	s.mu.Lock()
	s.nightly.LastRun, s.nightly.Message = at.UnixMilli(), msg
	st := s.nightly
	s.mu.Unlock()
	s.save(nightlyKey, st)
}

// nightlyItems choisit les images à corriger cette nuit : correction des paquets possible, au moins
// une faille du niveau choisi, et des conteneurs dans la portée (tous ou la liste), hors agent,
// conteneurs ignorés, arrêtés par l'utilisateur et corrections écartées par un retour arrière.
func (s *Service) nightlyItems(ctx context.Context, set settings.Settings) []item {
	rep := s.vs.Report(ctx)
	byID := map[string]monitor.Container{}
	for _, c := range s.mon.Containers() {
		byID[c.ID] = c
	}
	chosen := map[string]bool{}
	for _, n := range set.VulnFixList {
		chosen[n] = true
	}
	var items []item
	for _, img := range rep.Images {
		p := buildPlan(s.input(ctx, img, byID))
		if p.Self || !p.Patch.Possible {
			continue
		}
		hit := false
		for _, pk := range p.Patch.Packages {
			if pk.Counts.CountAtLeast(set.VulnFixLevel) > 0 {
				hit = true
			}
		}
		if !hit {
			continue
		}
		members := s.members(img, byID, func(c monitor.Container) bool {
			return (set.VulnFixAll || chosen[c.Name]) && !c.Ignored && !c.StoppedByUser && !s.mon.IsStoppedByUser(c.Name) &&
				!s.upd.FixDeclined(c.Name, img.ID)
		})
		if len(members) == 0 {
			continue
		}
		items = append(items, item{t: target{img: img, plan: p, members: members}, route: RoutePatch})
	}
	return items
}
