// Package images détecte les mises à jour d'images : compare le digest publié sur le registre
// (requête HEAD, qui ne compte pas dans les quotas de Docker Hub) à celui de l'image utilisée par le conteneur.
package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

// Ref est une référence d'image décomposée.
type Ref struct {
	Registry string // hôte de l'API (registry-1.docker.io pour Docker Hub)
	Repo     string // ex. library/redis
	Tag      string
}

// Repo tel qu'il apparaît dans RepoDigests (docker.io/… pour Docker Hub).
func (r Ref) digestPrefix() []string {
	if r.Registry == "registry-1.docker.io" {
		short := strings.TrimPrefix(r.Repo, "library/")
		return []string{r.Repo + "@", short + "@", "docker.io/" + r.Repo + "@"}
	}
	return []string{r.Registry + "/" + r.Repo + "@"}
}

// Parse décompose une référence ; ok=false pour une image épinglée par digest ou locale.
func Parse(ref string) (Ref, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "@") || strings.HasPrefix(ref, "sha256:") {
		return Ref{}, false
	}
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	r := Ref{Tag: tag}
	first, rest, hasSlash := strings.Cut(name, "/")
	if hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Registry, r.Repo = first, rest
	} else {
		r.Registry, r.Repo = "registry-1.docker.io", name
		if !hasSlash {
			r.Repo = "library/" + name
		}
	}
	if r.Registry == "docker.io" || r.Registry == "index.docker.io" {
		r.Registry = "registry-1.docker.io"
	}
	if r.Registry == "localhost" || strings.HasPrefix(r.Registry, "localhost:") {
		return Ref{}, false
	}
	return r, true
}

type remote struct {
	digest  string
	checked time.Time
	err     string
}

// Status d'un conteneur vis-à-vis de son image.
type Status struct {
	UpdateAvailable bool   `json:"update_available"`
	CheckedAt       int64  `json:"checked_at"`
	Error           string `json:"error,omitempty"`
	// Ignored : la version publiée a été écartée par un retour à la version précédente.
	Ignored bool `json:"ignored,omitempty"`
}

type Checker struct {
	http *http.Client
	log  *slog.Logger

	mu     sync.Mutex
	remote map[string]remote // référence → digest distant
}

func NewChecker(log *slog.Logger) *Checker {
	return &Checker{http: &http.Client{Timeout: 20 * time.Second}, log: log, remote: map[string]remote{}}
}

// RemoteDigest renvoie le digest publié (mis en cache 6 h).
func (c *Checker) RemoteDigest(ctx context.Context, ref string, force bool) (string, error) {
	c.mu.Lock()
	cached, ok := c.remote[ref]
	c.mu.Unlock()
	if ok && !force && time.Since(cached.checked) < 6*time.Hour {
		if cached.err != "" {
			return "", errors.New(cached.err)
		}
		return cached.digest, nil
	}
	r, valid := Parse(ref)
	if !valid {
		return "", errors.New("référence non vérifiable")
	}
	digest, err := c.head(ctx, r)
	entry := remote{digest: digest, checked: time.Now()}
	if err != nil {
		entry.err = err.Error()
	}
	c.mu.Lock()
	c.remote[ref] = entry
	c.mu.Unlock()
	return digest, err
}

func (c *Checker) head(ctx context.Context, r Ref) (string, error) {
	u := fmt.Sprintf("https://%s/v2/%s/manifests/%s", r.Registry, r.Repo, url.PathEscape(r.Tag))
	do := func(token string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		req.Header.Set("Accept", manifestAccept)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.http.Do(req)
	}
	resp, err := do("")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		token, err := c.token(ctx, resp.Header.Get("WWW-Authenticate"), r)
		if err != nil {
			return "", err
		}
		if resp, err = do(token); err != nil {
			return "", err
		}
		resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registre : code %d", resp.StatusCode)
	}
	d := resp.Header.Get("Docker-Content-Digest")
	if d == "" {
		return "", errors.New("registre : digest absent")
	}
	return d, nil
}

// token obtient un jeton anonyme selon l'en-tête WWW-Authenticate (Bearer realm=…,service=…,scope=…).
func (c *Checker) token(ctx context.Context, challenge string, r Ref) (string, error) {
	params := map[string]string{}
	rest, ok := strings.CutPrefix(challenge, "Bearer ")
	if !ok {
		return "", errors.New("authentification du registre non prise en charge")
	}
	for _, part := range strings.Split(rest, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if found {
			params[k] = strings.Trim(v, `"`)
		}
	}
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("registre : realm absent")
	}
	q := url.Values{}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + r.Repo + ":pull"
	}
	q.Set("scope", scope)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("jeton du registre : %w", err)
	}
	if out.Token != "" {
		return out.Token, nil
	}
	if out.AccessToken != "" {
		return out.AccessToken, nil
	}
	return "", errors.New("jeton du registre vide")
}

// UpdateAvailable compare le digest distant aux RepoDigests de l'image utilisée.
func UpdateAvailable(ref string, remoteDigest string, repoDigests []string) (bool, bool) {
	r, ok := Parse(ref)
	if !ok || remoteDigest == "" || len(repoDigests) == 0 {
		return false, false
	}
	matchedRepo := false
	for _, rd := range repoDigests {
		for _, p := range r.digestPrefix() {
			if strings.HasPrefix(rd, p) {
				matchedRepo = true
				if strings.TrimPrefix(rd, p) == remoteDigest {
					return false, true
				}
			}
		}
	}
	if !matchedRepo {
		return false, false // image construite localement ou d'un autre dépôt
	}
	return true, true
}
