package docker

// Opérations utilisées par le retour à la version précédente d'un conteneur : téléchargement
// d'une image par son digest et pose d'une référence (dépôt:tag) sur une image locale.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PullDigest télécharge l'image désignée par « dépôt@sha256:… », comme `docker pull`.
func (c *Client) PullDigest(ctx context.Context, ref string, fn func(PullEvent)) error {
	repo, digest, ok := strings.Cut(ref, "@")
	if !ok || repo == "" || !strings.HasPrefix(digest, "sha256:") {
		return fmt.Errorf("référence par digest invalide : %q", ref)
	}
	return c.pull(ctx, url.Values{"fromImage": {repo}, "tag": {digest}}, fn)
}

// Tag pose la référence ref (dépôt:tag) sur l'image locale image (ID ou référence).
func (c *Client) Tag(ctx context.Context, image, ref string) error {
	repo, tag := SplitRef(ref)
	if tag == "" {
		return fmt.Errorf("référence sans tag : %q", ref)
	}
	return c.do(ctx, http.MethodPost, "/images/"+image+"/tag", url.Values{"repo": {repo}, "tag": {tag}}, nil, nil)
}
