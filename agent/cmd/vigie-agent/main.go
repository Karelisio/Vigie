// Vigie agent : surveille l'hôte et ses conteneurs Docker, et expose une API sécurisée pour l'appli Vigie.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // fuseau horaire (TZ) pour les heures calmes, même sans base système

	"github.com/karelisio/vigie/agent/internal/alerts"
	"github.com/karelisio/vigie/agent/internal/api"
	"github.com/karelisio/vigie/agent/internal/autoheal"
	"github.com/karelisio/vigie/agent/internal/backup"
	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/debrid"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/logwatch"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/qr"
	"github.com/karelisio/vigie/agent/internal/regression"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
	"github.com/karelisio/vigie/agent/internal/traffic"
	"github.com/karelisio/vigie/agent/internal/updater"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	// Conteneur auxiliaire lancé par l'agent pour se mettre à jour lui-même.
	if id := os.Getenv("VIGIE_HELPER_REPLACE"); id != "" {
		os.Exit(runHelper(log, id))
	}
	if err := run(log); err != nil {
		log.Error("arrêt", "erreur", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	dk, err := docker.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	if v, err := dk.Version(ctx); err != nil {
		log.Warn("Docker injoignable pour l'instant", "hôte", cfg.DockerHost, "erreur", err)
	} else {
		log.Info("Docker connecté", "version", v.Version, "api", v.APIVersion, "arch", v.Arch)
	}

	sys := system.New(cfg.HostProc, cfg.HostSys, cfg.HostRoot)
	set := settings.Load(ctx, st, cfg)
	mon := monitor.New(cfg, dk, sys, st, set, log)
	runner := checks.NewRunner(st, log, version, cfg.CheckFailures)
	runner.Seed(ctx, cfg.ChecksSeed)
	eng := alerts.New(ctx, cfg, mon, dk, st, log)

	mon.OnSystem = eng.OnSystem
	mon.OnEvent = eng.OnEvent
	mon.OnImageUpdates = eng.OnImageUpdates
	meter := traffic.NewMeter(st)
	mon.OnMinute = func(ts time.Time, s system.Snapshot) {
		// Contexte indépendant : la dernière minute est aussi comptée à l'arrêt de l'agent.
		if err := meter.Sample(context.Background(), ts, s.NetRxTotal, s.NetTxTotal); err != nil {
			log.Warn("compteur de trafic", "erreur", err)
		}
	}
	set.OnChange = func(s settings.Settings) {
		mon.Broadcast("settings", s)
		mon.Refresh()
	}
	runner.OnChange = mon.SetChecks
	runner.OnResult = eng.OnCheck

	// Alertes sur les logs : un suiveur par conteneur visé, relancé à chaque démarrage ou recréation.
	lw := logwatch.New(dk, mon.Containers, set.Get, eng.NotifyLog, log)
	onEvent, onSettings := mon.OnEvent, set.OnChange
	mon.OnEvent = func(ev docker.Event) {
		onEvent(ev)
		lw.OnEvent(ev)
	}
	set.OnChange = func(s settings.Settings) {
		onSettings(s)
		lw.Reload()
	}

	// Consommation avant / après chaque mise à jour.
	reg := regression.New(st, set.Get, eng.NotifyRegression, log)

	// Réparation automatique : healthcheck en échec depuis le délai choisi, ou arrêt anormal.
	heal := autoheal.New(dk, mon, st, set.Get, eng.NotifyAutoHeal, log)
	healEvent := mon.OnEvent
	mon.OnEvent = func(ev docker.Event) {
		healEvent(ev)
		heal.OnEvent(ev)
	}

	upd := updater.New(dk, log, updater.Hooks{
		NoteAction:   mon.NoteAction,
		SetTransient: mon.SetTransient,
		Publish:      func(j updater.Job) { mon.Broadcast("job", j) },
		Changed:      mon.AfterUpdate,
		Finished: func(j updater.Job) {
			result := "ok"
			if !j.OK {
				result = j.Error
			}
			st.Audit(context.Background(), "agent", "résultat", j.Target, result+" — "+j.Message, "vigie")
			if j.Action == "update" && j.Kind != "self" {
				var updated []string
				for _, r := range j.Results {
					if r.Updated && r.Name != "" {
						updated = append(updated, r.Name)
					}
				}
				reg.OnUpdated(context.Background(), updated, time.UnixMilli(j.FinishedAt))
			}
			if j.Kind == "rollback" && j.OK {
				reg.Forget(context.Background(), j.Target) // plus de comparaison avec la version écartée
			}
			if j.Kind == "backup" {
				// Sauvegardes : un échec est une alerte à part entière, même appli ouverte.
				eng.NotifyBackup(j.OK, j.Message, j.Error)
				return
			}
			if j.Kind != "self" || !j.OK {
				eng.NotifyJob(j.Title(), j.Body(), j.OK)
			}
		},
	}, updater.Options{
		Enabled:     cfg.AllowUpdates,
		SelfID:      mon.SelfID(),
		PruneImages: func() bool { return set.Get().PruneImages },
		DataDir:     cfg.DataDir,
		Version:     version,
		Store:       st,
	})
	// Retour à la version précédente : version écartée ignorée, bouton sur l'alerte de surconsommation.
	mon.IgnoredDigest = upd.IgnoredDigest
	eng.Rollback = upd.CanRollback
	if r := updater.LastSelfUpdate(cfg.DataDir); r != nil && time.Since(time.UnixMilli(r.TS)) < 10*time.Minute {
		if r.OK {
			log.Info("agent mis à jour", "depuis", r.From, "vers", version)
		} else {
			log.Warn("échec de la mise à jour de l'agent, ancienne version rétablie", "erreur", r.Error)
		}
	}

	// Comptes debrid : vérifiés toutes les 5 min (état des services), alertes d'expiration et de panne.
	dm := debrid.New(ctx, st, log)
	dm.OnCheck = eng.OnDebrid
	dm.OnService = eng.OnDebridServices

	apiSrv := api.New(cfg, mon, dk, st, runner, eng, upd, log, version)
	apiSrv.LogWatch = lw
	apiSrv.AutoHeal = heal
	apiSrv.Debrid = dm
	bk := backup.New(st, dk, upd, log, backup.Options{DataDir: cfg.DataDir, SelfID: mon.SelfID(), NoteAction: mon.NoteAction})
	apiSrv.Backups = bk
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	banner(log, cfg, mon)

	go mon.Run(ctx)
	go reg.Run(ctx)
	go runner.Run(ctx)
	go updater.NewNightly(upd, mon, st, log).Run(ctx)
	go lw.Run(ctx)
	go heal.Run(ctx)
	go dm.Run(ctx)
	go bk.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		log.Info("API à l'écoute", "adresse", cfg.Listen, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}
	log.Info("arrêt en cours…")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	time.Sleep(200 * time.Millisecond)
	return nil
}

func banner(log *slog.Logger, cfg *config.Config, mon *monitor.Monitor) {
	h := mon.Host
	log.Info("Vigie agent", "version", version, "serveur", cfg.ServerName, "hôte", h.Hostname, "os", h.OS, "noyau", h.Kernel, "cpu", h.NumCPU)
	if mon.SelfID() != "" {
		log.Info("conteneur de l'agent détecté", "id", mon.SelfID()[:12])
	}
	if !cfg.FirstStart && !cfg.ShowQR {
		log.Info("QR de configuration masqué (définis VIGIE_SHOW_QR=true pour l'afficher à nouveau)")
		return
	}
	if cfg.PublicURL == "" {
		log.Warn("VIGIE_PUBLIC_URL non défini : le QR ne contiendra pas l'adresse du serveur")
	}
	q := url.Values{}
	q.Set("name", cfg.ServerName)
	q.Set("url", cfg.PublicURL)
	q.Set("token", cfg.Token)
	link := "vigie://connect?" + q.Encode()

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("════════════════════════════════════════════════════════════════\n")
	b.WriteString("  VIGIE — configuration de l'appli\n")
	b.WriteString("  Ouvre Vigie > Ajouter un serveur > Scanner le QR code\n")
	b.WriteString("════════════════════════════════════════════════════════════════\n")
	if code, err := qr.Render(link, cfg.QRInvert); err == nil {
		b.WriteString(code)
	}
	b.WriteString("\n  Ou saisie manuelle :\n")
	fmt.Fprintf(&b, "    URL   : %s\n", orDash(cfg.PublicURL))
	fmt.Fprintf(&b, "    Token : %s\n", cfg.Token)
	fmt.Fprintf(&b, "    Lien  : %s\n", link)
	if !cfg.TokenFromEnv {
		b.WriteString("\n  Token généré automatiquement et conservé dans le volume /data.\n")
	}
	b.WriteString("  QR illisible ? Ajoute VIGIE_QR_INVERT=true et redémarre.\n")
	b.WriteString("════════════════════════════════════════════════════════════════\n")
	fmt.Fprint(os.Stdout, b.String())
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// runHelper remplace l'ancien agent par un conteneur identique utilisant la nouvelle image.
func runHelper(log *slog.Logger, oldID string) int {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	dk, err := docker.New(host)
	if err != nil {
		log.Error("mise à jour de l'agent", "erreur", err)
		return 1
	}
	dataDir := os.Getenv("VIGIE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/data"
	}
	prune := os.Getenv("VIGIE_HELPER_PRUNE") == "true"
	m := updater.New(dk, log, updater.Hooks{}, updater.Options{
		Enabled: true, DataDir: dataDir, Version: version, PruneImages: func() bool { return prune },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	log.Info("remplacement de l'agent", "ancien", oldID[:min(12, len(oldID))])
	if err := m.RunHelper(ctx, oldID); err != nil {
		log.Error("mise à jour de l'agent annulée", "erreur", err)
		return 1
	}
	log.Info("agent remplacé")
	return 0
}

// healthcheck est utilisé par le HEALTHCHECK de l'image (pas de curl dans l'image distroless).
func healthcheck() int {
	addr := os.Getenv("VIGIE_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
