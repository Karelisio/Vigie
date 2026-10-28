package cleanup

import "github.com/karelisio/vigie/agent/internal/docker"

// Images gardées pour revenir à une version précédente : sans conteneur, elles font partie des
// « images inutilisées » et le nettoyage les supprime. Elles ne sont pas protégées (le nettoyage
// garde sa règle simple : toute image sans conteneur) ; l'estimation les compte à part pour que
// l'utilisateur le sache, et le retour arrière les re-télécharge par leur digest au besoin.

// Among compte, parmi les images que le nettoyage supprimerait, celles de ids.
func Among(du *docker.DiskUsage, ids []string) Category {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var c Category
	for _, img := range du.Images {
		if img.Containers != 0 || !want[img.ID] {
			continue
		}
		size := img.Size
		if img.SharedSize > 0 && img.SharedSize < size {
			size -= img.SharedSize
		}
		c.Count++
		c.Size += size
		delete(want, img.ID)
	}
	return c
}
