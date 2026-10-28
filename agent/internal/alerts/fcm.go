package alerts

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FCM envoie des messages via l'API HTTP v1 de Firebase Cloud Messaging,
// authentifiée par un compte de service (JWT RS256 → jeton OAuth2).
type FCM struct {
	projectID   string
	clientEmail string
	keyID       string
	tokenURI    string
	key         *rsa.PrivateKey
	http        *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

// ErrUnregistered signale un jeton d'appareil invalide ou expiré.
var ErrUnregistered = errors.New("jeton FCM invalide")

func NewFCM(serviceAccount []byte) (*FCM, error) {
	var sa struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		TokenURI     string `json:"token_uri"`
	}
	if err := json.Unmarshal(serviceAccount, &sa); err != nil {
		return nil, fmt.Errorf("compte de service FCM illisible : %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("compte de service FCM incomplet (project_id, client_email, private_key)")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("clé privée FCM invalide")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if k, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
			parsed = k
		} else {
			return nil, fmt.Errorf("clé privée FCM : %w", err)
		}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("clé privée FCM : RSA attendue")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &FCM{
		projectID:   sa.ProjectID,
		clientEmail: sa.ClientEmail,
		keyID:       sa.PrivateKeyID,
		tokenURI:    sa.TokenURI,
		key:         key,
		http:        &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (f *FCM) ProjectID() string { return f.projectID }

func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Until(f.exp) > time.Minute {
		return f.token, nil
	}
	now := time.Now()
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": f.keyID})
	claims, _ := json.Marshal(map[string]any{
		"iss":   f.clientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   f.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {unsigned + "." + enc.EncodeToString(sig)},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", fmt.Errorf("jeton OAuth2 FCM refusé (%d) : %s", resp.StatusCode, out.Error)
	}
	f.token = out.AccessToken
	f.exp = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	return f.token, nil
}

// Send envoie un message de données à priorité haute vers un appareil.
func (f *FCM) Send(ctx context.Context, device string, data map[string]string, collapse string) error {
	tok, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	android := map[string]any{"priority": "HIGH", "ttl": "86400s"}
	if collapse != "" {
		android["collapse_key"] = collapse
	}
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{
			"token":   device,
			"data":    data,
			"android": android,
		},
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://fcm.googleapis.com/v1/projects/"+url.PathEscape(f.projectID)+"/messages:send", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := string(raw)
	if resp.StatusCode == http.StatusNotFound || strings.Contains(msg, "UNREGISTERED") ||
		(resp.StatusCode == http.StatusBadRequest && strings.Contains(msg, "registration token")) {
		return ErrUnregistered
	}
	if resp.StatusCode == http.StatusUnauthorized {
		f.mu.Lock()
		f.token = ""
		f.mu.Unlock()
	}
	return fmt.Errorf("FCM %d : %s", resp.StatusCode, strings.TrimSpace(msg))
}
