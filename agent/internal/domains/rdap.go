package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/secret"
)

const (
	bootstrapKey   = "rdap_bootstrap"
	bootstrapTTL   = 7 * 24 * time.Hour
	requestTimeout = 15 * time.Second
	maxBody        = 1 << 20 // réponse RDAP d'un domaine
	maxBootstrap   = 2 << 20 // fichier d'amorçage IANA (~ 100 Ko)
	maxStatus      = 12
)

var errTooManyRedirects = errors.New("trop de redirections")

// bootstrapCache : serveur RDAP de chaque TLD, d'après l'amorçage IANA, conservé 7 jours en base.
type bootstrapCache struct {
	Fetched int64             `json:"fetched"`
	Bases   map[string]string `json:"bases"` // TLD → URL de base, terminée par « / »
}

// bootstrap renvoie la table TLD → serveur RDAP : celle en base si elle a moins de 7 jours, sinon
// celle de l'IANA (ou l'ancienne si l'IANA est injoignable). Table vide : tout passe par le service de repli.
func (m *Manager) bootstrap(ctx context.Context) map[string]string {
	var cached bootstrapCache
	_ = json.Unmarshal([]byte(m.st.Get(ctx, bootstrapKey)), &cached)
	if len(cached.Bases) > 0 && time.Since(time.UnixMilli(cached.Fetched)) < bootstrapTTL {
		return cached.Bases
	}
	bases, err := m.fetchBootstrap(ctx)
	if err != nil {
		m.log.Warn("amorçage RDAP de l'IANA", "erreur", err)
		return cached.Bases
	}
	raw, _ := json.Marshal(bootstrapCache{Fetched: time.Now().UnixMilli(), Bases: bases})
	if err := m.st.Set(ctx, bootstrapKey, string(raw)); err != nil {
		m.log.Warn("amorçage RDAP", "erreur", err)
	}
	return bases
}

func (m *Manager) fetchBootstrap(ctx context.Context) (map[string]string, error) {
	var doc struct {
		Services [][][]string `json:"services"`
	}
	if status, err := m.getJSON(ctx, m.opt.BootstrapURL, maxBootstrap, &doc); err != nil {
		return nil, err
	} else if status != http.StatusOK {
		return nil, fmt.Errorf("code %d", status)
	}
	bases := map[string]string{}
	for _, svc := range doc.Services {
		if len(svc) < 2 {
			continue
		}
		base := ""
		for _, u := range svc[1] {
			if strings.HasPrefix(u, "https://") {
				base = u
				break
			}
		}
		if base == "" {
			continue // seul HTTPS est accepté
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		for _, tld := range svc[0] {
			bases[strings.ToLower(strings.TrimSpace(tld))] = base
		}
	}
	if len(bases) == 0 {
		return nil, errors.New("fichier d'amorçage vide")
	}
	return bases, nil
}

// serverFor renvoie l'adresse RDAP du domaine : serveur du TLD le plus long connu, sinon service de repli.
func (m *Manager) serverFor(domain string, bases map[string]string) string {
	labels := strings.Split(domain, ".")
	for i := 1; i < len(labels); i++ {
		if base, ok := bases[strings.Join(labels[i:], ".")]; ok {
			return base + "domain/" + domain
		}
	}
	return m.opt.FallbackURL + domain
}

// lookup interroge le registre et renvoie le résultat (Error renseigné en cas d'échec).
func (m *Manager) lookup(ctx context.Context, domain string, bases map[string]string) result {
	r := result{CheckedAt: time.Now().UnixMilli()}
	var doc rdapDomain
	status, err := m.getJSON(ctx, m.serverFor(domain, bases), maxBody, &doc)
	switch {
	case err != nil:
		r.Error = err.Error()
	case status == http.StatusNotFound:
		r.Error = "domaine introuvable chez le registre"
	case status == http.StatusTooManyRequests:
		r.Error = "le registre limite les requêtes, nouvel essai plus tard"
	case status != http.StatusOK:
		r.Error = fmt.Sprintf("le registre répond par une erreur (code %d)", status)
	case doc.ObjectClassName != "" && !strings.EqualFold(doc.ObjectClassName, "domain"):
		r.Error = "réponse RDAP inattendue"
	default:
		doc.fill(&r)
	}
	return r
}

// getJSON lit une réponse JSON bornée en taille ; une réponse en erreur n'est décodée que si possible.
func (m *Manager) getJSON(ctx context.Context, u string, limit int64, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, errors.New("adresse RDAP invalide")
	}
	req.Header.Set("Accept", "application/rdap+json, application/json")
	req.Header.Set("User-Agent", "Vigie/"+m.opt.Version+" (+https://github.com/Karelisio/Vigie)")
	resp, err := m.http.Do(req)
	if err != nil {
		if errors.Is(err, errTooManyRedirects) {
			return 0, errTooManyRedirects
		}
		return 0, errors.New("registre injoignable : " + secret.Describe(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return 0, errors.New("réponse du registre interrompue : " + secret.Describe(err))
	}
	if int64(len(raw)) > limit {
		return 0, errors.New("réponse du registre trop volumineuse")
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, out); err != nil {
			return 0, errors.New("réponse du registre illisible")
		}
	}
	return resp.StatusCode, nil
}

// ---- Réponse RDAP (RFC 9083) ----

type rdapDomain struct {
	ObjectClassName string       `json:"objectClassName"`
	Status          []string     `json:"status"`
	Events          []rdapEvent  `json:"events"`
	Entities        []rdapEntity `json:"entities"`
}

type rdapEvent struct {
	Action string `json:"eventAction"`
	Date   string `json:"eventDate"`
}

type rdapEntity struct {
	Roles    []string        `json:"roles"`
	VCard    json.RawMessage `json:"vcardArray"`
	Entities []rdapEntity    `json:"entities"`
}

func (d rdapDomain) fill(r *result) {
	for _, ev := range d.Events {
		t, ok := parseDate(ev.Date)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(ev.Action)) {
		case "expiration":
			if r.Expires == 0 {
				r.Expires = t.UnixMilli()
			}
		case "registration":
			if r.Registered == 0 {
				r.Registered = t.UnixMilli()
			}
		case "last changed":
			if r.LastChanged == 0 {
				r.LastChanged = t.UnixMilli()
			}
		}
	}
	r.Registrar = registrar(d.Entities, 0)
	for _, s := range d.Status {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && len(s) <= 64 && len(r.Status) < maxStatus && !contains(r.Status, s) {
			r.Status = append(r.Status, s)
		}
	}
}

// registrar renvoie le nom du bureau d'enregistrement (entité de rôle « registrar », parfois imbriquée).
func registrar(list []rdapEntity, depth int) string {
	if depth > 3 {
		return ""
	}
	for _, e := range list {
		for _, role := range e.Roles {
			if strings.EqualFold(role, "registrar") {
				if name := vcardName(e.VCard); name != "" {
					return name
				}
			}
		}
	}
	for _, e := range list {
		if name := registrar(e.Entities, depth+1); name != "" {
			return name
		}
	}
	return ""
}

// vcardName lit « fn » (sinon « org ») d'une vCard jCard : ["vcard", [["fn", {}, "text", "Nom"], …]].
func vcardName(raw json.RawMessage) string {
	var card []json.RawMessage
	if json.Unmarshal(raw, &card) != nil || len(card) < 2 {
		return ""
	}
	var props [][]json.RawMessage
	if json.Unmarshal(card[1], &props) != nil {
		return ""
	}
	found := map[string]string{}
	for _, p := range props {
		if len(p) < 4 {
			continue
		}
		var name, value string
		if json.Unmarshal(p[0], &name) != nil || json.Unmarshal(p[3], &value) != nil {
			continue
		}
		if value = strings.TrimSpace(value); value != "" && len(value) <= 120 {
			found[strings.ToLower(name)] = value
		}
	}
	if found["fn"] != "" {
		return found["fn"]
	}
	return found["org"]
}

// parseDate lit les dates RDAP, RFC 3339 en principe, mais aussi sans fuseau (UTC), avec un fuseau
// sans deux-points, avec une espace au lieu de « T », ou réduites au jour.
func parseDate(v string) (time.Time, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05Z0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, v); err == nil && t.Year() > 1970 {
			return t, true
		}
	}
	return time.Time{}, false
}
