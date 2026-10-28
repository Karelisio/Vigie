package debrid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/karelisio/vigie/agent/internal/secret"
)

// get interroge l'API (jeton en Bearer s'il est fourni) et décode la réponse JSON dans out.
// Une réponse en erreur illisible n'est pas une erreur ici : son code HTTP suffit au fournisseur.
func (m *Manager) get(ctx context.Context, u, bearer string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, &checkError{msg: secret.Describe(err)}
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Vigie (+https://github.com/Karelisio/Vigie)")
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, &checkError{msg: secret.Describe(err), down: true}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, out); err != nil && resp.StatusCode/100 == 2 {
		return resp.StatusCode, &checkError{msg: fmt.Sprintf("réponse illisible (code %d)", resp.StatusCode)}
	}
	return resp.StatusCode, nil
}

// statusError décrit un code HTTP d'erreur ; 401 et 403 signifient une clé refusée.
func statusError(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return refused("clé API refusée")
	case status == http.StatusTooManyRequests:
		return &checkError{msg: "trop de requêtes, nouvel essai plus tard"}
	case status >= 500:
		return &checkError{msg: fmt.Sprintf("service indisponible (code %d)", status), down: true}
	}
	return &checkError{msg: fmt.Sprintf("réponse inattendue (code %d)", status)}
}

// parseTime lit une date d'expiration quelle que soit sa forme : secondes ou millisecondes Unix
// (nombre ou texte), date ISO 8601 avec ou sans fuseau ; false, null, 0 ou vide : pas de date.
func parseTime(raw json.RawMessage) time.Time {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return time.Time{}
	}
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case string:
		x = strings.TrimSpace(x)
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02"} {
				if t, err := time.Parse(layout, x); err == nil {
					return t
				}
			}
		}
		n = f
	}
	switch {
	case n <= 0:
		return time.Time{}
	case n > 1e12:
		return time.UnixMilli(int64(n))
	}
	return time.Unix(int64(n), 0)
}

// number lit un nombre écrit comme nombre, texte ou booléen (0 sinon).
func number(raw json.RawMessage) float64 {
	var v any
	_ = json.Unmarshal(raw, &v)
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	return 0
}

// realDebrid : GET /rest/1.0/user (Bearer). type « premium » ou « free », premium en secondes restantes,
// expiration en date ISO ; erreurs {error, error_code} (8 : jeton invalide).
func (m *Manager) realDebrid(ctx context.Context, key string) (info, error) {
	var r struct {
		Username   string          `json:"username"`
		Type       string          `json:"type"`
		Premium    json.RawMessage `json:"premium"`
		Expiration json.RawMessage `json:"expiration"`
		ErrorCode  int             `json:"error_code"`
	}
	status, err := m.get(ctx, m.bases[RealDebrid]+"/user", key, &r)
	switch {
	case err != nil:
		return info{}, err
	case status == http.StatusUnauthorized || r.ErrorCode == 8:
		return info{}, refused("clé API refusée (jeton invalide ou expiré)")
	case status == http.StatusForbidden:
		return info{}, refused("accès refusé (compte bloqué ou non activé)")
	case status != http.StatusOK:
		return info{}, statusError(status)
	}
	seconds := number(r.Premium)
	in := info{username: r.Username, premium: r.Type == "premium" || seconds > 0, expires: parseTime(r.Expiration)}
	if in.expires.IsZero() && seconds > 0 {
		in.expires = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return in, nil
}

// allDebrid : GET /v4/user?agent=vigie&apikey=… (la clé part aussi en Bearer, forme recommandée
// par les versions récentes de l'API). Réponse {status, data: {user: {isPremium, premiumUntil}}}
// ou {status: "error", error: {code, message}} ; les codes AUTH_* signalent une clé refusée.
func (m *Manager) allDebrid(ctx context.Context, key string) (info, error) {
	var r struct {
		Status string `json:"status"`
		Data   struct {
			User struct {
				Username     string          `json:"username"`
				IsPremium    json.RawMessage `json:"isPremium"`
				PremiumUntil json.RawMessage `json:"premiumUntil"`
			} `json:"user"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	u := m.bases[AllDebrid] + "/user?agent=vigie&apikey=" + url.QueryEscape(key)
	status, err := m.get(ctx, u, key, &r)
	switch {
	case err != nil:
		return info{}, err
	case r.Error.Code == "AUTH_BLOCKED":
		return info{}, refused("clé bloquée par AllDebrid : confirme la connexion depuis l'e-mail reçu")
	case r.Error.Code == "AUTH_USER_BANNED":
		return info{}, refused("compte banni par AllDebrid")
	case strings.HasPrefix(r.Error.Code, "AUTH_") || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return info{}, refused("clé API refusée")
	case r.Status != "success" && r.Error.Message != "" && status < 500:
		return info{}, &checkError{msg: "AllDebrid : " + r.Error.Message}
	case status != http.StatusOK || r.Status != "success":
		return info{}, statusError(status)
	}
	user := r.Data.User
	return info{username: user.Username, premium: number(user.IsPremium) > 0, expires: parseTime(user.PremiumUntil)}, nil
}

// torBox : GET /v1/api/user/me (Bearer). Réponse {success, error, detail, data: {email, plan, premium_expires_at}} ;
// plan 0 : gratuit. Erreurs BAD_TOKEN, AUTH_ERROR ou NO_AUTH : clé refusée.
func (m *Manager) torBox(ctx context.Context, key string) (info, error) {
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Detail  string `json:"detail"`
		Data    struct {
			Email            string          `json:"email"`
			Plan             json.RawMessage `json:"plan"`
			PremiumExpiresAt json.RawMessage `json:"premium_expires_at"`
		} `json:"data"`
	}
	status, err := m.get(ctx, m.bases[TorBox]+"/user/me", key, &r)
	switch {
	case err != nil:
		return info{}, err
	case r.Error == "BAD_TOKEN" || r.Error == "AUTH_ERROR" || r.Error == "NO_AUTH" ||
		status == http.StatusUnauthorized || status == http.StatusForbidden:
		return info{}, refused("clé API refusée")
	case status != http.StatusOK || !r.Success:
		if r.Detail != "" && status < 500 {
			return info{}, &checkError{msg: "TorBox : " + r.Detail}
		}
		return info{}, statusError(status)
	}
	return info{username: r.Data.Email, premium: number(r.Data.Plan) > 0, expires: parseTime(r.Data.PremiumExpiresAt)}, nil
}

// premiumize : GET /api/account/info?apikey=…. Réponse {status, premium_until} (secondes Unix, false ou null
// sans premium) ou {status: "error", message} quand la clé est refusée.
func (m *Manager) premiumize(ctx context.Context, key string) (info, error) {
	var r struct {
		Status       string          `json:"status"`
		PremiumUntil json.RawMessage `json:"premium_until"`
	}
	status, err := m.get(ctx, m.bases[Premiumize]+"/account/info?apikey="+url.QueryEscape(key), "", &r)
	switch {
	case err != nil:
		return info{}, err
	case r.Status == "error" && status < 500 && status != http.StatusTooManyRequests:
		return info{}, refused("clé API refusée")
	case status != http.StatusOK || r.Status != "success":
		return info{}, statusError(status)
	}
	until := parseTime(r.PremiumUntil)
	return info{premium: !until.IsZero(), expires: until}, nil
}
