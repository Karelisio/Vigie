package sshwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/store"
)

// Pays des adresses : GeoJS (https://get.geojs.io), gratuit, sans clé ni compte, en HTTPS et par lots
// (plusieurs adresses séparées par des virgules dans une seule requête). Les réponses sont gardées
// 30 jours en base ; un pays inconnu est redemandé au plus une fois par jour.
const (
	DefaultGeoURL = "https://get.geojs.io/v1/ip/country.json"
	geoBatch      = 50               // adresses par requête
	geoGap        = 10 * time.Second // délai minimum entre deux requêtes (hors alerte de connexion)
	geoTTL        = 30 * 24 * time.Hour
	geoRetry      = 24 * time.Hour
	geoTimeout    = 8 * time.Second
	geoMaxBody    = 1 << 20
)

type geo struct {
	url    string // "" : désactivé
	client *http.Client
	st     *store.Store
	now    func() time.Time
	mu     sync.Mutex
	next   time.Time // prochaine requête autorisée
}

func newGeo(rawURL string, st *store.Store, now func() time.Time) *geo {
	switch strings.ToLower(strings.TrimSpace(rawURL)) {
	case "":
		rawURL = DefaultGeoURL
	case "off", "false", "0", "non", "no":
		rawURL = ""
	}
	return &geo{url: rawURL, client: &http.Client{Timeout: geoTimeout}, st: st, now: now}
}

func (g *geo) enabled() bool { return g.url != "" }

// publicIP indique si l'adresse est routable sur Internet (sinon : pas de pays).
func publicIP(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// cached renvoie les pays connus en base, sans requête.
func (g *geo) cached(ctx context.Context, ips []string) map[string]string {
	out, err := g.st.GeoCountries(ctx, ips, g.now(), geoTTL, geoRetry)
	if err != nil {
		return map[string]string{}
	}
	return out
}

// lookup renvoie les pays des adresses : cache, puis une requête au plus pour les adresses publiques
// manquantes (50 au plus). Sans urgent, la requête n'a lieu que si la précédente date de plus de 10 s.
// En cas d'échec, les pays manquants restent inconnus (rien n'est bloqué).
func (g *geo) lookup(ctx context.Context, ips []string, urgent bool) map[string]string {
	out := g.cached(ctx, ips)
	if !g.enabled() {
		return out
	}
	var missing []string
	seen := map[string]bool{}
	for _, ip := range ips {
		if _, ok := out[ip]; !ok && publicIP(ip) && !seen[ip] {
			seen[ip] = true
			missing = append(missing, ip)
		}
	}
	if len(missing) == 0 {
		return out
	}
	missing = missing[:min(len(missing), geoBatch)]
	g.mu.Lock()
	now := g.now()
	if now.Before(g.next) && !urgent {
		g.mu.Unlock()
		return out
	}
	g.next = now.Add(geoGap)
	g.mu.Unlock()
	got, err := g.fetch(ctx, missing)
	if err != nil {
		return out
	}
	_ = g.st.PutGeo(ctx, got, now)
	for ip, c := range got {
		out[ip] = c
	}
	return out
}

// fetch interroge le service : réponse en tableau (plusieurs adresses) ou en objet (une seule).
func (g *geo) fetch(ctx context.Context, ips []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, geoTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url+"?ip="+url.QueryEscape(strings.Join(ips, ",")), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "vigie-agent")
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service des pays : HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, geoMaxBody))
	if err != nil {
		return nil, err
	}
	type entry struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
	}
	var list []entry
	if err := json.Unmarshal(body, &list); err != nil {
		var one entry
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, fmt.Errorf("service des pays : réponse illisible")
		}
		list = []entry{one}
	}
	asked := map[string]bool{}
	for _, ip := range ips {
		asked[ip] = true
	}
	out := map[string]string{}
	for _, e := range list {
		ip := e.IP
		if a, err := netip.ParseAddr(ip); err == nil {
			ip = a.Unmap().String()
		}
		if !asked[ip] {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(e.Country))
		if !validCode(code) {
			code = ""
		}
		out[ip] = code
	}
	// Adresses absentes de la réponse : pays inconnu (redemandé plus tard).
	for _, ip := range ips {
		if _, ok := out[ip]; !ok {
			out[ip] = ""
		}
	}
	return out, nil
}

func validCode(c string) bool {
	return len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z'
}

// CountryName renvoie le nom français d'un pays (code ISO 3166-1), « Réseau privé » pour une adresse
// non routable, "" si inconnu.
func CountryName(code, ip string) string {
	if code == "" {
		if ip != "" && !publicIP(ip) {
			if _, err := netip.ParseAddr(ip); err == nil {
				return "Réseau privé"
			}
		}
		return ""
	}
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}
