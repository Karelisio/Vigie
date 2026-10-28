package images

import "strings"

// RepoDigest renvoie le digest (sha256:…) publié pour le dépôt de ref parmi les RepoDigests d'une
// image locale, "" si aucun ne correspond (image construite localement ou d'un autre dépôt).
func RepoDigest(ref string, repoDigests []string) string {
	r, ok := Parse(ref)
	if !ok {
		return ""
	}
	for _, rd := range repoDigests {
		for _, p := range r.digestPrefix() {
			if d, found := strings.CutPrefix(rd, p); found && strings.HasPrefix(d, "sha256:") {
				return d
			}
		}
	}
	return ""
}
