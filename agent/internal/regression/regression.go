// Package regression compare la consommation d'un conteneur avant et après sa mise à jour, et prévient
// quand la nouvelle version consomme nettement plus (fuite de mémoire, boucle…).
package regression

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/cleanup"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
)

const (
	pendingKey = "regression_pending" // comparaisons à venir, conservées en base
	before     = 24 * time.Hour       // référence : les 24 h précédant la mise à jour
	warmup     = 15 * time.Minute     // le démarrage de la nouvelle version n'est pas compté
	after      = 6 * time.Hour        // comparaison 6 h après la mise à jour
	minMinutes = 60                   // mesures nécessaires de part et d'autre
)

// Seuils : nettement plus, et assez pour compter.
const (
	memRatio = 1.5
	memDelta = 100 << 20 // octets
	cpuRatio = 2.0
	cpuDelta = 5.0 // points de CPU de l'hôte
)

type pending struct {
	Name string  `json:"name"`
	At   int64   `json:"at"` // fin de la mise à jour (ms)
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
}

// Watcher mémorise la consommation des conteneurs mis à jour et les compare quelques heures plus tard.
type Watcher struct {
	st       *store.Store
	settings func() settings.Settings
	notify   func(name, title, body string)
	log      *slog.Logger
	mu       sync.Mutex
}

func New(st *store.Store, set func() settings.Settings, notify func(name, title, body string), log *slog.Logger) *Watcher {
	return &Watcher{st: st, settings: set, notify: notify, log: log}
}

func (w *Watcher) load(ctx context.Context) []pending {
	var list []pending
	_ = json.Unmarshal([]byte(w.st.Get(ctx, pendingKey)), &list)
	return list
}

func (w *Watcher) save(ctx context.Context, list []pending) {
	b, _ := json.Marshal(list)
	if err := w.st.Set(ctx, pendingKey, string(b)); err != nil {
		w.log.Warn("comparaison après mise à jour", "erreur", err)
	}
}

// OnUpdated retient la consommation des 24 h précédentes de chaque conteneur mis à jour.
func (w *Watcher) OnUpdated(ctx context.Context, names []string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.load(ctx)
	for _, name := range names {
		cpu, mem, n, err := w.st.CtrAverage(ctx, name, at.Add(-before), at)
		if err != nil || n < minMinutes {
			continue // pas assez d'historique pour comparer
		}
		kept := list[:0]
		for _, p := range list {
			if p.Name != name {
				kept = append(kept, p)
			}
		}
		list = append(kept, pending{Name: name, At: at.UnixMilli(), CPU: cpu, Mem: mem})
	}
	w.save(ctx, list)
}

// Run vérifie toutes les 10 minutes les comparaisons arrivées à échéance.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.Tick(ctx, now)
		}
	}
}

// Tick compare les conteneurs mis à jour depuis au moins 6 h, puis les oublie.
func (w *Watcher) Tick(ctx context.Context, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.load(ctx)
	if len(list) == 0 {
		return
	}
	var kept []pending
	for _, p := range list {
		at := time.UnixMilli(p.At)
		if now.Before(at.Add(after)) {
			kept = append(kept, p)
			continue
		}
		cpu, mem, n, err := w.st.CtrAverage(ctx, p.Name, at.Add(warmup), now)
		if err != nil || n < minMinutes || !w.settings().RegressionAlert {
			continue
		}
		if body, worse := compare(p.CPU, p.Mem, cpu, mem); worse {
			w.notify(p.Name, p.Name+" consomme plus depuis sa mise à jour", body)
		}
	}
	w.save(ctx, kept)
}

// compare renvoie le détail des hausses notables (moyennes avant / après).
func compare(cpuBefore, memBefore, cpuAfter, memAfter float64) (string, bool) {
	var parts []string
	if memAfter > memBefore*memRatio && memAfter-memBefore > memDelta {
		parts = append(parts, fmt.Sprintf("RAM : %s → %s", cleanup.HumanBytes(int64(memBefore)), cleanup.HumanBytes(int64(memAfter))))
	}
	if cpuAfter > cpuBefore*cpuRatio && cpuAfter-cpuBefore > cpuDelta {
		parts = append(parts, "CPU : "+pct(cpuBefore)+" → "+pct(cpuAfter))
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, " · ") + " (moyennes avant et depuis la mise à jour). Si ça persiste, vérifie ses logs ou reviens à l'image précédente dans Dockhand.", true
}

func pct(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f %%", v), ".", ",", 1)
}
