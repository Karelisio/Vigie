package regression

import "context"

// Forget abandonne la comparaison prévue pour un conteneur, revenu à sa version précédente : sa
// consommation depuis la mise à jour ne dit plus rien de la version en service.
func (w *Watcher) Forget(ctx context.Context, name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.load(ctx)
	var kept []pending
	for _, p := range list {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	if len(kept) != len(list) {
		w.save(ctx, kept)
	}
}
