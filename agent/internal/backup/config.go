package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Clés de la table kv. Les secrets sont rangés à part : ils ne sont jamais renvoyés à l'appli
// et sont retirés de la copie de la base envoyée au stockage.
const (
	configKey  = "backup_config"
	secretKey  = "backup_secret"
	historyKey = "backup_history"
	lastKey    = "backup_last" // jour (AAAA-MM-JJ) de la dernière sauvegarde planifiée

	// Mask remplace un secret enregistré dans les réponses ; renvoyé tel quel (ou vide), il le conserve.
	Mask = "••••••••"

	maxHistory = 30
	maxItems   = 50
)

// Selection est un élément choisi pour la sauvegarde.
type Selection struct {
	ID   string `json:"id"`   // « volume:<nom> », « bind:<chemin sur l'hôte> » ou « vigie »
	Stop bool   `json:"stop"` // arrêter les conteneurs qui l'utilisent pendant la copie
}

// Config est la configuration modifiable depuis l'appli.
type Config struct {
	Schedule  bool        `json:"schedule"`  // sauvegarde quotidienne
	Time      string      `json:"time"`      // HH:MM, heure locale (TZ)
	Retention int         `json:"retention"` // copies gardées par élément
	Endpoint  string      `json:"endpoint"`  // https://hôte (adresse « path-style »)
	Region    string      `json:"region"`
	Bucket    string      `json:"bucket"`
	Prefix    string      `json:"prefix"`
	AccessKey string      `json:"access_key"`
	SecretKey string      `json:"secret_key"`
	Items     []Selection `json:"items"`
}

type secrets struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func defaultConfig() Config {
	return Config{Time: "03:30", Retention: 7, Prefix: "vigie", Items: []Selection{}}
}

var (
	bucketRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
	regionRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

// normalize vérifie la configuration (hors secrets) et la met en forme.
func (c *Config) normalize() error {
	if _, err := time.Parse("15:04", c.Time); err != nil {
		return errors.New("heure invalide (HH:MM)")
	}
	if c.Retention < 1 || c.Retention > 365 {
		return errors.New("nombre de copies à garder hors limites (1–365)")
	}
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return errors.New("adresse du stockage invalide : https://hôte, sans chemin")
		}
	}
	c.Region = strings.TrimSpace(c.Region)
	if c.Region != "" && !regionRe.MatchString(c.Region) {
		return errors.New("région invalide")
	}
	c.Bucket = strings.TrimSpace(c.Bucket)
	if c.Bucket != "" && !bucketRe.MatchString(c.Bucket) {
		return errors.New("nom de bucket invalide (lettres, chiffres, « . », « _ », « - »)")
	}
	c.Prefix = strings.Trim(strings.TrimSpace(c.Prefix), "/")
	if c.Prefix != "" && (len(c.Prefix) > 200 || !prefixRe.MatchString(c.Prefix) || hasDotSegment(c.Prefix)) {
		return errors.New("préfixe invalide (lettres, chiffres, « . », « _ », « - », séparés par « / »)")
	}
	if len(c.Items) > maxItems {
		return fmt.Errorf("%d éléments au plus", maxItems)
	}
	seen := map[string]string{}
	for _, it := range c.Items {
		key, err := itemKey(it.ID)
		if err != nil {
			return err
		}
		if other, dup := seen[key]; dup {
			return fmt.Errorf("« %s » et « %s » iraient dans le même dossier du stockage", other, it.ID)
		}
		seen[key] = it.ID
	}
	return nil
}

func hasDotSegment(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return true
		}
	}
	return false
}

// storageReady indique ce qui manque pour joindre le stockage (ErrNotConfigured).
func (c Config) storageReady(s secrets) error {
	var missing []string
	for _, f := range []struct{ v, name string }{
		{c.Endpoint, "adresse"}, {c.Region, "région"}, {c.Bucket, "bucket"},
		{s.AccessKey, "clé d'accès"}, {s.SecretKey, "clé secrète"},
	} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w : %s", ErrNotConfigured, strings.Join(missing, ", "))
	}
	return nil
}

// ready indique si une sauvegarde peut partir.
func (c Config) ready(s secrets) error {
	if err := c.storageReady(s); err != nil {
		return err
	}
	if len(c.Items) == 0 {
		return errors.New("aucun élément à sauvegarder")
	}
	return nil
}

// merge applique les secrets reçus : une valeur vide ou masquée conserve le secret enregistré.
// Une nouvelle adresse de stockage exige de ressaisir la clé secrète, pour qu'elle ne serve
// jamais à signer des requêtes vers un autre serveur que celui pour lequel elle a été donnée.
func merge(in Config, saved Config, old secrets) (secrets, error) {
	s := old
	if v := strings.TrimSpace(in.AccessKey); v != "" && v != Mask {
		s.AccessKey = v
	}
	newSecret := strings.TrimSpace(in.SecretKey)
	if newSecret != "" && newSecret != Mask {
		s.SecretKey = newSecret
	} else if old.SecretKey != "" && in.Endpoint != saved.Endpoint {
		return s, errors.New("nouvelle adresse de stockage : saisis à nouveau la clé secrète")
	}
	if len(s.AccessKey) > 256 || len(s.SecretKey) > 256 {
		return s, errors.New("clé trop longue")
	}
	return s, nil
}

// masked renvoie la configuration telle que l'appli la voit : secrets remplacés par Mask.
func masked(c Config, s secrets) Config {
	c.AccessKey, c.SecretKey = "", ""
	if s.AccessKey != "" {
		c.AccessKey = Mask
	}
	if s.SecretKey != "" {
		c.SecretKey = Mask
	}
	return c
}

// ---- Persistance ----

func (s *Service) load(ctx context.Context) (Config, secrets) {
	c := defaultConfig()
	if raw := s.st.Get(ctx, configKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			s.log.Warn("sauvegardes : configuration illisible, valeurs par défaut", "erreur", err)
			c = defaultConfig()
		}
	}
	var sec secrets
	if raw := s.st.Get(ctx, secretKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &sec)
	}
	c.AccessKey, c.SecretKey = "", ""
	return c, sec
}

// Config renvoie la configuration, secrets masqués.
func (s *Service) Config(ctx context.Context) Config {
	c, sec := s.load(ctx)
	return masked(c, sec)
}

// Update valide et enregistre la configuration reçue de l'appli et renvoie la version masquée.
func (s *Service) Update(ctx context.Context, in Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := in.normalize(); err != nil {
		return Config{}, err
	}
	saved, old := s.load(ctx)
	sec, err := merge(in, saved, old)
	if err != nil {
		return Config{}, err
	}
	if in.Schedule {
		if err := in.ready(sec); err != nil {
			return Config{}, fmt.Errorf("sauvegarde quotidienne impossible : %w", err)
		}
	}
	plain := in
	plain.AccessKey, plain.SecretKey = "", ""
	if plain.Items == nil {
		plain.Items = []Selection{}
	}
	b, _ := json.Marshal(plain)
	if err := s.st.Set(ctx, configKey, string(b)); err != nil {
		return Config{}, err
	}
	if sec != old {
		b, _ := json.Marshal(sec)
		if err := s.st.Set(ctx, secretKey, string(b)); err != nil {
			return Config{}, err
		}
	}
	return masked(plain, sec), nil
}

// ---- Historique ----

// Record est le bilan d'une sauvegarde.
type Record struct {
	TS       int64        `json:"ts"`       // début (ms)
	Duration int64        `json:"duration"` // ms
	Size     int64        `json:"size"`     // octets envoyés (compressés)
	OK       bool         `json:"ok"`
	Error    string       `json:"error,omitempty"`
	Trigger  string       `json:"trigger"` // manual ou schedule
	Items    []ItemResult `json:"items"`
}

// ItemResult est le bilan d'un élément.
type ItemResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Key   string `json:"key,omitempty"` // objet créé dans le stockage
	Size  int64  `json:"size"`          // compressé
	Raw   int64  `json:"raw"`           // archive tar avant compression
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// History renvoie les dernières sauvegardes, la plus récente en premier.
func (s *Service) History(ctx context.Context) []Record {
	out := []Record{}
	if raw := s.st.Get(ctx, historyKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Service) addHistory(ctx context.Context, r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := append([]Record{r}, s.History(ctx)...)
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	b, _ := json.Marshal(list)
	if err := s.st.Set(ctx, historyKey, string(b)); err != nil {
		s.log.Warn("sauvegardes : historique non enregistré", "erreur", err)
	}
}

// lastRaw renvoie la taille avant compression de la dernière copie réussie d'un élément (0 si aucune).
func lastRaw(history []Record, id string) int64 {
	for _, r := range history {
		for _, it := range r.Items {
			if it.ID == id && it.OK {
				return it.Raw
			}
		}
	}
	return 0
}
