// Command boop runs the Boop notification server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chrisgreg/boop/server/internal/api"
	"github.com/chrisgreg/boop/server/internal/apns"
	"github.com/chrisgreg/boop/server/internal/auth"
	"github.com/chrisgreg/boop/server/internal/config"
	"github.com/chrisgreg/boop/server/internal/database"
	"github.com/chrisgreg/boop/server/internal/delivery"
	"github.com/chrisgreg/boop/server/internal/devices"
	"github.com/chrisgreg/boop/server/internal/events"
	"github.com/chrisgreg/boop/server/internal/pairing"
	"github.com/chrisgreg/boop/server/internal/projects"
	"github.com/chrisgreg/boop/server/internal/settings"
	"github.com/chrisgreg/boop/server/internal/silences"
	"github.com/chrisgreg/boop/server/internal/web"
	"github.com/chrisgreg/boop/server/internal/webpush"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "boop:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", cfg.DatabasePath, err)
	}
	defer db.Close()

	st := settings.New(db)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Retention: an explicit BOOP_RETENTION_DAYS wins; otherwise the UI-saved value (default 90) is kept.
	if cfg.RetentionDaysSet {
		err = st.Set(ctx, settings.KeyRetentionDays, strconv.Itoa(cfg.RetentionDays))
	} else {
		err = st.SetDefault(ctx, settings.KeyRetentionDays, strconv.Itoa(cfg.RetentionDays))
	}
	if err != nil {
		return err
	}

	devStore := devices.New(db)
	webPushStore := webpush.NewStore(db)
	webPushSubject := cfg.BaseURL
	if !strings.HasPrefix(webPushSubject, "https://") {
		webPushSubject = "mailto:boop@localhost"
	}
	webPushClient, err := webpush.NewClient(ctx, webPushStore, webPushSubject)
	if err != nil {
		return fmt.Errorf("initialize Web Push: %w", err)
	}
	log.Info("web_push.configured", "subject", webPushSubject)
	var sender delivery.Sender
	var client *apns.Client
	apnsErr := ""
	if cfg.APNS.Configured() {
		client, err = apns.New(cfg.APNS)
		if err != nil {
			apnsErr = err.Error()
			log.Warn("apns.invalid", "error", apnsErr)
		} else {
			sender = client
			log.Info("apns.configured", "environment", cfg.APNS.Environment, "bundle_id", cfg.APNS.BundleID)
		}
	} else {
		apnsErr = "missing " + join(cfg.APNS.Missing())
		log.Warn("apns.not_configured", "missing", cfg.APNS.Missing())
	}
	dispatcher := delivery.New(db, devStore, sender, webPushStore, webPushClient, log)
	dispatcher.Start(ctx)

	admin := auth.NewAdmin(cfg.AdminUser, cfg.AdminPassword)
	if admin.Enabled() {
		log.Info("auth.enabled", "user", cfg.AdminUser)
	} else {
		log.Warn("auth.disabled", "hint", "set BOOP_ADMIN_USER and BOOP_ADMIN_PASSWORD to protect the web UI")
	}

	evStore := events.New(db)
	srv := &api.Server{
		Config: cfg, DB: db, Log: log, Settings: st,
		Projects: projects.New(db), Devices: devStore, Pairing: pairing.New(db, devStore), Events: evStore, Silences: silences.New(db),
		WebPush: webPushStore, WebPushClient: webPushClient,
		Dispatcher: dispatcher, APNS: client, APNSError: apnsErr, Admin: admin, StartedAt: time.Now(), Web: web.Handler(),
	}

	go retentionLoop(ctx, log, st, evStore, srv.Pairing, cfg.RetentionDays)

	httpServer := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Port)),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	log.Info("server.started", "port", cfg.Port, "database", cfg.DatabasePath, "version", api.Version)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("server.stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	stop()
	dispatcher.Wait()
	return nil
}

// retentionLoop prunes old events once at start and then hourly.
func retentionLoop(ctx context.Context, log *slog.Logger, st *settings.Store, ev *events.Store, pr *pairing.Store, def int) {
	run := func() {
		days, err := st.GetInt(ctx, settings.KeyRetentionDays, def)
		if err != nil {
			log.Error("retention.failed", "error", err.Error())
			return
		}
		n, err := ev.Prune(ctx, days, time.Now())
		if err != nil {
			log.Error("retention.failed", "error", err.Error())
			return
		}
		_ = pr.Cleanup(ctx)
		if n > 0 {
			log.Info("retention.completed", "deleted", n, "retention_days", days)
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
