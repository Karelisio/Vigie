// Package checks exécute les vérifications HTTP configurées (URL, code attendu, mot-clé, latence).
package checks

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

const recentCount = 40

type Status struct {
	store.Check
	State               string              `json:"state"` // up, down, pending, disabled
	Last                *store.CheckResult  `json:"last"`
	Uptime24h           float64             `json:"uptime_24h"` // -1 si aucune donnée
	Uptime7d            float64             `json:"uptime_7d"`
	Latency24h          float64             `json:"latency_24h"`
	ConsecutiveFailures int                 `json:"consecutive_failures"`
	Recent              []store.CheckResult `json:"recent"`
}

type Runner struct {
	st       *store.Store
	log      *slog.Logger
	client   *http.Client
	version  string
	failures int

	mu       sync.Mutex
	statuses map[int64]*Status
	running  map[int64]bool
	lastRun  map[int64]time.Time

	// OnChange est appelé après chaque résultat avec l'état complet.
	OnChange func([]Status)
	// OnResult est appelé avec l'état du check après chaque exécution.
	OnResult func(Status)
}

func NewRunner(st *store.Store, log *slog.Logger, version string, failures int) *Runner {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	tr.MaxIdleConnsPerHost = 2
	tr.DisableKeepAlives = true // mesure de latence réaliste (connexion complète)
	return &Runner{
		st:       st,
		log:      log,
		version:  version,
		failures: failures,
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("trop de redirections")
				}
				return nil
			},
		},
		statuses: map[int64]*Status{},
		running:  map[int64]bool{},
		lastRun:  map[int64]time.Time{},
	}
}

// Seed ajoute les checks déclarés dans VIGIE_CHECKS jamais ajoutés auparavant.
// Format : une entrée par ligne (ou séparée par « ; ») : Nom | URL | code | mot-clé
func (r *Runner) Seed(ctx context.Context, spec string) {
	seen := map[string]bool{}
	for _, n := range strings.Split(r.st.Get(ctx, "seeded_checks"), "\n") {
		if n != "" {
			seen[n] = true
		}
	}
	changed := false
	for _, line := range strings.FieldsFunc(spec, func(c rune) bool { return c == '\n' || c == ';' }) {
		parts := strings.Split(line, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[0], "#") {
			continue
		}
		if seen[parts[0]] {
			continue
		}
		c := store.Check{Name: parts[0], URL: parts[1], ExpectedStatus: 200, IntervalSec: 60, TimeoutMS: 10000, Enabled: true}
		if len(parts) > 2 && parts[2] != "" {
			if code, err := strconv.Atoi(parts[2]); err == nil {
				c.ExpectedStatus = code
			}
		}
		if len(parts) > 3 {
			c.Keyword = parts[3]
		}
		if err := Validate(&c); err != nil {
			r.log.Warn("check ignoré", "nom", c.Name, "erreur", err)
			continue
		}
		if _, err := r.st.CheckByName(ctx, c.Name); err == nil {
			seen[c.Name] = true
			changed = true
			continue
		}
		if err := r.st.CreateCheck(ctx, &c); err != nil {
			r.log.Error("ajout du check", "nom", c.Name, "erreur", err)
			continue
		}
		r.log.Info("check ajouté depuis VIGIE_CHECKS", "nom", c.Name, "url", c.URL)
		seen[c.Name] = true
		changed = true
	}
	if changed {
		names := make([]string, 0, len(seen))
		for n := range seen {
			names = append(names, n)
		}
		sort.Strings(names)
		_ = r.st.Set(ctx, "seeded_checks", strings.Join(names, "\n"))
	}
}

// Validate normalise et vérifie un check.
func Validate(c *store.Check) error {
	c.Name = strings.TrimSpace(c.Name)
	c.URL = strings.TrimSpace(c.URL)
	if c.Name == "" || len(c.Name) > 80 {
		return errors.New("nom requis (80 caractères max)")
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("URL http(s) invalide")
	}
	if c.ExpectedStatus == 0 {
		c.ExpectedStatus = 200
	}
	if c.ExpectedStatus < 100 || c.ExpectedStatus > 599 {
		return errors.New("code attendu invalide")
	}
	if c.IntervalSec == 0 {
		c.IntervalSec = 60
	}
	if c.IntervalSec < 15 {
		c.IntervalSec = 15
	}
	if c.IntervalSec > 3600 {
		c.IntervalSec = 3600
	}
	if c.TimeoutMS == 0 {
		c.TimeoutMS = 10000
	}
	if c.TimeoutMS < 1000 {
		c.TimeoutMS = 1000
	}
	if c.TimeoutMS > 60000 {
		c.TimeoutMS = 60000
	}
	if len(c.Keyword) > 200 {
		return errors.New("mot-clé trop long")
	}
	return nil
}

// Run planifie les checks jusqu'à l'annulation du contexte.
func (r *Runner) Run(ctx context.Context) {
	if err := r.Reload(ctx); err != nil {
		r.log.Error("chargement des checks", "erreur", err)
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.mu.Lock()
			var due []store.Check
			for id, s := range r.statuses {
				if !s.Enabled || r.running[id] {
					continue
				}
				if now.Sub(r.lastRun[id]) >= time.Duration(s.IntervalSec)*time.Second {
					r.running[id] = true
					r.lastRun[id] = now
					due = append(due, s.Check)
				}
			}
			r.mu.Unlock()
			for _, c := range due {
				go r.execute(ctx, c)
			}
		}
	}
}

// RunNow exécute immédiatement un check et renvoie son état.
func (r *Runner) RunNow(ctx context.Context, id int64) (Status, error) {
	c, err := r.st.Check(ctx, id)
	if err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	r.running[id] = true
	r.lastRun[id] = time.Now()
	r.mu.Unlock()
	r.execute(ctx, c)
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.statuses[id]; ok {
		return *s, nil
	}
	return Status{}, store.ErrNoRows
}

// Reload relit la liste des checks depuis la base (après création, modification ou suppression).
func (r *Runner) Reload(ctx context.Context) error {
	list, err := r.st.Checks(ctx)
	if err != nil {
		return err
	}
	next := map[int64]*Status{}
	for _, c := range list {
		r.mu.Lock()
		prev := r.statuses[c.ID]
		r.mu.Unlock()
		s := &Status{Check: c, State: "pending", Uptime24h: -1, Uptime7d: -1}
		if prev != nil {
			s.Last, s.Recent, s.ConsecutiveFailures = prev.Last, prev.Recent, prev.ConsecutiveFailures
			s.Uptime24h, s.Uptime7d, s.Latency24h = prev.Uptime24h, prev.Uptime7d, prev.Latency24h
		} else {
			r.fillHistory(ctx, s)
		}
		s.State = state(s)
		next[c.ID] = s
	}
	r.mu.Lock()
	r.statuses = next
	for id := range r.lastRun {
		if _, ok := next[id]; !ok {
			delete(r.lastRun, id)
		}
	}
	r.mu.Unlock()
	r.notify()
	return nil
}

// Forget force la réexécution d'un check modifié au prochain tick.
func (r *Runner) Forget(id int64) {
	r.mu.Lock()
	delete(r.lastRun, id)
	r.mu.Unlock()
}

func (r *Runner) fillHistory(ctx context.Context, s *Status) {
	if recent, err := r.st.RecentResults(ctx, s.ID, recentCount); err == nil {
		s.Recent = recent
		if n := len(recent); n > 0 {
			last := recent[n-1]
			s.Last = &last
			for i := n - 1; i >= 0 && !recent[i].OK; i-- {
				s.ConsecutiveFailures++
			}
		}
	}
	now := time.Now()
	s.Uptime24h, s.Latency24h, _ = r.st.Uptime(ctx, s.ID, now.Add(-24*time.Hour))
	s.Uptime7d, _, _ = r.st.Uptime(ctx, s.ID, now.Add(-7*24*time.Hour))
}

func state(s *Status) string {
	switch {
	case !s.Enabled:
		return "disabled"
	case s.Last == nil:
		return "pending"
	case s.Last.OK:
		return "up"
	}
	return "down"
}

// DownAfter renvoie le nombre d'échecs consécutifs avant alerte.
func (r *Runner) DownAfter() int { return r.failures }

func (r *Runner) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.statuses))
	for _, s := range r.statuses {
		cp := *s
		cp.Recent = append([]store.CheckResult(nil), s.Recent...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (r *Runner) notify() {
	if r.OnChange != nil {
		r.OnChange(r.Statuses())
	}
}

func (r *Runner) execute(ctx context.Context, c store.Check) {
	defer func() {
		r.mu.Lock()
		delete(r.running, c.ID)
		r.mu.Unlock()
	}()
	res := r.probe(ctx, c)
	if ctx.Err() != nil {
		return
	}
	if err := r.st.AddResult(ctx, res); err != nil {
		r.log.Warn("enregistrement du résultat", "check", c.Name, "erreur", err)
	}
	r.mu.Lock()
	s, ok := r.statuses[c.ID]
	if !ok {
		r.mu.Unlock()
		return
	}
	s.Last = &res
	s.Recent = append(s.Recent, res)
	if len(s.Recent) > recentCount {
		s.Recent = s.Recent[len(s.Recent)-recentCount:]
	}
	if res.OK {
		s.ConsecutiveFailures = 0
	} else {
		s.ConsecutiveFailures++
	}
	r.mu.Unlock()

	now := time.Now()
	up24, lat24, _ := r.st.Uptime(ctx, c.ID, now.Add(-24*time.Hour))
	up7, _, _ := r.st.Uptime(ctx, c.ID, now.Add(-7*24*time.Hour))
	r.mu.Lock()
	s.Uptime24h, s.Latency24h, s.Uptime7d = up24, lat24, up7
	s.State = state(s)
	snapshot := *s
	snapshot.Recent = append([]store.CheckResult(nil), s.Recent...)
	r.mu.Unlock()

	if r.OnResult != nil {
		r.OnResult(snapshot)
	}
	r.notify()
}

func (r *Runner) probe(ctx context.Context, c store.Check) store.CheckResult {
	res := store.CheckResult{CheckID: c.ID, TS: time.Now().UnixMilli()}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "Vigie/"+r.version+" (+https://github.com/Karelisio/Vigie)")
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		res.LatencyMS = time.Since(start).Milliseconds()
		res.Error = describe(err)
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	var body []byte
	if c.Keyword != "" {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	switch {
	case resp.StatusCode != c.ExpectedStatus:
		res.Error = fmt.Sprintf("code %d (attendu %d)", resp.StatusCode, c.ExpectedStatus)
	case c.Keyword != "" && !strings.Contains(string(body), c.Keyword):
		res.Error = fmt.Sprintf("mot-clé « %s » absent (réponse : %s)", c.Keyword, describeBody(resp.Header.Get("Content-Type"), body))
	default:
		res.OK = true
	}
	return res
}

// describeBody résume ce qui a été reçu, pour diagnostiquer un mot-clé absent.
func describeBody(contentType string, body []byte) string {
	ct := strings.ToLower(contentType)
	head := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 512)])))
	switch {
	case len(body) == 0:
		return "vide"
	case strings.Contains(ct, "html") || strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html"):
		if i := strings.Index(head, "<title>"); i >= 0 {
			if j := strings.Index(head[i:], "</title>"); j > 7 && i+j <= len(body) {
				return "page HTML « " + strings.TrimSpace(string(body[i+7:i+j])) + " »"
			}
		}
		return "page HTML"
	case strings.Contains(ct, "json") || strings.HasPrefix(head, "{") || strings.HasPrefix(head, "["):
		return "JSON sans ce mot-clé"
	case ct != "":
		return strings.SplitN(ct, ";", 2)[0]
	}
	return "texte"
}

func describe(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "délai dépassé"
	case errors.As(err, &dnsErr):
		return "DNS : " + dnsErr.Err
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "connexion refusée ou injoignable"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
