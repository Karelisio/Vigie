package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/karelisio/vigie/agent/internal/docker"
)

// ---- Nouveautés : notes de version publiées sur GitHub pour l'image d'un conteneur ----

const (
	labelSource  = "org.opencontainers.image.source"
	labelVersion = "org.opencontainers.image.version"
	maxBody      = 6000    // caractères par note de version
	maxReleases  = 4 << 20 // taille maximale de la réponse de GitHub
	releasesTTL  = time.Hour
	// Une release est souvent publiée juste après la construction de son image par la CI.
	buildMargin = time.Hour
)

var (
	errRateLimited = errors.New("limite de l'API GitHub atteinte, réessaie plus tard")
	errNoRelease   = errors.New("pas de release publiée sur GitHub")
)

type release struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	PublishedAt int64  `json:"published_at"`
	Body        string `json:"body"` // Markdown
	URL         string `json:"url"`
	Newer       bool   `json:"newer"` // publiée après la version du conteneur
}

type changelog struct {
	Source   string    `json:"source"`  // github.com/owner/repo, vide si inconnu
	Current  string    `json:"current"` // version du conteneur, vide si inconnue
	Releases []release `json:"releases"`
	Error    string    `json:"error,omitempty"`
}

func (s *Server) containerChangelog(w http.ResponseWriter, r *http.Request) {
	c, ok := s.find(w, r)
	if !ok {
		return
	}
	in, err := s.dk.Inspect(r.Context(), c.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	// Image en cours d'utilisation (et non la dernière téléchargée pour ce tag).
	img, err := s.dk.ImageDetails(r.Context(), in.Image)
	if err != nil {
		img = &docker.ImageDetails{}
	}
	out := changelog{Current: currentVersion(img.Label(labelVersion), in.Config.Image), Releases: []release{}}
	repo, why := imageSource(img.Label(labelSource), in.Config.Image)
	if repo == "" {
		out.Error = why
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Source = "github.com/" + repo
	list, err := githubReleases.get(r.Context(), repo, s.version)
	switch {
	case err != nil:
		out.Error = err.Error()
	case len(list) == 0:
		out.Error = errNoRelease.Error()
	default:
		built, _ := time.Parse(time.RFC3339Nano, img.Created)
		out.Releases = markNewer(list, out.Current, built)
	}
	writeJSON(w, http.StatusOK, out)
}

// imageSource renvoie le dépôt GitHub (« owner/repo ») de l'image, d'après son label de source ou
// sa référence ghcr.io ; sinon, la raison de l'échec.
func imageSource(label, ref string) (string, string) {
	if repo := githubRepo(label); repo != "" {
		return repo, ""
	}
	name, _, _ := strings.Cut(ref, "@")
	name, _ = docker.SplitRef(name)
	if rest, ok := strings.CutPrefix(name, "ghcr.io/"); ok {
		if repo := githubRepo("github.com/" + rest); repo != "" {
			return repo, ""
		}
	}
	if label != "" {
		return "", "le code source de cette image n'est pas sur GitHub (" + label + ")"
	}
	return "", "l'image n'indique pas son dépôt de code source (label " + labelSource + " absent)"
}

var githubName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// githubRepo extrait « owner/repo » d'une adresse github.com (https, ssh ou sans schéma).
func githubRepo(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if _, rest, ok := strings.Cut(u, "://"); ok {
		u = rest
	}
	u = strings.TrimPrefix(strings.TrimPrefix(u, "git@"), "www.")
	if len(u) < len("github.com/") || !strings.EqualFold(u[:len("github.com")], "github.com") {
		return ""
	}
	if sep := u[len("github.com")]; sep != '/' && sep != ':' {
		return ""
	}
	parts := strings.Split(u[len("github.com/"):], "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	for _, p := range []string{owner, repo} {
		if !githubName.MatchString(p) || strings.Trim(p, ".") == "" {
			return ""
		}
	}
	return owner + "/" + repo
}

// currentVersion lit la version déclarée par l'image, sinon le tag de sa référence (sauf « latest »).
func currentVersion(label, ref string) string {
	v := strings.TrimSpace(label)
	if v == "" && !strings.Contains(ref, "@") {
		_, v = docker.SplitRef(ref)
	}
	if v == "latest" {
		return ""
	}
	return v
}

// markNewer signale les releases publiées après celle de la version actuelle (tag identique, au
// « v » près) ; si elle n'est pas dans la liste, après la construction de l'image locale.
func markNewer(list []release, current string, built time.Time) []release {
	out := append([]release{}, list...)
	var since int64
	found := false
	if cur := strings.TrimPrefix(current, "v"); cur != "" {
		for _, r := range out {
			if strings.TrimPrefix(r.Tag, "v") == cur {
				since, found = r.PublishedAt, true
				break
			}
		}
	}
	if !found {
		if built.IsZero() {
			return out
		}
		since = built.Add(buildMargin).UnixMilli()
	}
	for i := range out {
		out[i].Newer = out[i].PublishedAt > since
	}
	return out
}

// ---- Client GitHub ----

// releaseCache garde les releases de chaque dépôt en mémoire : sans authentification, l'API de
// GitHub n'accepte que 60 requêtes par heure.
type releaseCache struct {
	base string
	http *http.Client

	mu      sync.Mutex
	entries map[string]cachedReleases
}

type cachedReleases struct {
	list []release
	at   time.Time
}

var githubReleases = newReleaseCache("https://api.github.com")

func newReleaseCache(base string) *releaseCache {
	return &releaseCache{base: base, http: &http.Client{Timeout: 10 * time.Second}, entries: map[string]cachedReleases{}}
}

// get renvoie les 10 dernières releases publiées (liste vide si le dépôt est introuvable).
func (c *releaseCache) get(ctx context.Context, repo, version string) ([]release, error) {
	key := strings.ToLower(repo)
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Since(e.at) < releasesTTL {
		return e.list, nil
	}
	list, err := c.fetch(ctx, repo, version)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	for k, old := range c.entries {
		if time.Since(old.at) >= releasesTTL {
			delete(c.entries, k)
		}
	}
	c.entries[key] = cachedReleases{list: list, at: time.Now()}
	c.mu.Unlock()
	return list, nil
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

func (c *releaseCache) fetch(ctx context.Context, repo, version string) ([]release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/repos/"+repo+"/releases?per_page=10", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vigie-agent/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub injoignable : %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return []release{}, nil // dépôt introuvable ou privé
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errRateLimited
	default:
		return nil, fmt.Errorf("GitHub : code %d", resp.StatusCode)
	}
	var raw []struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		Draft       bool      `json:"draft"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleases)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("réponse de GitHub illisible : %w", err)
	}
	out := []release{}
	for _, r := range raw {
		if r.Draft || r.PublishedAt.IsZero() {
			continue
		}
		body := strings.TrimSpace(htmlComment.ReplaceAllString(strings.ReplaceAll(r.Body, "\r\n", "\n"), ""))
		if utf8.RuneCountInString(body) > maxBody {
			body = string([]rune(body)[:maxBody-1]) + "…"
		}
		out = append(out, release{Tag: r.TagName, Name: r.Name, PublishedAt: r.PublishedAt.UnixMilli(), Body: body, URL: r.HTMLURL})
	}
	return out, nil
}
