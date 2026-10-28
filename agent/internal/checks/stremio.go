package checks

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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

// addonURL déduit de l'adresse du manifest celle d'une ressource : <base>/<ressource>/<type>/<id>.json.
func addonURL(manifest, resource, kind, id string) string {
	base := manifest
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	return strings.TrimSuffix(base, "manifest.json") + resource + "/" + kind + "/" + id + ".json"
}

// probeStremio teste l'addon comme le ferait Stremio : manifest, puis la ressource qu'il fournit
// (liens d'un contenu connu, sinon sa fiche, sinon un catalogue). Le code attendu et le mot-clé
// ne s'appliquent pas à ce mode.
func (r *Runner) probeStremio(ctx context.Context, c store.Check) store.CheckResult {
	res := store.CheckResult{CheckID: c.ID, TS: time.Now().UnixMilli()}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutMS)*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := r.stremioProbe(ctx, c, &res); err != nil {
		res.Error = err.Error()
	} else {
		res.OK = true
	}
	res.LatencyMS = time.Since(start).Milliseconds()
	return res
}

type stremioCatalog struct {
	Type          string   `json:"type"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ExtraRequired []string `json:"extraRequired"`
	Extra         []struct {
		IsRequired bool `json:"isRequired"`
	} `json:"extra"`
}

// browsable : un catalogue consultable sans paramètre (pas une recherche).
func (c stremioCatalog) browsable() bool {
	if len(c.ExtraRequired) > 0 {
		return false
	}
	for _, e := range c.Extra {
		if e.IsRequired {
			return false
		}
	}
	return true
}

type stremioManifest struct {
	Types         []string          `json:"types"`
	IDPrefixes    []string          `json:"idPrefixes"`
	Resources     []json.RawMessage `json:"resources"`
	Catalogs      []stremioCatalog  `json:"catalogs"`
	BehaviorHints struct {
		ConfigurationRequired bool `json:"configurationRequired"`
	} `json:"behaviorHints"`
}

// stremioResource : types et préfixes d'identifiants gérés par une ressource (vides : tous).
type stremioResource struct{ types, prefixes []string }

// resources indexe les ressources par nom ; une ressource écrite en simple chaîne hérite des types
// et préfixes du manifest.
func (m stremioManifest) resources() map[string]stremioResource {
	out := map[string]stremioResource{}
	for _, raw := range m.Resources {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			out[name] = stremioResource{m.Types, m.IDPrefixes}
			continue
		}
		var obj struct {
			Name       string   `json:"name"`
			Types      []string `json:"types"`
			IDPrefixes []string `json:"idPrefixes"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
			res := stremioResource{obj.Types, obj.IDPrefixes}
			if res.types == nil {
				res.types = m.Types
			}
			if res.prefixes == nil {
				res.prefixes = m.IDPrefixes
			}
			out[obj.Name] = res
		}
	}
	return out
}

func (s stremioResource) handles(kind, id string) bool {
	typeOK := len(s.types) == 0 || slices.Contains(s.types, kind)
	prefixOK := len(s.prefixes) == 0
	for _, p := range s.prefixes {
		prefixOK = prefixOK || strings.HasPrefix(id, p)
	}
	return typeOK && prefixOK
}

// stremioProbe renseigne le code HTTP, le certificat, le test effectué et le nombre de liens trouvés.
func (r *Runner) stremioProbe(ctx context.Context, c store.Check, res *store.CheckResult) error {
	var m stremioManifest
	resp, err := r.fetchJSON(ctx, c.URL, "manifest", 2<<20, &m)
	if resp != nil {
		res.Status = resp.StatusCode
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			res.CertExpires = resp.TLS.PeerCertificates[0].NotAfter.UnixMilli()
		}
	}
	if err != nil {
		return err
	}
	if m.BehaviorHints.ConfigurationRequired {
		return errors.New("addon non configuré : utilise l'URL du manifest installée dans Stremio (avec ta configuration)")
	}
	kind, id := "movie", c.StremioID
	if strings.Contains(id, ":") {
		kind = "series"
	}
	resources := m.resources()
	if s, ok := resources["stream"]; ok && s.handles(kind, id) {
		res.Probe = "stream"
		return r.stremioStreams(ctx, c, kind, id, res)
	}
	// Addon de métadonnées : la fiche du contenu (sans saison ni épisode).
	metaID, _, _ := strings.Cut(id, ":")
	if s, ok := resources["meta"]; ok && s.handles(kind, metaID) {
		res.Probe = "meta"
		var out struct {
			Meta *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"meta"`
		}
		if err := r.fetchResource(ctx, addonURL(c.URL, "meta", kind, metaID), "fiche", 4<<20, &out, res); err != nil {
			return err
		}
		if out.Meta == nil || out.Meta.ID == "" && out.Meta.Name == "" {
			return errors.New("aucune fiche pour " + metaID)
		}
		return nil
	}
	if _, ok := resources["catalog"]; ok {
		for _, cat := range m.Catalogs {
			if !cat.browsable() {
				continue
			}
			res.Probe = "catalog"
			var out struct {
				Metas []json.RawMessage `json:"metas"`
			}
			if err := r.fetchResource(ctx, addonURL(c.URL, "catalog", cat.Type, cat.ID), "catalogue", 8<<20, &out, res); err != nil {
				return err
			}
			if len(out.Metas) == 0 {
				return fmt.Errorf("catalogue « %s » vide", cmp.Or(cat.Name, cat.ID))
			}
			return nil
		}
	}
	// Ni liens, ni fiches, ni catalogue consultable : un manifest valide suffit.
	res.Probe = "manifest"
	return nil
}

// fetchResource lit une ressource de l'addon et retient son code HTTP.
func (r *Runner) fetchResource(ctx context.Context, u, what string, limit int64, out any, res *store.CheckResult) error {
	resp, err := r.fetchJSON(ctx, u, what, limit, out)
	if resp != nil {
		res.Status = resp.StatusCode
	}
	return err
}

// stremioStreams demande les liens du contenu de test.
func (r *Runner) stremioStreams(ctx context.Context, c store.Check, kind, id string, res *store.CheckResult) error {
	var out struct {
		Streams []struct {
			URL         string `json:"url"`
			InfoHash    string `json:"infoHash"`
			YtID        string `json:"ytId"`
			NzbURL      string `json:"nzbUrl"`
			Name        string `json:"name"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"streams"`
	}
	if err := r.fetchResource(ctx, addonURL(c.URL, "stream", kind, id), "liens", 8<<20, &out, res); err != nil {
		return err
	}
	// Seules les entrées lisibles comptent (les addons signalent parfois leurs erreurs par une fausse entrée).
	for _, s := range out.Streams {
		if s.URL != "" || s.InfoHash != "" || s.YtID != "" || s.NzbURL != "" {
			res.Links++
		}
	}
	if res.Links > 0 {
		return nil
	}
	msg := "aucun lien pour " + id
	if len(out.Streams) > 0 {
		// La fausse entrée porte en général l'explication de l'addon (debrid, configuration…).
		s := out.Streams[0]
		if hint := oneLine(cmp.Or(s.Description, s.Title, s.Name), 150); hint != "" {
			msg += " (l'addon indique : « " + hint + " »)"
		}
	}
	return errors.New(msg)
}

// oneLine réduit un texte à une seule ligne d'au plus n caractères.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
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
		d := describeBody(resp.Header.Get("Content-Type"), body)
		if what == "manifest" && strings.HasPrefix(d, "page HTML") {
			return resp, errors.New("ce n'est pas le manifest de l'addon (" + d + ") : vérifie l'URL, celle installée dans Stremio")
		}
		msg := "réponse JSON invalide (" + what + ")"
		if !strings.HasPrefix(d, "JSON") {
			msg += " : " + d
		}
		return resp, errors.New(msg)
	}
	return resp, nil
}
