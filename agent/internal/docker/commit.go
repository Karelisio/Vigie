package docker

// Création d'une image à partir d'un conteneur (`docker commit`), utilisée par la correction des
// failles : les paquets mis à niveau dans un conteneur temporaire deviennent une image dérivée.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// Commit crée une image, sans référence, à partir du conteneur id (arrêté). config est la configuration
// de la nouvelle image : Docker complète ses champs vides avec ceux du conteneur, d'où l'importance
// de la commande du conteneur temporaire (voir le paquet vulnfix). Renvoie l'ID de l'image (sha256:…).
func (c *Client) Commit(ctx context.Context, id, comment, author string, config map[string]any) (string, error) {
	q := url.Values{"container": {id}, "pause": {"false"}}
	if comment != "" {
		q.Set("comment", comment)
	}
	if author != "" {
		q.Set("author", author)
	}
	var out struct {
		ID string `json:"Id"`
	}
	// Flux sans délai côté client : l'écriture de la couche peut être longue (pilote vfs).
	resp, err := c.call(ctx, c.stream, http.MethodPost, "/commit", q, config)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}
