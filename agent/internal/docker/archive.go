package docker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// Archive renvoie, en flux, une archive tar du chemin path dans le conteneur id (sauvegardes).
// Fonctionne aussi sur un conteneur arrêté : Docker monte alors ses volumes le temps de la copie.
// L'appelant ferme le flux.
func (c *Client) Archive(ctx context.Context, id, path string) (io.ReadCloser, error) {
	resp, err := c.call(ctx, c.stream, http.MethodGet, "/containers/"+url.PathEscape(id)+"/archive", url.Values{"path": {path}}, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// PutArchive extrait l'archive tar body dans le dossier dir du conteneur id, qui peut être créé
// sans avoir été démarré (restaurations : dépôt de la copie dans le conteneur auxiliaire).
func (c *Client) PutArchive(ctx context.Context, id, dir string, body io.Reader) error {
	u := c.base + "/containers/" + url.PathEscape(id) + "/archive?" + url.Values{"path": {dir}}.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/x-tar")
	resp, err := c.stream.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		return &APIError{Code: resp.StatusCode, Message: e.Message}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return nil
}

// RemoveWithVolumes supprime un conteneur, même en marche, avec ses volumes anonymes.
func (c *Client) RemoveWithVolumes(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), url.Values{"force": {"true"}, "v": {"true"}}, nil, nil)
}
