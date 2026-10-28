package vulns

// Client de l'API OSV (https://google.github.io/osv.dev/api/) : seuls des noms de paquets, leurs versions
// et leur écosystème sont envoyés. Réponses lues avec une taille maximale et un délai par requête.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	batchSize      = 1000 // requêtes au plus par appel à querybatch
	maxPages       = 20   // pages au plus par lot (next_page_token)
	batchTimeout   = 60 * time.Second
	detailTimeout  = 20 * time.Second
	maxBatchBody   = 32 << 20
	maxDetailBody  = 8 << 20
	maxErrorBody   = 4 << 10
	maxAffected    = 300
	maxSummaryRune = 200
)

var errOSVNotFound = errors.New("faille inconnue d'OSV")

// osvRetryDelay : attente avant le second essai d'un appel à querybatch (réduite dans les tests).
var osvRetryDelay = 3 * time.Second

type osvClient struct {
	base string
	http *http.Client
	ua   string
}

func newOSVClient(base, version string) *osvClient {
	return &osvClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 2 * batchTimeout},
		ua:   "Vigie-agent/" + version,
	}
}

type osvQuery struct {
	Package struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Version   string `json:"version"`
	PageToken string `json:"page_token,omitempty"`
}

// osvRef est une faille trouvée par querybatch (identifiant et date de modification seulement).
type osvRef struct {
	ID       string `json:"id"`
	Modified string `json:"modified"`
}

type batchResponse struct {
	Results []struct {
		Vulns         []osvRef `json:"vulns"`
		NextPageToken string   `json:"next_page_token"`
	} `json:"results"`
}

func (c *osvClient) do(ctx context.Context, method, path string, body any, limit int64, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("OSV ne répond pas (délai dépassé)")
		}
		return fmt.Errorf("OSV injoignable : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errOSVNotFound
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		var e struct {
			Message string `json:"message"`
		}
		text := strings.TrimSpace(string(msg))
		if json.Unmarshal(msg, &e) == nil && e.Message != "" {
			text = e.Message
		}
		return fmt.Errorf("OSV a répondu %d : %s", resp.StatusCode, truncate(text, 200))
	}
	lr := &io.LimitedReader{R: resp.Body, N: limit + 1}
	if err := json.NewDecoder(lr).Decode(out); err != nil {
		if lr.N <= 0 {
			return errors.New("réponse OSV trop volumineuse")
		}
		return fmt.Errorf("réponse OSV illisible : %w", err)
	}
	return nil
}

// queryBatch interroge OSV pour chaque paquet (lots de 1000, pages suivantes comprises) ; le résultat
// suit l'ordre des requêtes. progress reçoit le nombre de paquets traités.
func (c *osvClient) queryBatch(ctx context.Context, qs []osvQuery, progress func(done int)) ([][]osvRef, error) {
	out := make([][]osvRef, len(qs))
	for start := 0; start < len(qs); start += batchSize {
		end := min(start+batchSize, len(qs))
		pending := make([]int, 0, end-start) // index des requêtes à (re)lancer
		for i := start; i < end; i++ {
			qs[i].PageToken = ""
			pending = append(pending, i)
		}
		for page := 0; len(pending) > 0; page++ {
			if page >= maxPages {
				return nil, errors.New("OSV : trop de pages de résultats")
			}
			body := struct {
				Queries []osvQuery `json:"queries"`
			}{Queries: make([]osvQuery, len(pending))}
			for k, i := range pending {
				body.Queries[k] = qs[i]
			}
			var resp batchResponse
			err := c.do(ctx, http.MethodPost, "/v1/querybatch", body, maxBatchBody, batchTimeout, &resp)
			if err != nil && ctx.Err() == nil && !errors.Is(err, errOSVNotFound) {
				// Un nouvel essai après une erreur passagère.
				select {
				case <-ctx.Done():
				case <-time.After(osvRetryDelay):
				}
				resp = batchResponse{}
				err = c.do(ctx, http.MethodPost, "/v1/querybatch", body, maxBatchBody, batchTimeout, &resp)
			}
			if err != nil {
				return nil, err
			}
			if len(resp.Results) != len(pending) {
				return nil, fmt.Errorf("réponse OSV inattendue : %d résultats pour %d paquets", len(resp.Results), len(pending))
			}
			var next []int
			for k, i := range pending {
				r := resp.Results[k]
				out[i] = append(out[i], r.Vulns...)
				if r.NextPageToken != "" {
					qs[i].PageToken = r.NextPageToken
					next = append(next, i)
				}
			}
			pending = next
		}
		if progress != nil {
			progress(end)
		}
	}
	return out, nil
}

// ---- Fiche détaillée d'une faille ----

type osvSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

type osvSpecific struct {
	Severity json.RawMessage `json:"severity"`
	Urgency  string          `json:"urgency"`
}

type osvVuln struct {
	ID        string        `json:"id"`
	Summary   string        `json:"summary"`
	Details   string        `json:"details"`
	Modified  string        `json:"modified"`
	Withdrawn string        `json:"withdrawn"`
	Aliases   []string      `json:"aliases"`
	Upstream  []string      `json:"upstream"`
	Severity  []osvSeverity `json:"severity"`
	Affected  []struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		Severity []osvSeverity `json:"severity"`
		Ranges   []struct {
			Type   string `json:"type"`
			Events []struct {
				Introduced   string `json:"introduced"`
				Fixed        string `json:"fixed"`
				LastAffected string `json:"last_affected"`
			} `json:"events"`
		} `json:"ranges"`
		EcosystemSpecific osvSpecific `json:"ecosystem_specific"`
		DatabaseSpecific  osvSpecific `json:"database_specific"`
	} `json:"affected"`
	DatabaseSpecific osvSpecific `json:"database_specific"`
}

func (c *osvClient) vuln(ctx context.Context, id string) (*osvVuln, error) {
	var v osvVuln
	if err := c.do(ctx, http.MethodGet, "/v1/vulns/"+url.PathEscape(id), nil, maxDetailBody, detailTimeout, &v); err != nil {
		return nil, err
	}
	if v.ID == "" {
		return nil, errOSVNotFound
	}
	return &v, nil
}

// record est la partie utile d'une fiche OSV, conservée en base.
type record struct {
	ID        string     `json:"id"`
	Modified  string     `json:"m,omitempty"`
	Summary   string     `json:"s,omitempty"`
	CVEs      []string   `json:"c,omitempty"` // alias CVE (aliases et upstream)
	Severity  string     `json:"sev"`         // critical, high, medium, low, unknown
	Score     float64    `json:"sc,omitempty"`
	Urgency   string     `json:"u,omitempty"` // urgence Debian, dernier recours pour la gravité
	Affected  []affected `json:"a,omitempty"`
	Missing   bool       `json:"x,omitempty"` // fiche introuvable ou illisible
	Withdrawn bool       `json:"w,omitempty"` // retirée par OSV
}

type affected struct {
	Eco    string    `json:"e"`
	Name   string    `json:"n"`
	Ranges [][]event `json:"r,omitempty"`
}

type event struct {
	Introduced string `json:"i,omitempty"`
	Fixed      string `json:"f,omitempty"`
	Last       string `json:"l,omitempty"`
}

// Écosystèmes dont les plages sont conservées (les autres ne concernent pas les images analysées).
var keptEcosystems = map[string]bool{"Debian": true, "Ubuntu": true, "Alpine": true, "npm": true}

// reduce extrait d'une fiche OSV ce que Vigie affiche : résumé, alias CVE, gravité, plages de versions.
func reduce(v *osvVuln) record {
	r := record{ID: v.ID, Modified: v.Modified, Summary: summarize(v.Summary, v.Details), Withdrawn: v.Withdrawn != ""}
	seen := map[string]bool{}
	for _, a := range append(append([]string{v.ID}, v.Aliases...), v.Upstream...) {
		if strings.HasPrefix(a, "CVE-") && !seen[a] {
			seen[a] = true
			r.CVEs = append(r.CVEs, a)
		}
	}
	r.Severity, r.Score = severityOf(v.Severity)
	for _, a := range v.Affected {
		if r.Severity == "unknown" {
			r.Severity, r.Score = severityOf(a.Severity)
		}
		if r.Urgency == "" {
			r.Urgency = a.EcosystemSpecific.Urgency
		}
		if !keptEcosystems[ecoBase(a.Package.Ecosystem)] || len(r.Affected) >= maxAffected {
			continue
		}
		aff := affected{Eco: a.Package.Ecosystem, Name: a.Package.Name}
		for _, rg := range a.Ranges {
			if rg.Type != "ECOSYSTEM" && rg.Type != "SEMVER" {
				continue
			}
			var evs []event
			for _, e := range rg.Events {
				evs = append(evs, event{Introduced: e.Introduced, Fixed: e.Fixed, Last: e.LastAffected})
			}
			aff.Ranges = append(aff.Ranges, evs)
		}
		r.Affected = append(r.Affected, aff)
	}
	if r.Severity == "unknown" {
		r.Severity = levelOf(v.DatabaseSpecific.Severity)
	}
	for _, a := range v.Affected {
		if r.Severity != "unknown" {
			break
		}
		if r.Severity = levelOf(a.DatabaseSpecific.Severity); r.Severity == "unknown" {
			r.Severity = levelOf(a.EcosystemSpecific.Severity)
		}
	}
	return r
}

// summarize : résumé OSV, sinon début de la description (première phrase), 200 caractères au plus.
func summarize(summary, details string) string {
	s := strings.TrimSpace(summary)
	if s == "" {
		s = strings.TrimSpace(details)
		if i := strings.IndexAny(s, "\n"); i > 0 {
			s = s[:i]
		}
		if i := strings.Index(s, ". "); i > 0 {
			s = s[:i+1]
		}
	}
	return truncate(strings.Join(strings.Fields(s), " "), maxSummaryRune)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// Rang des gravités, de la plus forte à la plus faible.
var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "unknown": 4}

// levelFromScore applique l'échelle CVSS (v3 et v4).
func levelFromScore(s float64) string {
	switch {
	case s >= 9:
		return "critical"
	case s >= 7:
		return "high"
	case s >= 4:
		return "medium"
	}
	return "low"
}

// severityOf : vecteur CVSS v3.x en priorité, sinon v4, sinon priorité Ubuntu.
func severityOf(list []osvSeverity) (string, float64) {
	for _, typ := range []string{"CVSS_V3", "CVSS_V4"} {
		for _, s := range list {
			if s.Type != typ {
				continue
			}
			score, err := cvss3Score(s.Score)
			if typ == "CVSS_V4" {
				score, err = cvss4Score(s.Score)
			}
			if err == nil {
				return levelFromScore(score), score
			}
		}
	}
	for _, s := range list {
		if s.Type == "Ubuntu" {
			if lvl := levelOfString(s.Score); lvl != "unknown" {
				return lvl, 0
			}
		}
	}
	return "unknown", 0
}

// levelOf traduit une gravité textuelle (GitHub, Ubuntu, Debian…) ; « unknown » si absente ou inconnue.
func levelOf(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return "unknown"
	}
	return levelOfString(s)
}

func levelOfString(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "high", "important":
		return "high"
	case "moderate", "medium":
		return "medium"
	case "low", "negligible", "unimportant":
		return "low"
	}
	return "unknown"
}

// fixedFor renvoie la version qui corrige la faille pour ce paquet et cette version installée
// (« » si aucune correction n'est publiée). La plage qui contient la version installée l'emporte ;
// à défaut, la plus petite version corrigée supérieure à celle installée.
func (r *record) fixedFor(eco, name, installed string) string {
	var candidates []string
	matched := false
	for _, a := range r.Affected {
		if a.Name != name || (a.Eco != eco && a.Eco != ecoBase(eco)) {
			continue
		}
		for _, evs := range a.Ranges {
			introduced := ""
			for _, e := range evs {
				switch {
				case e.Introduced != "":
					introduced = e.Introduced
				case e.Fixed != "":
					candidates = append(candidates, e.Fixed)
					if introduced != "" && contains(eco, introduced, e.Fixed, installed) {
						return e.Fixed
					}
					introduced = ""
				case e.Last != "":
					if introduced != "" && (introduced == "0" || compareVersions(eco, installed, introduced) >= 0) &&
						compareVersions(eco, installed, e.Last) <= 0 {
						matched = true // vulnérable jusqu'à cette version comprise : pas de correction
					}
					introduced = ""
				}
			}
			if introduced != "" && (introduced == "0" || compareVersions(eco, installed, introduced) >= 0) {
				matched = true // plage ouverte : pas encore corrigée
			}
		}
	}
	if matched {
		return ""
	}
	best := ""
	for _, f := range candidates {
		if compareVersions(eco, f, installed) > 0 && (best == "" || compareVersions(eco, f, best) < 0) {
			best = f
		}
	}
	return best
}

// contains : installed ∈ [introduced, fixed[.
func contains(eco, introduced, fixed, installed string) bool {
	return (introduced == "0" || compareVersions(eco, installed, introduced) >= 0) && compareVersions(eco, installed, fixed) < 0
}
