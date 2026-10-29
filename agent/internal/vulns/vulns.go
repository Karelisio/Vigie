// Package vulns recherche les failles connues des paquets installés dans les images des conteneurs,
// à partir de la base publique OSV (osv.dev). Seuls des noms de paquets, leurs versions et leur
// écosystème sont envoyés ; l'inventaire est lu une seule fois par image (cache par ID d'image).
package vulns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/images"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	defaultOSV       = "https://api.osv.dev"
	reportKey        = "vulns_report"  // dernier résultat (JSON)
	nightlyKey       = "vulns_nightly" // jour (AAAA-MM-JJ) de la dernière analyse de nuit
	dayLayout        = "2006-01-02"
	startupDelay     = 2 * time.Minute // première analyse après l'installation
	nightlyOffset    = time.Hour       // après l'heure des mises à jour automatiques
	staleAfter       = 36 * time.Hour  // analyse de rattrapage (agent arrêté la nuit…)
	retryAfter       = time.Hour       // après un échec, pas de nouvel essai automatique avant
	scanTimeout      = 30 * time.Minute
	recordTTL        = 3 * 24 * time.Hour  // fiche OSV relue au-delà (ou si elle a été modifiée)
	recordKeep       = 30 * 24 * time.Hour // fiches inutilisées supprimées au-delà
	detailWorkers    = 4
	maxDetailFetches = 5000
	maxListed        = 200 // failles listées par image (les plus graves d'abord)
)

// updateDelay : analyse des nouvelles images après une mise à jour (réduit dans les tests).
var updateDelay = 90 * time.Second

var (
	// ErrDisabled : réglage « vuln_scan » désactivé, aucune requête vers OSV.
	ErrDisabled = errors.New("analyse des failles désactivée dans les réglages")
	// ErrBusy : une analyse est déjà en cours.
	ErrBusy = errors.New("une analyse est déjà en cours")
)

// Docker est la partie du client Docker utilisée par l'analyse.
type Docker interface {
	List(ctx context.Context) ([]docker.Container, error)
	Inspect(ctx context.Context, id string) (*docker.Inspect, error)
	ImageDetails(ctx context.Context, ref string) (*docker.ImageDetails, error)
	Archiver
}

// Options de l'analyse.
type Options struct {
	Version string
	// OSVURL remplace l'adresse de l'API OSV (tests) ; défaut : VIGIE_OSV_URL, sinon https://api.osv.dev.
	OSVURL string
	// UpdateAvailable indique si une nouvelle image est publiée pour ce conteneur.
	UpdateAvailable func(container string) bool
}

// Counts compte les failles par gravité.
type Counts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
	Fixable  int `json:"fixable"` // avec une version corrigée
	// Failles jugées sans importance par la distribution : comptées à part, hors du total.
	Negligible int `json:"negligible"`
}

func (c *Counts) add(v Vuln) {
	if v.Negligible {
		c.Negligible++
		return
	}
	switch v.Severity {
	case "critical":
		c.Critical++
	case "high":
		c.High++
	case "medium":
		c.Medium++
	case "low":
		c.Low++
	default:
		c.Unknown++
	}
	if v.Fixed != "" {
		c.Fixable++
	}
}

// Total renvoie le nombre de failles (hors failles sans importance).
func (c Counts) Total() int { return c.Critical + c.High + c.Medium + c.Low + c.Unknown }

// Totals : failles distinctes de toutes les images (une faille commune à deux images compte une fois).
type Totals struct {
	Counts
	Images     int `json:"images"`     // images analysées
	Vulnerable int `json:"vulnerable"` // images avec au moins une faille
}

// ContainerRef est un conteneur qui utilise l'image.
type ContainerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// Vuln est une faille d'un paquet de l'image.
type Vuln struct {
	ID        string   `json:"id"`
	Aliases   []string `json:"aliases"` // identifiants CVE
	Summary   string   `json:"summary"`
	Severity  string   `json:"severity"` // critical, high, medium, low, unknown
	Score     float64  `json:"score"`    // CVSS (0 si inconnu)
	Package   string   `json:"package"`
	Version   string   `json:"version"` // installée
	Fixed     string   `json:"fixed"`   // « » : pas de version corrigée
	Ecosystem string   `json:"ecosystem"`
	URL       string   `json:"url"`
	// Negligible : la distribution juge la faille sans importance (urgence Debian « unimportant »,
	// priorité Ubuntu « negligible ») ; Note l'explique. Ni comptée, ni alertée, ni corrigée.
	Negligible bool   `json:"negligible,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Image est le résultat de l'analyse d'une image.
type Image struct {
	ID              string         `json:"id"`
	Ref             string         `json:"ref"`
	Containers      []ContainerRef `json:"containers"`
	OS              string         `json:"os"`
	Ecosystem       string         `json:"ecosystem"` // écosystème OSV des paquets du système, « » s'il n'est pas pris en charge
	Packages        int            `json:"packages"`
	Counts          Counts         `json:"counts"`
	UpdateAvailable bool           `json:"update_available"`
	Error           string         `json:"error,omitempty"`
	ReadAt          int64          `json:"read_at"` // lecture de l'inventaire (ms)
	Vulns           []Vuln         `json:"vulns"`
	More            int            `json:"more"` // failles non listées (au-delà de 200)
	// Paquets qui ont une version corrigée, toutes failles comprises (agent 0.7.0).
	Fixes []Fix `json:"fixes"`
	// Image corrigée par Vigie (labels de l'image), nil sinon.
	Patched *images.Patched `json:"patched,omitempty"`
	// Dépôt source de l'image (label org.opencontainers.image.source), pour signaler une faille.
	Source string `json:"source,omitempty"`
}

// Fix est un paquet vulnérable dont une version corrigée est publiée.
type Fix struct {
	Ecosystem string `json:"ecosystem"`
	Package   string `json:"package"` // paquet source (Debian, Ubuntu), origine (Alpine) ou dépendance npm
	Version   string `json:"version"` // installée
	Fixed     string `json:"fixed"`   // version qui corrige toutes ses failles corrigées connues
	Counts    Counts `json:"counts"`  // ces failles, par gravité
}

// fixesOf regroupe par paquet les failles qui ont une version corrigée (les plus graves d'abord).
func fixesOf(all []Vuln) []Fix {
	index := map[[3]string]int{}
	out := []Fix{}
	for _, v := range all {
		if v.Fixed == "" || v.Negligible {
			continue
		}
		k := [3]string{v.Ecosystem, v.Package, v.Version}
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, Fix{Ecosystem: v.Ecosystem, Package: v.Package, Version: v.Version, Fixed: v.Fixed})
		}
		f := &out[i]
		if compareVersions(v.Ecosystem, v.Fixed, f.Fixed) > 0 {
			f.Fixed = v.Fixed
		}
		f.Counts.add(v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Counts, out[j].Counts
		for _, p := range [][2]int{{a.Critical, b.Critical}, {a.High, b.High}, {a.Medium, b.Medium}, {a.Total(), b.Total()}} {
			if p[0] != p[1] {
				return p[0] > p[1]
			}
		}
		return out[i].Package < out[j].Package
	})
	return out
}

// SystemPackages indique si l'écosystème est celui des paquets d'un système (Debian, Ubuntu, Alpine).
func SystemPackages(eco string) bool {
	switch ecoBase(eco) {
	case "Debian", "Ubuntu", "Alpine":
		return true
	}
	return false
}

// AtLeast indique si la gravité sev atteint le niveau level : critical, high, medium, ou low (toutes,
// gravité inconnue comprise).
func AtLeast(sev, level string) bool {
	if level == "low" {
		return true
	}
	r, ok := severityRank[level]
	if !ok {
		return false
	}
	s, ok := severityRank[sev]
	return ok && s <= r
}

// CountAtLeast compte les failles d'au moins ce niveau.
func (c Counts) CountAtLeast(level string) int {
	n := 0
	for _, p := range []struct {
		sev string
		n   int
	}{{"critical", c.Critical}, {"high", c.High}, {"medium", c.Medium}, {"low", c.Low}, {"unknown", c.Unknown}} {
		if AtLeast(p.sev, level) {
			n += p.n
		}
	}
	return n
}

// Plus additionne deux décomptes.
func (c Counts) Plus(o Counts) Counts {
	return Counts{c.Critical + o.Critical, c.High + o.High, c.Medium + o.Medium, c.Low + o.Low, c.Unknown + o.Unknown,
		c.Fixable + o.Fixable, c.Negligible + o.Negligible}
}

// CountVulns compte une liste de failles par gravité.
func CountVulns(list []Vuln) Counts {
	var c Counts
	for _, v := range list {
		c.add(v)
	}
	return c
}

// Progress décrit l'analyse en cours.
type Progress struct {
	Phase string `json:"phase"` // inventory, query, details
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Report est l'état renvoyé par GET /api/v1/vulns.
type Report struct {
	Enabled  bool      `json:"enabled"`
	Scanning bool      `json:"scanning"`
	Progress *Progress `json:"progress,omitempty"`
	LastScan int64     `json:"last_scan"` // fin de la dernière analyse réussie (ms), 0 : jamais
	Duration int64     `json:"duration"`  // durée de cette analyse (ms)
	Error    string    `json:"error,omitempty"`
	ErrorAt  int64     `json:"error_at,omitempty"`
	NextScan int64     `json:"next_scan"` // prochaine analyse planifiée (ms), 0 si désactivée
	Totals   Totals    `json:"totals"`
	Images   []Image   `json:"images"`
}

// Scanner planifie et exécute les analyses.
type Scanner struct {
	ctx      context.Context
	dk       Docker
	st       *store.Store
	settings func() settings.Settings
	log      *slog.Logger
	osv      *osvClient
	updates  func(string) bool
	now      func() time.Time
	started  time.Time

	// OnReport reçoit le résultat de chaque analyse réussie (alertes).
	OnReport func(Report)
	// OnNightly est appelé après l'analyse de la nuit (ou à sa place si une analyse vient d'avoir
	// lieu) : correction automatique des failles (agent 0.7.0).
	OnNightly func()

	mu          sync.Mutex
	report      Report
	scanning    bool
	again       bool // mise à jour d'image pendant une analyse : en relancer une ensuite
	nightPass   bool // créneau de nuit atteint : OnNightly à la fin de l'analyse en cours
	outdated    bool // résultat enregistré par un agent plus ancien : à refaire
	progress    Progress
	lastAttempt time.Time
	updTimer    *time.Timer
	meta        map[string]imageMeta // ID d'image → labels utiles (immuables)
}

func New(ctx context.Context, dk Docker, st *store.Store, set func() settings.Settings, log *slog.Logger, opt Options) *Scanner {
	base := opt.OSVURL
	if base == "" {
		base = os.Getenv("VIGIE_OSV_URL")
	}
	if base == "" {
		base = defaultOSV
	}
	s := &Scanner{
		ctx: ctx, dk: dk, st: st, settings: set, log: log,
		osv:     newOSVClient(base, opt.Version),
		updates: opt.UpdateAvailable,
		now:     time.Now,
		report:  Report{Images: []Image{}},
		meta:    map[string]imageMeta{},
	}
	if s.updates == nil {
		s.updates = func(string) bool { return false }
	}
	s.started = s.now()
	if raw := st.Get(ctx, reportKey); raw != "" {
		var r Report
		if err := json.Unmarshal([]byte(raw), &r); err == nil {
			if r.Images == nil {
				r.Images = []Image{}
			}
			for _, img := range r.Images {
				// Résultat d'un agent 0.6 : sans les paquets corrigeables, la correction des failles
				// n'a rien à proposer. Nouvelle analyse peu après le démarrage.
				if img.Fixes == nil && img.Counts.Fixable > 0 {
					s.outdated = true
				}
			}
			s.report = r
		} else {
			log.Warn("failles : dernier résultat illisible", "erreur", err)
		}
	}
	return s
}

// Run planifie les analyses : peu après l'installation, chaque nuit après les mises à jour automatiques,
// et en rattrapage si la dernière analyse est trop ancienne.
func (s *Scanner) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(s.now())
		}
	}
}

// nightlySlot renvoie le créneau de nuit dû (s'il y en a un) : l'heure des mises à jour automatiques + 1 h,
// aujourd'hui ou hier (créneau après minuit), pas encore lancé, dépassé depuis moins de 12 h.
func nightlySlot(now time.Time, clock, last string) (time.Time, bool) {
	t, err := time.Parse("15:04", clock)
	if err != nil {
		t, _ = time.Parse("15:04", "04:00")
	}
	y, m, d := now.Date()
	for _, dd := range []int{-1, 0} {
		at := time.Date(y, m, d+dd, t.Hour(), t.Minute()+int(nightlyOffset/time.Minute), 0, 0, now.Location())
		if !now.Before(at) && now.Sub(at) < 12*time.Hour && at.Format(dayLayout) != last {
			return at, true
		}
	}
	return time.Time{}, false
}

// nextSlot renvoie le prochain créneau de nuit (affiché dans l'appli).
func nextSlot(now time.Time, clock, last string) time.Time {
	if at, due := nightlySlot(now, clock, last); due {
		return at
	}
	t, err := time.Parse("15:04", clock)
	if err != nil {
		t, _ = time.Parse("15:04", "04:00")
	}
	y, m, d := now.Date()
	for dd := 0; dd <= 2; dd++ {
		at := time.Date(y, m, d+dd, t.Hour(), t.Minute()+int(nightlyOffset/time.Minute), 0, 0, now.Location())
		if at.After(now) && at.Format(dayLayout) != last {
			return at
		}
	}
	return time.Time{}
}

func (s *Scanner) tick(now time.Time) {
	set := s.settings()
	if !set.VulnScan {
		return
	}
	s.mu.Lock()
	last := time.UnixMilli(s.report.LastScan)
	never := s.report.LastScan == 0
	attempted := s.lastAttempt
	s.mu.Unlock()

	if at, due := nightlySlot(now, set.AutoUpdateTime, s.st.Get(s.ctx, nightlyKey)); due {
		_ = s.st.Set(s.ctx, nightlyKey, at.Format(dayLayout))
		s.mu.Lock()
		busy := s.scanning
		s.nightPass = true
		s.mu.Unlock()
		if !never && !busy && now.Sub(last) < 3*time.Hour {
			// Analyse récente (après les mises à jour de la nuit) : inutile de recommencer.
			s.mu.Lock()
			s.nightPass = false
			s.mu.Unlock()
			s.nightly()
			return
		}
		if err := s.Start("nightly"); err != nil && !errors.Is(err, ErrBusy) {
			s.mu.Lock()
			s.nightPass = false
			s.mu.Unlock()
			s.log.Warn("failles : analyse de nuit impossible", "erreur", err)
		}
		return
	}
	if now.Sub(s.started) < startupDelay || now.Sub(attempted) < retryAfter {
		return
	}
	s.mu.Lock()
	outdated := s.outdated
	s.outdated = false
	s.mu.Unlock()
	if never || outdated || now.Sub(last) > staleAfter {
		_ = s.Start("auto")
	}
}

// Start lance une analyse en arrière-plan (ErrDisabled, ErrBusy).
func (s *Scanner) Start(reason string) error {
	if !s.settings().VulnScan {
		return ErrDisabled
	}
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return ErrBusy
	}
	s.scanning = true
	s.progress = Progress{Phase: "inventory"}
	s.lastAttempt = s.now()
	s.mu.Unlock()
	go s.scan(reason)
	return nil
}

// ImagesUpdated demande l'analyse des nouvelles images, peu après une mise à jour (regroupées si plusieurs).
func (s *Scanner) ImagesUpdated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updTimer != nil {
		s.updTimer.Stop()
	}
	s.updTimer = time.AfterFunc(updateDelay, func() {
		if s.ctx.Err() != nil {
			return // agent en cours d'arrêt
		}
		if err := s.Start("update"); errors.Is(err, ErrBusy) {
			s.mu.Lock()
			s.again = true
			s.mu.Unlock()
		}
	})
}

// Scanning indique si une analyse est en cours.
func (s *Scanner) Scanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanning
}

func (s *Scanner) setProgress(phase string, done, total int) {
	s.mu.Lock()
	s.progress = Progress{Phase: phase, Done: done, Total: total}
	s.mu.Unlock()
}

func (s *Scanner) scan(reason string) {
	ctx, cancel := context.WithTimeout(s.ctx, scanTimeout)
	start := s.now()
	rep, err := s.analyze(ctx)
	cancel()
	end := s.now()

	if err == nil {
		rep = s.live(context.WithoutCancel(s.ctx), rep, false) // tri et totaux
	}
	s.mu.Lock()
	if err != nil {
		s.report.Error = err.Error()
		s.report.ErrorAt = end.UnixMilli()
	} else {
		rep.LastScan = end.UnixMilli()
		rep.Duration = end.Sub(start).Milliseconds()
		s.report = rep
	}
	saved := s.report
	s.mu.Unlock()
	if b, mErr := json.Marshal(saved); mErr == nil {
		if sErr := s.st.Set(context.WithoutCancel(s.ctx), reportKey, string(b)); sErr != nil {
			s.log.Warn("failles : résultat non enregistré", "erreur", sErr)
		}
	}
	if err != nil {
		s.log.Warn("failles : analyse impossible", "raison", reason, "erreur", err)
	} else {
		s.log.Info("failles : analyse terminée", "raison", reason, "images", rep.Totals.Images,
			"failles", rep.Totals.Total(), "critiques", rep.Totals.Critical, "corrigeables", rep.Totals.Fixable,
			"durée", end.Sub(start).Round(time.Second))
		if s.OnReport != nil {
			s.OnReport(rep)
		}
	}

	s.mu.Lock()
	s.scanning = false
	again := s.again
	s.again = false
	night := s.nightPass
	s.nightPass = false
	s.mu.Unlock()
	if night {
		if err == nil {
			s.nightly()
		} else {
			s.log.Warn("failles : pas de correction automatique cette nuit, l'analyse a échoué")
		}
	}
	if again && s.ctx.Err() == nil {
		_ = s.Start("update")
	}
}

// nightly appelle OnNightly en arrière-plan.
func (s *Scanner) nightly() {
	if s.OnNightly != nil && s.ctx.Err() == nil {
		go s.OnNightly()
	}
}

type imageGroup struct {
	id, ref    string
	containers []ContainerRef
}

// analyze lit les inventaires, interroge OSV et construit le résultat.
func (s *Scanner) analyze(ctx context.Context) (Report, error) {
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	list, err := s.dk.List(lctx)
	cancel()
	if err != nil {
		return Report{}, fmt.Errorf("Docker : %w", err)
	}
	groups := groupByImage(list)

	invs := make([]Inventory, len(groups))
	s.setProgress("inventory", 0, len(groups))
	for i, g := range groups {
		invs[i] = s.inventory(ctx, g)
		s.setProgress("inventory", i+1, len(groups))
		if ctx.Err() != nil {
			return Report{}, errors.New("analyse interrompue (délai dépassé)")
		}
	}

	perImage, err := s.lookup(ctx, invs, s.setProgress)
	if err != nil {
		return Report{}, err
	}

	rep := Report{Images: make([]Image, 0, len(groups))}
	for i, g := range groups {
		inv := invs[i]
		img := Image{
			ID: g.id, Ref: g.ref, Containers: g.containers, OS: inv.OS, Ecosystem: inv.Eco,
			Packages: inv.Count, Error: inv.Error, ReadAt: inv.ReadAt, Vulns: []Vuln{},
		}
		all := perImage[i]
		img.Fixes = fixesOf(all)
		for _, v := range all {
			img.Counts.add(v)
		}
		if len(all) > maxListed {
			img.More = len(all) - maxListed
			all = all[:maxListed]
		}
		if all != nil {
			img.Vulns = all
		}
		s.decorate(ctx, &img)
		rep.Images = append(rep.Images, img)
	}
	s.prune(ctx, groups)
	return rep, nil
}

// lookup interroge OSV pour les paquets des inventaires et renvoie, pour chacun, toutes ses failles
// (triées, bulletins redondants retirés). progress (facultatif) suit l'avancement.
func (s *Scanner) lookup(ctx context.Context, invs []Inventory, progress func(phase string, done, total int)) ([][]Vuln, error) {
	if progress == nil {
		progress = func(string, int, int) {}
	}
	// Une requête par paquet distinct, toutes images confondues.
	index := map[[3]string]int{}
	var queries []osvQuery
	for _, inv := range invs {
		for _, p := range inv.Packages {
			if _, ok := index[p]; ok {
				continue
			}
			index[p] = len(queries)
			var q osvQuery
			q.Package.Ecosystem, q.Package.Name, q.Version = p[0], p[1], p[2]
			queries = append(queries, q)
		}
	}
	progress("query", 0, len(queries))
	results, err := s.osv.queryBatch(ctx, queries, func(done int) { progress("query", done, len(queries)) })
	if err != nil {
		return nil, err
	}
	refs := map[string]string{} // ID → date de modification
	for _, r := range results {
		for _, v := range r {
			refs[v.ID] = v.Modified
		}
	}
	records, err := s.records(ctx, refs, progress)
	if err != nil {
		return nil, err
	}
	out := make([][]Vuln, len(invs))
	for i, inv := range invs {
		var all []Vuln
		for _, p := range inv.Packages {
			for _, ref := range results[index[p]] {
				rec := records[ref.ID]
				if rec == nil || rec.Withdrawn {
					continue
				}
				all = append(all, rec.vuln(p[0], p[1], p[2]))
			}
		}
		all = dropCoveredAdvisories(all, records)
		sortVulns(all)
		out[i] = all
	}
	return out, nil
}

// Assess recherche les failles connues des paquets d'un inventaire (liste complète). Sert à la
// correction des failles : nouvelle version d'une image avant son installation, image corrigée.
func (s *Scanner) Assess(ctx context.Context, inv Inventory) ([]Vuln, error) {
	if !s.settings().VulnScan {
		return nil, ErrDisabled
	}
	out, err := s.lookup(ctx, []Inventory{inv}, nil)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// StoredInventory renvoie l'inventaire enregistré d'une image (false s'il manque ou est ancien).
func (s *Scanner) StoredInventory(ctx context.Context, imageID string) (Inventory, bool) {
	var inv Inventory
	raw := s.st.VulnInventory(ctx, imageID)
	if raw == "" || json.Unmarshal([]byte(raw), &inv) != nil || inv.V != inventoryVersion || inv.Error != "" {
		return Inventory{}, false
	}
	return inv, true
}

// SaveInventory enregistre l'inventaire d'une image lu ailleurs (image corrigée, nouvelle version) :
// l'analyse suivante n'aura pas à le relire.
func (s *Scanner) SaveInventory(ctx context.Context, imageID string, inv Inventory) {
	if inv.Error != "" || imageID == "" {
		return
	}
	b, _ := json.Marshal(inv)
	if err := s.st.SetVulnInventory(context.WithoutCancel(ctx), imageID, string(b), inv.ReadAt); err != nil {
		s.log.Warn("failles : inventaire non enregistré", "image", imageID, "erreur", err)
	}
}

// Rescan relance une analyse complète (après une correction) ; en cours : une autre suivra.
func (s *Scanner) Rescan() {
	if err := s.Start("vulnfix"); errors.Is(err, ErrBusy) {
		s.mu.Lock()
		s.again = true
		s.mu.Unlock()
	}
}

// imageMeta : ce que la configuration d'une image apporte au résultat (labels).
type imageMeta struct {
	patched *images.Patched
	source  string
}

// decorate ajoute à l'image ce que disent ses labels : correction par Vigie, dépôt source.
func (s *Scanner) decorate(ctx context.Context, img *Image) {
	s.mu.Lock()
	m, ok := s.meta[img.ID]
	s.mu.Unlock()
	if !ok {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		det, err := s.dk.ImageDetails(dctx, img.ID)
		cancel()
		if err != nil {
			return // réessayé à la prochaine lecture
		}
		labels := images.LabelsOf(det.Config)
		m = imageMeta{patched: images.PatchedFrom(labels), source: labels["org.opencontainers.image.source"]}
		s.mu.Lock()
		s.meta[img.ID] = m
		s.mu.Unlock()
	}
	img.Patched, img.Source = m.patched, m.source
}

// Préfixes des conteneurs temporaires de la correction des failles (jamais analysés).
const (
	TempPatchPrefix   = "vigie-correctif-"
	TempAnalyzePrefix = "vigie-analyse-"
)

// Temporary indique si le conteneur est un conteneur temporaire de la correction des failles.
func Temporary(name string) bool {
	return strings.HasPrefix(name, TempPatchPrefix) || strings.HasPrefix(name, TempAnalyzePrefix)
}

func groupByImage(list []docker.Container) []imageGroup {
	byID := map[string]*imageGroup{}
	var order []string
	for _, c := range list {
		if c.ImageID == "" || Temporary(c.Name()) {
			continue
		}
		g := byID[c.ImageID]
		if g == nil {
			g = &imageGroup{id: c.ImageID}
			byID[c.ImageID] = g
			order = append(order, c.ImageID)
		}
		if g.ref == "" || strings.HasPrefix(g.ref, "sha256:") {
			g.ref = c.Image
		}
		g.containers = append(g.containers, ContainerRef{ID: c.ID, Name: c.Name(), State: c.State})
	}
	out := make([]imageGroup, 0, len(order))
	for _, id := range order {
		g := byID[id]
		// Un conteneur en marche d'abord : c'est lui qui sert à lire l'inventaire.
		sort.Slice(g.containers, func(i, j int) bool {
			ri, rj := g.containers[i].State == "running", g.containers[j].State == "running"
			if ri != rj {
				return ri
			}
			return g.containers[i].Name < g.containers[j].Name
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].containers[0].Name < out[j].containers[0].Name })
	return out
}

// inventory renvoie l'inventaire de l'image : depuis la base, sinon lu dans un de ses conteneurs.
func (s *Scanner) inventory(ctx context.Context, g imageGroup) Inventory {
	if raw := s.st.VulnInventory(ctx, g.id); raw != "" {
		var inv Inventory
		if json.Unmarshal([]byte(raw), &inv) == nil && inv.V == inventoryVersion && inv.Error == "" {
			return inv
		}
	}
	dirs := []string{"/app"}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if det, err := s.dk.ImageDetails(dctx, g.id); err == nil {
		if wd, _ := det.Config["WorkingDir"].(string); wd != "" {
			dirs = []string{wd, "/app"}
		}
	}
	cancel()
	var inv Inventory
	for i, c := range g.containers {
		if i >= 2 {
			break
		}
		inv = readInventory(ctx, s.dk, c.ID, dirs, s.now())
		if inv.OS == "" && len(inv.Packages) == 0 && inv.Error == "" {
			// Rien trouvé : image sans système (scratch) ou conteneur supprimé entre-temps ?
			ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := s.dk.Inspect(ictx, c.ID)
			cancel()
			if errors.Is(err, docker.ErrNotFound) {
				inv.Error = "conteneur supprimé pendant l'analyse"
				continue
			}
		}
		break
	}
	if inv.Error == "" && ctx.Err() == nil {
		b, _ := json.Marshal(inv)
		if err := s.st.SetVulnInventory(ctx, g.id, string(b), inv.ReadAt); err != nil {
			s.log.Warn("failles : inventaire non enregistré", "image", g.ref, "erreur", err)
		}
	} else if inv.Error != "" {
		s.log.Warn("failles : inventaire incomplet", "image", g.ref, "erreur", inv.Error)
	}
	return inv
}

// records renvoie les fiches des failles trouvées : depuis le cache si elles n'ont pas changé
// (date de modification) et ont moins de 3 jours, sinon depuis OSV.
func (s *Scanner) records(ctx context.Context, refs map[string]string, progress func(string, int, int)) (map[string]*record, error) {
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cached, err := s.st.VulnRecords(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("cache des failles : %w", err)
	}
	now := s.now()
	out := map[string]*record{}
	stale := map[string]*record{}
	var fetch []string
	for _, id := range ids {
		c, ok := cached[id]
		var r record
		if ok && json.Unmarshal([]byte(c.Data), &r) == nil {
			if c.Modified == refs[id] && now.Sub(time.UnixMilli(c.FetchedAt)) < recordTTL {
				out[id] = &r
				continue
			}
			stale[id] = &r
		}
		fetch = append(fetch, id)
	}

	fetched, failed := s.fetch(ctx, fetch, "details", progress)
	if ctx.Err() != nil {
		return nil, errors.New("analyse interrompue (délai dépassé)")
	}
	if len(fetch) > 0 && failed == len(fetch) && len(stale) == 0 {
		return nil, errors.New("OSV : impossible de lire le détail des failles")
	}
	for _, id := range fetch {
		switch r := fetched[id]; {
		case r != nil:
			out[id] = r
		case stale[id] != nil:
			out[id] = stale[id]
		default:
			out[id] = &record{ID: id, Severity: "unknown", Missing: true}
		}
	}

	// Gravité inconnue : celle de la fiche CVE correspondante (NVD), sinon l'urgence Debian.
	need := map[string][]*record{}
	for _, id := range fetch {
		r := fetched[id]
		if r == nil || r.Severity != "unknown" {
			continue
		}
		for _, cve := range r.CVEs {
			if cve != r.ID {
				need[cve] = append(need[cve], r)
				break
			}
		}
	}
	if len(need) > 0 {
		cveIDs := make([]string, 0, len(need))
		for id := range need {
			cveIDs = append(cveIDs, id)
		}
		sort.Strings(cveIDs)
		cves := map[string]*record{}
		cachedCVE, _ := s.st.VulnRecords(ctx, cveIDs)
		var missing []string
		for _, id := range cveIDs {
			var r record
			if c, ok := cachedCVE[id]; ok && now.Sub(time.UnixMilli(c.FetchedAt)) < recordTTL && json.Unmarshal([]byte(c.Data), &r) == nil {
				cves[id] = &r
			} else {
				missing = append(missing, id)
			}
		}
		got, _ := s.fetch(ctx, missing, "details", progress)
		for id, r := range got {
			cves[id] = r
			fetched[id] = r // en cache aussi
		}
		for id, list := range need {
			if c := cves[id]; c != nil && c.Severity != "unknown" {
				for _, r := range list {
					r.Severity, r.Score = c.Severity, c.Score
				}
			}
		}
	}
	for _, r := range fetched {
		if r.Severity == "unknown" && r.Urgency != "" {
			r.Severity = levelOfString(r.Urgency)
		}
	}

	rows := make([]store.VulnRecord, 0, len(fetched))
	for id, r := range fetched {
		b, _ := json.Marshal(r)
		rows = append(rows, store.VulnRecord{ID: id, Modified: r.Modified, Data: string(b), FetchedAt: now.UnixMilli()})
	}
	if err := s.st.SetVulnRecords(context.WithoutCancel(ctx), rows); err != nil {
		s.log.Warn("failles : fiches non enregistrées", "erreur", err)
	}
	return out, nil
}

// fetch lit des fiches OSV (4 à la fois, 5000 au plus par analyse) ; failed compte les échecs.
func (s *Scanner) fetch(ctx context.Context, ids []string, phase string, progress func(string, int, int)) (map[string]*record, int) {
	out := map[string]*record{}
	if len(ids) > maxDetailFetches {
		ids = ids[:maxDetailFetches]
	}
	if len(ids) == 0 {
		return out, 0
	}
	var mu sync.Mutex
	failed, done := 0, 0
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range detailWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				v, err := s.osv.vuln(ctx, id)
				mu.Lock()
				switch {
				case err == nil:
					r := reduce(v)
					out[id] = &r
				case errors.Is(err, errOSVNotFound):
					out[id] = &record{ID: id, Severity: "unknown", Missing: true, Withdrawn: true}
				default:
					failed++
					if failed == 1 {
						s.log.Warn("failles : détail illisible", "id", id, "erreur", err)
					}
				}
				done++
				n := done
				mu.Unlock()
				progress(phase, n, len(ids))
			}
		}()
	}
	progress(phase, 0, len(ids))
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return out, failed
}

// prune oublie les inventaires des images qui ne sont plus utilisées et les fiches anciennes.
func (s *Scanner) prune(ctx context.Context, groups []imageGroup) {
	keep := make([]string, len(groups))
	for i, g := range groups {
		keep[i] = g.id
	}
	if err := s.st.PruneVulnInventories(ctx, keep); err != nil {
		s.log.Warn("failles : nettoyage des inventaires", "erreur", err)
	}
	inUse := map[string]bool{}
	for _, id := range keep {
		inUse[id] = true
	}
	s.mu.Lock()
	for id := range s.meta {
		if !inUse[id] {
			delete(s.meta, id)
		}
	}
	s.mu.Unlock()
	if err := s.st.PruneVulnRecords(ctx, s.now().Add(-recordKeep).UnixMilli()); err != nil {
		s.log.Warn("failles : nettoyage du cache", "erreur", err)
	}
}

// vuln construit la faille d'un paquet à partir de sa fiche.
func (r *record) vuln(eco, name, version string) Vuln {
	aliases := []string{}
	for _, c := range r.CVEs {
		if c != r.ID {
			aliases = append(aliases, c)
		}
	}
	sev := r.Severity
	if sev == "" {
		sev = "unknown"
	}
	v := Vuln{
		ID: r.ID, Aliases: aliases, Summary: r.Summary, Severity: sev, Score: r.Score,
		Package: name, Version: version, Fixed: r.fixedFor(eco, name, version), Ecosystem: eco,
		URL: "https://osv.dev/vulnerability/" + r.ID,
	}
	v.Negligible, v.Note = r.negligible(eco, name)
	return v
}

// negligible : la distribution de l'image juge la faille sans importance (souvent contestée, ou sans
// effet pratique), quelle que soit la note CVSS publiée ailleurs.
func (r *record) negligible(eco, name string) (bool, string) {
	switch ecoBase(eco) {
	case "Debian":
		urgency := r.Urgency
		for _, a := range r.Affected {
			if a.Name == name && a.Eco == eco && a.Urgency != "" {
				urgency = a.Urgency // celle de la version de Debian de l'image l'emporte
			}
		}
		if strings.EqualFold(urgency, "unimportant") {
			return true, "Sans importance selon Debian"
		}
	case "Ubuntu":
		if strings.EqualFold(r.Priority, "negligible") {
			return true, "Négligeable selon Ubuntu"
		}
	}
	return false, ""
}

// dropCoveredAdvisories retire les bulletins regroupant plusieurs CVE (DSA, DLA, USN…) quand chacune
// de leurs CVE figure déjà, pour le même paquet, dans une fiche propre à cette CVE.
func dropCoveredAdvisories(list []Vuln, records map[string]*record) []Vuln {
	type key struct{ eco, pkg, ver string }
	covered := map[key]map[string]bool{}
	for _, v := range list {
		k := key{v.Ecosystem, v.Package, v.Version}
		for _, c := range records[v.ID].CVEs {
			if strings.Contains(v.ID, c) {
				if covered[k] == nil {
					covered[k] = map[string]bool{}
				}
				covered[k][c] = true
			}
		}
	}
	out := list[:0]
	for _, v := range list {
		cves := records[v.ID].CVEs
		redundant := len(cves) > 0
		for _, c := range cves {
			if strings.Contains(v.ID, c) || !covered[key{v.Ecosystem, v.Package, v.Version}][c] {
				redundant = false
				break
			}
		}
		if !redundant {
			out = append(out, v)
		}
	}
	return out
}

// sortVulns : les plus graves d'abord, puis celles qui ont une correction, puis par score et paquet.
func sortVulns(list []Vuln) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Negligible != b.Negligible {
			return b.Negligible // les failles sans importance en dernier
		}
		if ra, rb := severityRank[a.Severity], severityRank[b.Severity]; ra != rb {
			return ra < rb
		}
		if (a.Fixed != "") != (b.Fixed != "") {
			return a.Fixed != ""
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.ID < b.ID
	})
}

// worse compare deux images : la plus exposée d'abord.
func worse(a, b Image) bool {
	ca, cb := a.Counts, b.Counts
	for _, p := range [][2]int{{ca.Critical, cb.Critical}, {ca.High, cb.High}, {ca.Fixable, cb.Fixable}, {ca.Medium, cb.Medium}, {ca.Total(), cb.Total()}} {
		if p[0] != p[1] {
			return p[0] > p[1]
		}
	}
	return name(a) < name(b)
}

func name(img Image) string {
	if len(img.Containers) > 0 {
		return img.Containers[0].Name
	}
	return img.Ref
}

// Report renvoie le dernier résultat, avec les conteneurs actuels de chaque image et l'état de leurs mises à jour.
func (s *Scanner) Report(ctx context.Context) Report {
	s.mu.Lock()
	rep := s.report
	rep.Images = append([]Image(nil), s.report.Images...)
	rep.Scanning = s.scanning
	if s.scanning {
		p := s.progress
		rep.Progress = &p
	}
	s.mu.Unlock()
	set := s.settings()
	rep.Enabled = set.VulnScan
	if rep.Enabled {
		if at := nextSlot(s.now(), set.AutoUpdateTime, s.st.Get(ctx, nightlyKey)); !at.IsZero() {
			rep.NextScan = at.UnixMilli()
		}
	}
	return s.live(ctx, rep, true)
}

// live met à jour les conteneurs de chaque image (une image qui n'est plus utilisée disparaît),
// l'indication « mise à jour disponible », le tri et les totaux.
func (s *Scanner) live(ctx context.Context, rep Report, refresh bool) Report {
	if refresh {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		list, err := s.dk.List(lctx)
		cancel()
		if err == nil {
			current := map[string][]ContainerRef{}
			for _, g := range groupByImage(list) {
				current[g.id] = g.containers
			}
			kept := rep.Images[:0:0]
			for _, img := range rep.Images {
				if c, ok := current[img.ID]; ok {
					img.Containers = c
					kept = append(kept, img)
				}
			}
			rep.Images = kept
		}
	}
	for i := range rep.Images {
		img := &rep.Images[i]
		img.UpdateAvailable = false
		for _, c := range img.Containers {
			img.UpdateAvailable = img.UpdateAvailable || s.updates(c.Name)
		}
		if img.Fixes == nil {
			img.Fixes = []Fix{} // résultat d'un agent 0.6 : rempli à la prochaine analyse
		}
		s.decorate(ctx, img)
	}
	sort.SliceStable(rep.Images, func(i, j int) bool { return worse(rep.Images[i], rep.Images[j]) })
	rep.Totals = totals(rep.Images)
	if rep.Images == nil {
		rep.Images = []Image{}
	}
	return rep
}

func totals(images []Image) Totals {
	t := Totals{Images: len(images)}
	seen := map[[3]string]bool{}
	for _, img := range images {
		if img.Counts.Total() > 0 {
			t.Vulnerable++
		}
		for _, v := range img.Vulns {
			k := [3]string{v.ID, v.Ecosystem, v.Package}
			if !seen[k] {
				seen[k] = true
				t.add(v)
			}
		}
	}
	return t
}

func isNotFound(err error) bool { return errors.Is(err, docker.ErrNotFound) }
