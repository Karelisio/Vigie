// Package config lit la configuration de l'agent depuis les variables d'environnement.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen     string
	DataDir    string
	Token      string
	PublicURL  string
	ServerName string

	HostProc string
	HostSys  string
	HostRoot string

	DockerHost string
	TrustProxy bool

	RateLimit float64
	RateBurst int

	AlertCPU          float64
	AlertRAM          float64
	AlertDisk         float64
	AlertDuration     time.Duration
	AlertCooldown     time.Duration
	RestartLoopCount  int
	RestartLoopWindow time.Duration
	CheckFailures     int

	FCMServiceAccount []byte

	ChecksSeed string
	ShowQR     bool
	// AllowUpdates autorise le téléchargement d'images et la recréation des conteneurs depuis l'appli.
	AllowUpdates bool
	QRInvert     bool
	Ignore       map[string]bool

	// FirstStart vaut true quand le token vient d'être généré (affichage du QR).
	FirstStart bool
	// TokenFromEnv indique que le token vient de VIGIE_TOKEN.
	TokenFromEnv bool
}

const minTokenLen = 32

func Load() (*Config, error) {
	c := &Config{
		Listen:            env("VIGIE_LISTEN", ":8080"),
		DataDir:           env("VIGIE_DATA_DIR", "/data"),
		PublicURL:         strings.TrimRight(env("VIGIE_PUBLIC_URL", ""), "/"),
		ServerName:        env("VIGIE_SERVER_NAME", ""),
		HostProc:          env("VIGIE_HOST_PROC", "/host/proc"),
		HostSys:           env("VIGIE_HOST_SYS", "/host/sys"),
		HostRoot:          env("VIGIE_HOST_ROOT", "/host/root"),
		DockerHost:        env("DOCKER_HOST", "unix:///var/run/docker.sock"),
		TrustProxy:        envBool("VIGIE_TRUST_PROXY", true),
		RateLimit:         envFloat("VIGIE_RATE_LIMIT", 10),
		RateBurst:         int(envFloat("VIGIE_RATE_BURST", 60)),
		AlertCPU:          envFloat("VIGIE_ALERT_CPU", 90),
		AlertRAM:          envFloat("VIGIE_ALERT_RAM", 90),
		AlertDisk:         envFloat("VIGIE_ALERT_DISK", 90),
		AlertDuration:     envDuration("VIGIE_ALERT_DURATION", 5*time.Minute),
		AlertCooldown:     envDuration("VIGIE_ALERT_COOLDOWN", 30*time.Minute),
		RestartLoopCount:  int(envFloat("VIGIE_RESTART_LOOP_COUNT", 3)),
		RestartLoopWindow: envDuration("VIGIE_RESTART_LOOP_WINDOW", 10*time.Minute),
		CheckFailures:     int(envFloat("VIGIE_CHECK_FAILURES", 2)),
		ChecksSeed:        os.Getenv("VIGIE_CHECKS"),
		ShowQR:            envBool("VIGIE_SHOW_QR", false),
		AllowUpdates:      envBool("VIGIE_ALLOW_UPDATES", true),
		QRInvert:          envBool("VIGIE_QR_INVERT", false),
		Ignore:            map[string]bool{},
	}
	for _, n := range strings.Split(os.Getenv("VIGIE_IGNORE"), ",") {
		if n = strings.TrimSpace(strings.TrimPrefix(n, "/")); n != "" {
			c.Ignore[n] = true
		}
	}
	if c.RestartLoopCount < 2 {
		c.RestartLoopCount = 2
	}
	if c.CheckFailures < 1 {
		c.CheckFailures = 1
	}

	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("dossier de données %s : %w", c.DataDir, err)
	}
	if err := c.loadToken(); err != nil {
		return nil, err
	}

	sa, err := loadServiceAccount()
	if err != nil {
		return nil, err
	}
	c.FCMServiceAccount = sa
	return c, nil
}

func (c *Config) loadToken() error {
	if t := strings.TrimSpace(os.Getenv("VIGIE_TOKEN")); t != "" {
		if len(t) < minTokenLen {
			return fmt.Errorf("VIGIE_TOKEN trop court (%d caractères, minimum %d)", len(t), minTokenLen)
		}
		c.Token = t
		c.TokenFromEnv = true
		// Premier démarrage avec ce token : on affiche le QR une fois.
		marker := filepath.Join(c.DataDir, "qr-shown")
		if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
			c.FirstStart = true
			_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0o600)
		}
		return nil
	}
	path := filepath.Join(c.DataDir, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= minTokenLen {
			c.Token = t
			return nil
		}
	}
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	c.Token = base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(c.Token+"\n"), 0o600); err != nil {
		return fmt.Errorf("écriture du token : %w", err)
	}
	c.FirstStart = true
	return nil
}

func loadServiceAccount() ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT")); raw != "" {
		return []byte(raw), nil
	}
	if b64 := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_B64")); b64 != "" {
		b64 = strings.Join(strings.Fields(b64), "")
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("FCM_SERVICE_ACCOUNT_B64 invalide : %w", err)
		}
		return b, nil
	}
	if p := os.Getenv("FCM_SERVICE_ACCOUNT_FILE"); p != "" {
		return os.ReadFile(p)
	}
	return nil, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	switch v {
	case "1", "true", "yes", "oui", "on":
		return true
	case "0", "false", "no", "non", "off":
		return false
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(k)), 64); err == nil {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(k))); err == nil && d > 0 {
		return d
	}
	return def
}
