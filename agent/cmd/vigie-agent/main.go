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
	"github.com/karelisio/vigie/agent/internal/checks"
	"github.com/karelisio/vigie/agent/internal/config"
	"github.com/karelisio/vigie/agent/internal/docker"
	"github.com/karelisio/vigie/agent/internal/monitor"
	"github.com/karelisio/vigie/agent/internal/qr"
	"github.com/karelisio/vigie/agent/internal/settings"
	"github.com/karelisio/vigie/agent/internal/store"
	"github.com/karelisio/vigie/agent/internal/system"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
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
	set.OnChange = func(s settings.Settings) {
		mon.Broadcast("settings", s)
		mon.Refresh()
	}
	runner.OnChange = mon.SetChecks
	runner.OnResult = eng.OnCheck

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.New(cfg, mon, dk, st, runner, eng, log, version).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	banner(log, cfg, mon)

	go mon.Run(ctx)
	go runner.Run(ctx)

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
