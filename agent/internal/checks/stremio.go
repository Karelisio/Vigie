package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	KindHTTP    = "http"
	KindStremio = "stremio"

	// DefaultStremioID est le contenu de test par défaut (The Shawshank Redemption).
	DefaultStremioID = "tt0111161"

	// Une recherche de liens sollicite les services debrid de l'addon : au plus une toutes les 5 min,
	// avec un délai de réponse généreux.
	stremioMinInterval = 300
	stremioMinTimeout  = 30000
)

// Film (tt0111161) ou épisode (tt0903747:1:1 : série, saison, épisode).
var stremioIDRe = regexp.MustCompile(`^tt\d{1,10}(:\d{1,5}:\d{1,5})?$`)

func validateStremio(c *store.Check, u *url.URL) error {
	if !strings.HasSuffix(u.Path, "/manifest.json") {
		return errors.New("addon Stremio : l'URL doit être celle du manifest (…/manifest.json)")
	}
	c.StremioID = strings.TrimSpace(c.StremioID)
	if c.StremioID == "" {
		c.StremioID = DefaultStremioID
	}
	if !stremioIDRe.MatchString(c.StremioID) {
		return errors.New("contenu de test invalide : identifiant IMDb attendu (tt0111161, ou tt0903747:1:1 pour un épisode)")
	}
	c.IntervalSec = max(c.IntervalSec, stremioMinInterval)
	c.TimeoutMS = max(c.TimeoutMS, stremioMinTimeout)
	return nil
}

// streamURL déduit de l'adresse du manifest celle de la recherche de liens pour un contenu.
func streamURL(manifest, id string) string {
	base := manifest
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	kind := "movie"
	if strings.Contains(id, ":") {
		kind = "series"
	}
	return strings.TrimSuffix(base, "manifest.json") + "stream/" + kind + "/" + id + ".json"
}

// probeStremio teste l'addon comme le ferait Stremio : manifest, puis liens d'un contenu connu.
// Le code attendu et le mot-clé ne s'appliquent pas à ce mode.
func (r *Runner) probeStremio(ctx context.Context, c store.Check) store.CheckResult {
	res := store.CheckResult{CheckID: c.ID, TS: time.Now().UnixMilli()}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMS)*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := r.stremioLinks(ctx, c, &res); err != nil {
		res.Error = err.Error()
	} else {
		res.OK = true
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	return res
}

// stremioLinks renseigne le code HTTP, le certificat et le nombre de liens trouvés.
func (r *Runner) stremioLinks(ctx context.Context, c store.Check, res *store.CheckResult) error {
	var manifest struct {
		Resources []json.RawMessage `json:"resources"`
	}
	resp, err := r.fetchJSON(ctx, c.URL, "manifest", 2<<20, &manifest)
	if resp != nil {
		res.Status = resp.StatusCode
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			res.CertExpires = resp.TLS.PeerCertificates[0].NotAfter.UnixMilli()
		}
	}
	if err != nil {
		return err
	}
	if !hasStreamResource(manifest.Resources) {
		return errors.New("manifest sans ressource « stream »")
	}

	var streams struct {
		Streams []struct {
			URL      string `json:"url"`
			InfoHash string `json:"infoHash"`
			YtID     string `json:"ytId"`
			NzbURL   string `json:"nzbUrl"`
		} `json:"streams"`
	}
	resp, err = r.fetchJSON(ctx, streamURL(c.URL, c.StremioID), "liens", 8<<20, &streams)
	if resp != nil {
		res.Status = resp.StatusCode
	}
	if err != nil {
		return err
	}
	// Seules les entrées lisibles comptent (les addons signalent parfois leurs erreurs par une fausse entrée).
	for _, s := range streams.Streams {
		if s.URL != "" || s.InfoHash != "" || s.YtID != "" || s.NzbURL != "" {
			res.Links++
		}
	}
	if res.Links == 0 {
		return errors.New("aucun lien pour " + c.StremioID)
	}
	return nil
}

// hasStreamResource cherche « stream » parmi les ressources, écrites en chaîne ou en objet {"name": …}.
func hasStreamResource(resources []json.RawMessage) bool {
	for _, raw := range resources {
		var name string
		if json.Unmarshal(raw, &name) == nil && name == "stream" {
			return true
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Name == "stream" {
			return true
		}
	}
	return false
}

// fetchJSON lit une réponse JSON ; l'erreur est déjà formulée pour l'utilisateur.
// La réponse (fermée) est renvoyée dès qu'elle existe, pour son code et son certificat.
func (r *Runner) fetchJSON(ctx context.Context, u, what string, limit int64, out any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	r.setHeaders(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s : %s", what, describe(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return resp, fmt.Errorf("%s : %s", what, describe(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, fmt.Errorf("%s : code %d", what, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		msg := "réponse JSON invalide (" + what + ")"
		if d := describeBody(resp.Header.Get("Content-Type"), body); !strings.HasPrefix(d, "JSON") {
			msg += " : " + d
		}
		return resp, errors.New(msg)
	}
	return resp, nil
}
