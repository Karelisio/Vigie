// Package alerts détecte les incidents (conteneur tombé, redémarrages en boucle,
// seuils système, checks en échec) et envoie les notifications push.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/oracle"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

type Engine struct {
	cfg *config.Config
	mon *monitor.Monitor
	dk  *docker.Client
	st  *store.Store
	fcm *FCM
	log *slog.Logger
	ctx context.Context

	mu       sync.Mutex
	over     map[string]time.Time // métrique → début du dépassement
	active   map[string]bool      // clé d'alerte → en cours
	pushed   map[string]bool      // clé d'alerte → notification envoyée
	lastFire map[string]time.Time
	starts   map[string][]time.Time
	killed   map[string]time.Time
	set      *settings.Manager
	quiet    []string // titres des alertes reçues pendant les heures calmes
	maint    []string // titres des alertes reçues pendant la maintenance
}

func New(ctx context.Context, cfg *config.Config, mon *monitor.Monitor, dk *docker.Client, st *store.Store, log *slog.Logger) *Engine {
	e := &Engine{
		cfg: cfg, mon: mon, dk: dk, st: st, log: log, ctx: ctx,
		over:     map[string]time.Time{},
		active:   map[string]bool{},
		pushed:   map[string]bool{},
		lastFire: map[string]time.Time{},
		starts:   map[string][]time.Time{},
		killed:   map[string]time.Time{},
		set:      mon.Settings(),
	}
	if len(cfg.FCMServiceAccount) > 0 {
		f, err := NewFCM(cfg.FCMServiceAccount)
		if err != nil {
			log.Error("notifications push désactivées", "erreur", err)
		} else {
			e.fcm = f
			log.Info("notifications push activées", "projet_firebase", f.ProjectID())
		}
	} else {
		log.Info("notifications push désactivées (FCM_SERVICE_ACCOUNT_B64 absent)")
	}
	go e.loopWatcher()
	go e.periodic()
	return e
}

func (e *Engine) PushEnabled() bool { return e.fcm != nil }

// ---- Seuils système ----

func (e *Engine) OnSystem(s system.Snapshot) {
	set := e.set.Get()
	e.metric("cpu", "Processeur", s.CPU, set.CPU, set.AlertDuration())
	e.metric("ram", "Mémoire", s.MemPct, set.RAM, set.AlertDuration())
	e.metric("disk", "Disque", s.DiskPct, set.Disk, time.Minute)
}

func (e *Engine) metric(key, label string, v, limit float64, dur time.Duration) {
	akey := "system:" + key
	e.mu.Lock()
	switch {
	case v >= limit:
		since, ok := e.over[key]
		if !ok {
			e.over[key] = time.Now()
			e.mu.Unlock()
			return
		}
		if e.active[akey] || time.Since(since) < dur {
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		e.fire(akey, Notification{
			Kind: "system", Severity: "critical", Target: key,
			Title: fmt.Sprintf("%s à %.0f %%", label, v),
			Body:  fmt.Sprintf("Au-dessus du seuil de %.0f %% depuis %s.", limit, humanDuration(time.Since(since))),
		})
	case v < limit-5:
		delete(e.over, key)
		wasActive := e.active[akey]
		e.mu.Unlock()
		if wasActive {
			e.resolve(akey, Notification{
				Kind: "system", Severity: "info", Target: key,
				Title: fmt.Sprintf("%s revenu à la normale", label),
				Body:  fmt.Sprintf("%.0f %% actuellement.", v),
			})
		}
	default:
		e.mu.Unlock()
	}
}

// ---- Conteneurs ----

func (e *Engine) ignored(ev docker.Event) bool {
	return e.cfg.Ignore[ev.Name()] || strings.EqualFold(ev.Actor.Attributes["vigie.ignore"], "true") || e.set.Get().IsMuted(ev.Name())
}

func (e *Engine) OnEvent(ev docker.Event) {
	name := ev.Name()
	if name == "" || e.ignored(ev) {
		return
	}
	switch {
	case ev.Action == "kill" || ev.Action == "stop":
		e.mu.Lock()
		e.killed[name] = time.Now()
		e.mu.Unlock()

	case ev.Action == "die":
		e.mu.Lock()
		k, wasKilled := e.killed[name]
		e.mu.Unlock()
		if (wasKilled && time.Since(k) < 20*time.Second) || e.mon.RecentAction(name, time.Minute) {
			return
		}
		code, _ := strconv.Atoi(ev.Actor.Attributes["exitCode"])
		id := ev.Actor.ID
		go e.confirmDown(id, name, code)

	case ev.Action == "start":
		if e.mon.RecentAction(name, time.Minute) {
			e.resolveDown(name)
			return
		}
		e.mu.Lock()
		now := time.Now()
		list := append(e.starts[name], now)
		cut := 0
		for cut < len(list) && now.Sub(list[cut]) > e.cfg.RestartLoopWindow {
			cut++
		}
		list = list[cut:]
		e.starts[name] = list
		n := len(list)
		loopActive := e.active["loop:"+name]
		e.mu.Unlock()
		if n >= e.cfg.RestartLoopCount && !loopActive {
			e.fire("loop:"+name, Notification{
				Kind: "container", Severity: "critical", Target: name, ContainerID: ev.Actor.ID,
				Title: fmt.Sprintf("%s redémarre en boucle", name),
				Body:  fmt.Sprintf("%d redémarrages en %s.", n, humanDuration(e.cfg.RestartLoopWindow)),
			})
			return
		}
		e.resolveDown(name)

	case ev.Action == "oom":
		e.fire("oom:"+name, Notification{
			Kind: "container", Severity: "critical", Target: name, ContainerID: ev.Actor.ID,
			Title: fmt.Sprintf("%s manque de mémoire", name),
			Body:  "Le noyau a tué un processus du conteneur (OOM).",
		})
		go func() {
			time.Sleep(10 * time.Minute)
			e.mu.Lock()
			delete(e.active, "oom:"+name)
			e.mu.Unlock()
		}()

	case strings.HasPrefix(ev.Action, "health_status"):
		status := strings.TrimSpace(strings.TrimPrefix(ev.Action, "health_status:"))
		key := "health:" + name
		switch status {
		case "unhealthy":
			e.fire(key, Notification{
				Kind: "container", Severity: "critical", Target: name, ContainerID: ev.Actor.ID,
				Title: fmt.Sprintf("%s en mauvaise santé", name),
				Body:  "Son healthcheck échoue.",
			})
		case "healthy":
			// Ne signale un retour à la normale que si une alerte était en cours.
			e.mu.Lock()
			active := e.active[key]
			e.mu.Unlock()
			if !active {
				return
			}
			e.resolve(key, Notification{
				Kind: "container", Severity: "info", Target: name, ContainerID: ev.Actor.ID,
				Title: fmt.Sprintf("%s de nouveau en bonne santé", name),
			})
		}

	case ev.Action == "destroy":
		e.mu.Lock()
		delete(e.starts, name)
		delete(e.killed, name)
		e.mu.Unlock()
	}
}

// confirmDown vérifie, après un délai de grâce, que le conteneur est bien resté arrêté.
func (e *Engine) confirmDown(id, name string, code int) {
	select {
	case <-e.ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	in, err := e.dk.Inspect(e.ctx, id)
	if err != nil {
		return // supprimé ou recréé
	}
	if in.State.Running || in.State.Restarting || e.mon.RecentAction(name, 2*time.Minute) || e.mon.IsStoppedByUser(name) {
		return
	}
	if in.State.OOMKilled {
		e.fire("down:"+name, Notification{
			Kind: "container", Severity: "critical", Target: name, ContainerID: id,
			Title: fmt.Sprintf("%s est tombé", name),
			Body:  "Tué par manque de mémoire (OOM).",
		})
		return
	}
	switch code {
	case 0, 130, 137, 143:
		return
	}
	body := fmt.Sprintf("Arrêté avec le code %d.", code)
	if in.State.Error != "" {
		body += " " + in.State.Error
	}
	e.fire("down:"+name, Notification{
		Kind: "container", Severity: "critical", Target: name, ContainerID: id,
		Title: fmt.Sprintf("%s est tombé", name),
		Body:  body,
	})
}

func (e *Engine) resolveDown(name string) {
	e.mu.Lock()
	active := e.active["down:"+name]
	e.mu.Unlock()
	if active {
		e.resolve("down:"+name, Notification{
			Kind: "container", Severity: "info", Target: name,
			Title: fmt.Sprintf("%s est reparti", name),
		})
	}
}

// loopWatcher clôt les alertes de boucle une fois le conteneur stabilisé.
func (e *Engine) loopWatcher() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
		}
		var done []string
		e.mu.Lock()
		for name, list := range e.starts {
			if len(list) == 0 || time.Since(list[len(list)-1]) > e.cfg.RestartLoopWindow {
				delete(e.starts, name)
				if e.active["loop:"+name] {
					done = append(done, name)
				}
			}
		}
		for name, t := range e.killed {
			if time.Since(t) > time.Hour {
				delete(e.killed, name)
			}
		}
		e.mu.Unlock()
		for _, name := range done {
			if c, ok := e.mon.Find(name); ok && c.State == "running" {
				e.resolve("loop:"+name, Notification{
					Kind: "container", Severity: "info", Target: name, ContainerID: c.ID,
					Title: fmt.Sprintf("%s s'est stabilisé", name),
					Body:  "Plus de redémarrage depuis " + humanDuration(e.cfg.RestartLoopWindow) + ".",
				})
			} else {
				e.mu.Lock()
				delete(e.active, "loop:"+name)
				e.mu.Unlock()
			}
		}
	}
}

// ---- Checks HTTP ----

func (e *Engine) OnCheck(s checks.Status) {
	key := "check:" + strconv.FormatInt(s.ID, 10)
	e.mu.Lock()
	active := e.active[key]
	e.mu.Unlock()
	if !s.Enabled || s.Last == nil {
		return
	}
	e.checkCert(s)
	if !s.Last.OK && s.ConsecutiveFailures >= e.cfg.CheckFailures && !active {
		e.fire(key, Notification{
			Kind: "check", Severity: "critical", Target: s.Name, CheckID: s.ID,
			Title: fmt.Sprintf("%s ne répond plus", s.Name),
			Body:  s.Last.Error,
		})
	} else if s.Last.OK && active {
		e.resolve(key, Notification{
			Kind: "check", Severity: "info", Target: s.Name, CheckID: s.ID,
			Title: fmt.Sprintf("%s répond à nouveau", s.Name),
			Body:  fmt.Sprintf("Latence : %d ms.", s.Last.LatencyMS),
		})
	}
}

// ---- Envoi ----

type Notification struct {
	Kind        string
	Severity    string // critical, warning, info
	Target      string
	ContainerID string
	CheckID     int64
	Title       string
	Body        string
}

func (e *Engine) fire(key string, n Notification) {
	e.mu.Lock()
	e.active[key] = true
	push := time.Since(e.lastFire[key]) >= e.cfg.AlertCooldown
	if push {
		e.lastFire[key] = time.Now()
	}
	e.pushed[key] = push
	e.mu.Unlock()
	e.dispatch(n, false, push)
}

func (e *Engine) resolve(key string, n Notification) {
	e.mu.Lock()
	push := e.pushed[key]
	delete(e.active, key)
	delete(e.pushed, key)
	e.mu.Unlock()
	e.dispatch(n, true, push)
}

func (e *Engine) dispatch(n Notification, resolved, push bool) {
	a := &store.Alert{
		TS: time.Now().UnixMilli(), Kind: n.Kind, Severity: n.Severity, Target: n.Target,
		Title: n.Title, Body: n.Body, Resolved: resolved,
	}
	if err := e.st.AddAlert(e.ctx, a); err != nil {
		e.log.Warn("enregistrement de l'alerte", "erreur", err)
	}
	if push && e.set.Get().InMaintenance(time.Now()) {
		// Maintenance : rien n'est envoyé, un bilan suit la fin de la maintenance.
		push = false
		if !resolved {
			e.mu.Lock()
			if len(e.maint) < 50 {
				e.maint = append(e.maint, n.Title)
			}
			e.mu.Unlock()
		}
	}
	if push && e.set.Get().InQuietHours(time.Now()) {
		// Heures calmes : pas de notification, un résumé sera envoyé à la fin.
		push = false
		// Le bilan d'une opération réussie (mises à jour de la nuit) figure aussi dans le résumé.
		if !resolved || n.Kind == "job" {
			e.mu.Lock()
			e.quiet = append(e.quiet, n.Title)
			e.mu.Unlock()
		}
	}
	e.log.Info("alerte", "titre", n.Title, "détail", n.Body, "résolue", resolved, "push", push && e.fcm != nil)
	e.mon.Broadcast("alert", a)
	if !push || e.fcm == nil {
		return
	}
	data := map[string]string{
		"type":     "alert",
		"alert_id": strconv.FormatInt(a.ID, 10),
		"kind":     n.Kind,
		"severity": n.Severity,
		"target":   n.Target,
		"title":    n.Title,
		"body":     n.Body,
		"resolved": strconv.FormatBool(resolved),
		"server":   e.cfg.ServerName,
		"url":      e.cfg.PublicURL,
		"ts":       strconv.FormatInt(a.TS, 10),
	}
	if n.ContainerID != "" {
		data["container_id"] = n.ContainerID
	}
	if n.CheckID != 0 {
		data["check_id"] = strconv.FormatInt(n.CheckID, 10)
	}
	go e.pushAll(data, n.Kind+":"+n.Target)
}

func (e *Engine) pushAll(data map[string]string, collapse string) {
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	devices, err := e.st.Devices(ctx)
	if err != nil {
		e.log.Warn("liste des appareils", "erreur", err)
		return
	}
	for _, d := range devices {
		err := e.fcm.Send(ctx, d.Token, data, collapse)
		switch {
		case errors.Is(err, ErrUnregistered):
			e.log.Info("appareil désinscrit", "nom", d.Name)
			_ = e.st.DeleteDevice(ctx, d.Token)
		case err != nil:
			e.log.Warn("envoi FCM", "appareil", d.Name, "erreur", err)
		}
	}
}

// SendTest envoie une notification de test à un appareil (ou à tous si token est vide).
func (e *Engine) SendTest(ctx context.Context, token string) (int, error) {
	if e.fcm == nil {
		return 0, errors.New("notifications push non configurées sur l'agent (FCM_SERVICE_ACCOUNT_B64)")
	}
	data := map[string]string{
		"type": "test", "kind": "test", "severity": "info", "target": "test",
		"title": "Vigie est prête", "body": "Les alertes de " + e.cfg.ServerName + " arriveront ici.",
		"resolved": "false", "server": e.cfg.ServerName, "url": e.cfg.PublicURL,
		"ts": strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	var targets []string
	if token != "" {
		targets = []string{token}
	} else {
		devices, err := e.st.Devices(ctx)
		if err != nil {
			return 0, err
		}
		for _, d := range devices {
			targets = append(targets, d.Token)
		}
	}
	sent := 0
	var last error
	for _, t := range targets {
		if err := e.fcm.Send(ctx, t, data, ""); err != nil {
			last = err
			if errors.Is(err, ErrUnregistered) {
				_ = e.st.DeleteDevice(ctx, t)
			}
			continue
		}
		sent++
	}
	if sent == 0 && last != nil {
		return 0, last
	}
	return sent, nil
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h := int(d.Hours())
	if m := int(d.Minutes()) % 60; m > 0 {
		return fmt.Sprintf("%d h %02d", h, m)
	}
	return fmt.Sprintf("%d h", h)
}

// ---- Certificats HTTPS ----

func (e *Engine) checkCert(s checks.Status) {
	if s.CertExpires == 0 {
		return
	}
	key := "cert:" + strconv.FormatInt(s.ID, 10)
	limit := e.set.Get().CertDays
	left := time.Until(time.UnixMilli(s.CertExpires))
	days := int(left.Hours() / 24)
	e.mu.Lock()
	active := e.active[key]
	e.mu.Unlock()
	switch {
	case days < limit && !active:
		body := fmt.Sprintf("Il expire dans %d jour(s), le %s.", days, time.UnixMilli(s.CertExpires).Format("02/01/2006"))
		if left < 0 {
			body = "Il a expiré : les clients refusent la connexion."
		}
		e.fire(key, Notification{
			Kind: "check", Severity: "warning", Target: s.Name, CheckID: s.ID,
			Title: fmt.Sprintf("Certificat de %s bientôt expiré", s.Name),
			Body:  body,
		})
	case days >= limit && active:
		e.resolve(key, Notification{
			Kind: "check", Severity: "info", Target: s.Name, CheckID: s.ID,
			Title: fmt.Sprintf("Certificat de %s renouvelé", s.Name),
			Body:  fmt.Sprintf("Valide encore %d jours.", days),
		})
	}
}

// ---- Mises à jour d'images ----

func (e *Engine) OnImageUpdates(names []string) {
	if !e.set.Get().ImageUpdates {
		return
	}
	var list []string
	for _, n := range names {
		if !e.cfg.Ignore[n] && !e.set.Get().IsMuted(n) {
			list = append(list, n)
		}
	}
	if len(list) == 0 {
		return
	}
	title := "Mise à jour disponible : " + list[0]
	if len(list) > 1 {
		title = fmt.Sprintf("%d mises à jour d'images disponibles", len(list))
	}
	// Une notification par lot : clé unique, pas de résolution.
	e.dispatch(Notification{
		Kind: "image", Severity: "info", Target: strings.Join(list, ", "),
		Title: title,
		Body:  "Nouvelle version publiée pour " + strings.Join(list, ", ") + ". Mets à jour depuis l'appli.",
	}, false, true)
}

// NotifyJob signale la fin d'une mise à jour lancée depuis l'appli, si celle-ci est fermée
// (appli ouverte : elle affiche déjà le résultat en direct).
func (e *Engine) NotifyJob(title, body string, ok bool) {
	if e.mon.Subscribers() > 0 {
		return
	}
	sev := "critical"
	if ok {
		sev = "info"
	}
	e.dispatch(Notification{Kind: "job", Severity: sev, Target: title, Title: title, Body: body}, ok, true)
}

// ---- Tâches périodiques : résumé des heures calmes, risque Oracle ----

func (e *Engine) periodic() {
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	nextOracle := time.Now().Add(10 * time.Minute)
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-minute.C:
			e.flushMaintenance(now)
			e.flushQuiet(now)
			if now.After(nextOracle) {
				nextOracle = now.Add(time.Hour)
				e.evaluateOracle()
			}
		}
	}
}

// flushMaintenance envoie le bilan des alertes survenues pendant la maintenance, une fois celle-ci finie.
func (e *Engine) flushMaintenance(now time.Time) {
	if e.set.Get().InMaintenance(now) {
		return
	}
	e.mu.Lock()
	missed := e.maint
	e.maint = nil
	e.mu.Unlock()
	if len(missed) == 0 {
		return
	}
	body := strings.Join(missed, " · ")
	if len(missed) > 4 {
		body = strings.Join(missed[:4], " · ") + fmt.Sprintf(" · et %d autre(s)", len(missed)-4)
	}
	e.dispatch(Notification{
		Kind: "summary", Severity: "warning", Target: "maintenance",
		Title: fmt.Sprintf("Fin de maintenance : %d alerte(s)", len(missed)),
		Body:  body,
	}, false, true)
}

func (e *Engine) flushQuiet(now time.Time) {
	if e.set.Get().InQuietHours(now) {
		return
	}
	e.mu.Lock()
	missed := e.quiet
	e.quiet = nil
	e.mu.Unlock()
	if len(missed) == 0 {
		return
	}
	body := strings.Join(missed, " · ")
	if len(missed) > 4 {
		body = strings.Join(missed[:4], " · ") + fmt.Sprintf(" · et %d autre(s)", len(missed)-4)
	}
	e.dispatch(Notification{
		Kind: "summary", Severity: "warning", Target: "heures-calmes",
		Title: fmt.Sprintf("%d alerte(s) pendant les heures calmes", len(missed)),
		Body:  body,
	}, false, true)
}

func (e *Engine) evaluateOracle() {
	if !e.set.Get().OracleWatch {
		return
	}
	r, err := oracle.Evaluate(e.ctx, e.st, e.mon.Host.Kernel, e.mon.Host.NumCPU)
	if err != nil || !r.Applicable || r.Days < 3 {
		return
	}
	key := "oracle"
	e.mu.Lock()
	active := e.active[key]
	e.mu.Unlock()
	if r.Risk == "high" && !active {
		e.fire(key, Notification{
			Kind: "oracle", Severity: "warning", Target: "oracle",
			Title: "Risque de récupération par Oracle",
			Body: fmt.Sprintf("Sur %.0f jours : CPU p95 %.0f %%, mémoire p95 %.0f %%, réseau %.1f %%. "+
				"Oracle peut récupérer une instance Always Free inactive.", r.Days, r.CPU.Value, r.Mem.Value, r.Net.Value),
		})
	} else if r.Risk != "high" && active {
		e.resolve(key, Notification{
			Kind: "oracle", Severity: "info", Target: "oracle",
			Title: "Instance de nouveau assez active pour Oracle",
		})
	}
}
