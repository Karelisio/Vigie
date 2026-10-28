package docker

import (
	"context"
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
