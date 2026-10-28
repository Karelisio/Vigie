// Package storage suit l'occupation du disque principal (tendance, jours avant d'être plein) et la taille
// des volumes Docker, des images et du cache de construction (toutes les 6 h, historique de 30 jours).
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

const (
	scanKey       = "storage_scan"   // dernier calcul des volumes, conservé en base
	scanInterval  = 6 * time.Hour    // calcul planifié des volumes
	retryAfter    = 30 * time.Minute // après un échec
	scanTimeout   = 10 * time.Minute // GET /system/df peut être long sur de gros volumes
	refreshMin    = time.Minute      // calcul à la demande : une fois par minute au plus
	forecastEvery = time.Hour        // évaluation de l'alerte de prévision
	cacheFor      = time.Minute      // prévision mise en cache pour les lectures rapprochées
	historyPoints = 7 * 24 * time.Hour
	gib           = 1 << 30
)

// ErrTooSoon : un calcul a été lancé il y a moins d'une minute.
var ErrTooSoon = errors.New("calcul lancé il y a moins d'une minute")

// Point est l'occupation du disque (octets) à une heure donnée.
type Point struct {
	TS   int64 `json:"ts"`
	Used int64 `json:"used"`
}

// Disk décrit le disque principal et sa prévision.
type Disk struct {
	Mount  string `json:"mount"`
	Total  int64  `json:"total"`
	Used   int64  `json:"used"`
	Free   int64  `json:"free"`
	Status string `json:"status"` // ok, stable, insufficient
	// PerDay : tendance en octets par jour (nil sans assez de recul).
	PerDay *int64 `json:"trend_per_day"`
	// DaysLeft : jours avant d'être plein au rythme actuel (nil sans croissance nette).
	DaysLeft *float64 `json:"days_left"`
	Hours    float64  `json:"history_hours"`
	Cleanups int      `json:"cleanups"`
	Points   []Point  `json:"points"`
}

// Usage est un nombre d'éléments et leur taille totale.
type Usage struct {
	Count int   `json:"count"`
	Size  int64 `json:"size"`
}

// Volume est un volume nommé avec sa croissance.
type Volume struct {
	Name       string   `json:"name"`
	Project    string   `json:"project,omitempty"`
	Size       int64    `json:"size"`
	Containers []string `json:"containers"`
	// Croissance ramenée à 24 h et à 7 jours (nil sans mesure assez ancienne).
	GrowthDay  *int64            `json:"growth_day"`
	GrowthWeek *int64            `json:"growth_week"`
	Fast       bool              `json:"fast"`
	History    []store.SizePoint `json:"history"`
}

// Report est la réponse de GET /api/v1/storage.
type Report struct {
	Disk       Disk     `json:"disk"`
	Volumes    []Volume `json:"volumes"`
	Anonymous  Usage    `json:"anonymous"`
	Images     Usage    `json:"images"`
	BuildCache Usage    `json:"build_cache"`
	// FastGrowth : croissance quotidienne (octets) au-delà de laquelle un volume est signalé.
	FastGrowth int64  `json:"fast_growth"`
	VolumesAt  int64  `json:"volumes_at"` // dernier calcul des volumes (ms, 0 : jamais)
	Refreshing bool   `json:"refreshing"`
	Error      string `json:"error,omitempty"`
	ComputedAt int64  `json:"computed_at"`
}

// scan est le dernier calcul des volumes, conservé en base pour être servi dès le redémarrage.
type scan struct {
	At         int64        `json:"at"`
	Error      string       `json:"error,omitempty"`
	Images     Usage        `json:"images"`
	BuildCache Usage        `json:"build_cache"`
	Anonymous  Usage        `json:"anonymous"`
	Volumes    []scanVolume `json:"volumes"`
}

type scanVolume struct {
	Name       string   `json:"name"`
	Project    string   `json:"project,omitempty"`
	Size       int64    `json:"size"`
	Containers []string `json:"containers"`
}

// Docker : opérations utilisées (remplacées dans les tests).
type Docker interface {
	Version(ctx context.Context) (*docker.Version, error)
	SystemDF(ctx context.Context, typed bool) (*docker.DF, error)
	VolumeUsers(ctx context.Context) (map[string][]string, error)
}

type Service struct {
	st       *store.Store
	dk       Docker
	snapshot func() system.Snapshot
	settings func() settings.Settings
	log      *slog.Logger
	ctx      context.Context
	now      func() time.Time

	// OnForecast reçoit la prévision du disque toutes les heures (alerte « disque bientôt plein »).
	OnForecast func(Disk, []Volume)
	// OnVolumes reçoit les volumes après chaque calcul (alerte « volume qui grossit vite »).
	OnVolumes func([]Volume)

	mu        sync.Mutex
	last      scan
	running   bool
	lastStart time.Time
	disk      Disk
	diskAt    time.Time
}

func New(ctx context.Context, st *store.Store, dk Docker, snapshot func() system.Snapshot, set func() settings.Settings, log *slog.Logger) *Service {
	s := &Service{st: st, dk: dk, snapshot: snapshot, settings: set, log: log, ctx: ctx, now: time.Now}
	if raw := st.Get(ctx, scanKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.last); err != nil {
			log.Warn("dernier calcul des volumes illisible", "erreur", err)
			s.last = scan{}
		}
	}
	return s
}

// Run calcule les volumes peu après le démarrage (si le dernier calcul date de plus de 6 h), puis toutes
// les 6 h ; la prévision du disque est évaluée toutes les heures.
func (s *Service) Run(ctx context.Context) {
	scanT := time.NewTimer(max(2*time.Minute, s.untilDue()))
	defer scanT.Stop()
	fcT := time.NewTimer(5 * time.Minute)
	defer fcT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scanT.C:
			// Un calcul à la demande depuis la programmation repousse le suivant.
			if wait := s.untilDue(); wait > time.Minute {
				scanT.Reset(wait)
				continue
			}
			if s.begin(false) == nil {
				s.scan(ctx)
			}
			next := scanInterval
			s.mu.Lock()
			if s.last.Error != "" {
				next = retryAfter // Docker injoignable ou trop lent : nouvel essai plus tôt
			}
			s.mu.Unlock()
			scanT.Reset(next)
		case <-fcT.C:
			s.evaluate(ctx)
			fcT.Reset(forecastEvery)
		}
	}
}

// untilDue : temps restant avant le prochain calcul planifié (6 h après le dernier réussi).
func (s *Service) untilDue() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.At == 0 {
		return 0
	}
	return time.UnixMilli(s.last.At).Add(scanInterval).Sub(s.now())
}

// evaluate transmet la prévision du disque au moteur d'alertes.
func (s *Service) evaluate(ctx context.Context) {
	if s.OnForecast == nil {
		return
	}
	r := s.Report(ctx)
	if r.Disk.Total > 0 {
		s.OnForecast(r.Disk, r.Volumes)
	}
}

// begin réserve le calcul : jamais deux en parallèle ; à la demande (limited), une fois par minute au plus.
func (s *Service) begin(limited bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errRunning
	}
	if limited && s.now().Sub(s.lastStart) < refreshMin {
		return ErrTooSoon
	}
	s.running = true
	s.lastStart = s.now()
	return nil
}

var errRunning = errors.New("calcul en cours")

// Refresh lance un nouveau calcul des volumes en arrière-plan. Un calcul déjà en cours n'est pas relancé ;
// ErrTooSoon (avec l'attente restante) si le dernier a commencé il y a moins d'une minute.
func (s *Service) Refresh() (time.Duration, error) {
	switch err := s.begin(true); {
	case errors.Is(err, errRunning):
		return 0, nil
	case errors.Is(err, ErrTooSoon):
		s.mu.Lock()
		wait := refreshMin - s.now().Sub(s.lastStart)
		s.mu.Unlock()
		return wait, err
	}
	go s.scan(s.ctx)
	return 0, nil
}

// scan mesure images, volumes et cache via GET /system/df, puis enregistre la taille de chaque volume.
// L'appelant a réservé le calcul avec begin.
func (s *Service) scan(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	start := s.now()
	next, err := s.measure(ctx, start)
	if errors.Is(ctx.Err(), context.Canceled) {
		return // arrêt de l'agent : le dernier calcul reste valable
	}
	if err != nil {
		s.log.Warn("taille des volumes", "erreur", err)
		s.mu.Lock()
		s.last.Error = "Docker : " + err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.last.Error = "Docker n'a pas répondu en 10 minutes : calcul des volumes abandonné"
		}
		failed := s.last
		s.mu.Unlock()
		s.save(failed)
		return
	}
	sizes := map[string]int64{}
	for _, v := range next.Volumes {
		sizes[v.Name] = v.Size
	}
	if err := s.st.InsertVolumeSizes(ctx, next.At, sizes); err != nil {
		s.log.Warn("historique des volumes", "erreur", err)
	}
	if err := s.st.PruneVolumeSizes(ctx, start); err != nil {
		s.log.Warn("historique des volumes", "erreur", err)
	}
	s.mu.Lock()
	s.last = next
	s.mu.Unlock()
	s.save(next)
	s.log.Info("volumes mesurés", "volumes", len(next.Volumes), "durée", s.now().Sub(start).Round(time.Second))
	if s.OnVolumes != nil {
		s.OnVolumes(s.Report(ctx).Volumes)
	}
}

func (s *Service) save(sc scan) {
	b, _ := json.Marshal(sc)
	if err := s.st.Set(context.Background(), scanKey, string(b)); err != nil {
		s.log.Warn("dernier calcul des volumes", "erreur", err)
	}
}

var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// anonymous : volume créé sans nom (VOLUME d'une image, « -v /chemin »).
func anonymous(v docker.DFVolume) bool {
	_, labeled := v.Labels["com.docker.volume.anonymous"]
	return labeled || hexID.MatchString(v.Name)
}

func (s *Service) measure(ctx context.Context, at time.Time) (scan, error) {
	typed := false
	if v, err := s.dk.Version(ctx); err == nil {
		typed = v.AtLeast(1, 42) // filtre « type » : Docker 23 et plus
	}
	df, err := s.dk.SystemDF(ctx, typed)
	if err != nil {
		return scan{}, err
	}
	users, err := s.dk.VolumeUsers(ctx)
	if err != nil {
		s.log.Warn("conteneurs des volumes", "erreur", err)
		users = map[string][]string{}
	}
	out := scan{At: at.UnixMilli(), Volumes: []scanVolume{}}
	out.Images.Count = len(df.Images)
	out.Images.Size = df.LayersSize
	if out.Images.Size == 0 {
		for _, img := range df.Images {
			out.Images.Size += max(0, img.Size)
		}
	}
	for _, b := range df.BuildCache {
		out.BuildCache.Count++
		if !b.Shared {
			out.BuildCache.Size += max(0, b.Size)
		}
	}
	for _, v := range df.Volumes {
		size := int64(-1)
		if v.UsageData != nil {
			size = v.UsageData.Size
		}
		if anonymous(v) {
			out.Anonymous.Count++
			out.Anonymous.Size += max(0, size)
			continue
		}
		if size < 0 {
			continue // taille inconnue (pilote distant) : rien à suivre
		}
		ctrs := append([]string{}, users[v.Name]...)
		sort.Strings(ctrs)
		out.Volumes = append(out.Volumes, scanVolume{
			Name: v.Name, Project: v.Labels["com.docker.compose.project"], Size: size, Containers: ctrs,
		})
	}
	return out, nil
}

// Report assemble la prévision du disque et le dernier calcul des volumes.
func (s *Service) Report(ctx context.Context) Report {
	now := s.now()
	s.mu.Lock()
	last := s.last
	running := s.running
	s.mu.Unlock()

	fast := int64(s.settings().VolumeGrowthGB * gib)
	if fast <= 0 {
		fast = 5 * gib
	}
	r := Report{
		Disk:       s.Disk(ctx),
		Volumes:    []Volume{},
		Anonymous:  last.Anonymous,
		Images:     last.Images,
		BuildCache: last.BuildCache,
		FastGrowth: fast,
		VolumesAt:  last.At,
		Refreshing: running,
		Error:      last.Error,
		ComputedAt: now.UnixMilli(),
	}
	hist, err := s.st.VolumeHistory(ctx, now.Add(-8*24*time.Hour))
	if err != nil {
		s.log.Warn("historique des volumes", "erreur", err)
	}
	for _, v := range last.Volumes {
		pts := hist[v.Name]
		vol := Volume{
			Name: v.Name, Project: v.Project, Size: v.Size, Containers: v.Containers,
			GrowthDay:  growth(pts, 24*time.Hour, 18*time.Hour, 30*time.Hour),
			GrowthWeek: growth(pts, 7*24*time.Hour, 5*24*time.Hour, 8*24*time.Hour),
			History:    recent(pts, now.Add(-historyPoints)),
		}
		if vol.Containers == nil {
			vol.Containers = []string{}
		}
		vol.Fast = vol.GrowthDay != nil && *vol.GrowthDay >= fast
		r.Volumes = append(r.Volumes, vol)
	}
	sort.SliceStable(r.Volumes, func(i, j int) bool { return r.Volumes[i].Size > r.Volumes[j].Size })
	return r
}

// growth compare la dernière mesure à celle la plus proche de target auparavant (entre lo et hi),
// et ramène l'écart à target.
func growth(pts []store.SizePoint, target, lo, hi time.Duration) *int64 {
	if len(pts) < 2 {
		return nil
	}
	last := pts[len(pts)-1]
	best, bestGap := -1, time.Duration(math.MaxInt64)
	for i := len(pts) - 2; i >= 0; i-- {
		age := time.Duration(last.TS-pts[i].TS) * time.Millisecond
		if age < lo || age > hi {
			continue
		}
		if gap := (age - target).Abs(); gap < bestGap {
			best, bestGap = i, gap
		}
	}
	if best < 0 {
		return nil
	}
	age := float64(last.TS - pts[best].TS)
	g := int64(math.Round(float64(last.Size-pts[best].Size) * float64(target.Milliseconds()) / age))
	return &g
}

// recent garde les mesures depuis since, une par heure au plus (la dernière de l'heure).
func recent(pts []store.SizePoint, since time.Time) []store.SizePoint {
	const h = int64(time.Hour / time.Millisecond)
	out := []store.SizePoint{}
	for _, p := range pts {
		if p.TS < since.UnixMilli() {
			continue
		}
		if n := len(out); n > 0 && out[n-1].TS/h == p.TS/h {
			out[n-1] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

// Disk renvoie l'état du disque principal et sa prévision (mise en cache une minute).
func (s *Service) Disk(ctx context.Context) Disk {
	now := s.now()
	s.mu.Lock()
	if !s.diskAt.IsZero() && now.Sub(s.diskAt) < cacheFor && now.After(s.diskAt) {
		d := s.disk
		s.mu.Unlock()
		return d
	}
	s.mu.Unlock()

	snap := s.snapshot()
	d := Disk{Mount: "/", Total: int64(snap.DiskTotal), Used: int64(snap.DiskUsed), Status: StatusInsufficient, Points: []Point{}}
	if len(snap.Disks) > 0 {
		d.Mount = snap.Disks[0].Mount
	}
	d.Free = max(0, d.Total-d.Used)
	if d.Total <= 0 {
		return d // pas encore de mesure
	}
	rows, err := s.st.SysWindow(ctx, now.Add(-forecastWindow))
	if err != nil {
		s.log.Warn("prévision du disque", "erreur", err)
		return d
	}
	// L'historique conserve le pourcentage d'occupation (tranches de 15 min) : converti avec la taille actuelle.
	samples := make([]Sample, 0, len(rows))
	for _, p := range rows {
		samples = append(samples, Sample{TS: p.TS, Used: p.Disk / 100 * float64(d.Total)})
	}
	d.Points = Points(samples)
	d.Points = append(d.Points, Point{TS: now.UnixMilli(), Used: d.Used})
	t := Estimate(samples, float64(d.Total))
	d.Status, d.Hours, d.Cleanups = t.Status, t.Hours, t.Cleanups
	if t.Status != StatusInsufficient {
		per := int64(math.Round(t.PerDay))
		d.PerDay = &per
	}
	if t.Status == StatusOK {
		days := math.Round(DaysLeft(float64(d.Free), t.PerDay)*10) / 10
		if days <= 3650 {
			d.DaysLeft = &days
		} else {
			d.Status = StatusStable // plus de 10 ans : rien à prévoir
		}
	}
	s.mu.Lock()
	s.disk, s.diskAt = d, now
	s.mu.Unlock()
	return d
}
