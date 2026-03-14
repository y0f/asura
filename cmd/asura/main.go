package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/escalation"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/monitor"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/server"
	"github.com/y0f/asura/internal/storage"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	hashKey := flag.String("hash-key", "", "hash an API key and exit")
	setup := flag.Bool("setup", false, "generate an API key and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	setupTOTP := flag.String("setup-totp", "", "set up TOTP for an API key name and exit")
	verifyTOTP := flag.String("verify-totp", "", "verify a TOTP code for an API key and exit")
	removeTOTP := flag.String("remove-totp", "", "remove TOTP secret for an API key and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("asura %s\n", version)
		os.Exit(0)
	}

	if *setup {
		key, hash, err := generateAPIKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Printf("  API Key : %s\n", key)
		fmt.Printf("  Hash    : %s\n", hash)
		fmt.Println()
		fmt.Println("  Put the hash in config.yaml under auth.api_keys[].hash")
		fmt.Println("  Save the API key — it cannot be recovered.")
		fmt.Println()
		os.Exit(0)
	}

	if *hashKey != "" {
		fmt.Println(config.HashAPIKey(*hashKey))
		os.Exit(0)
	}

	if *setupTOTP != "" || *verifyTOTP != "" || *removeTOTP != "" {
		if err := handleTOTPCommands(*configPath, *setupTOTP, *verifyTOTP, *removeTOTP, flag.Args()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.Logging)
	logger.Info("starting asura", "version", version, "listen", cfg.Server.Listen)

	store, err := storage.NewSQLiteStore(cfg.Database.Path, cfg.Database.MaxReadConns)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	logger.Info("database opened", "path", cfg.Database.Path)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	purgeStaleSessionsOnStartup(ctx, store, cfg, logger)

	registry := checker.DefaultRegistry(cfg.Monitor.CommandAllowlist, cfg.Monitor.AllowPrivateTargets)
	incMgr := incident.NewManager(store, logger)
	pipeline := monitor.NewPipeline(store, registry, incMgr, cfg.Monitor.Workers, cfg.Monitor.AdaptiveIntervals, logger)
	dispatcher := notifier.NewDispatcher(store, logger, cfg.Monitor.AllowPrivateTargets)

	var subNotifier *notifier.SubscriberNotifier
	if cfg.Subscriptions.Enabled {
		subNotifier = notifier.NewSubscriberNotifier(store, cfg.Subscriptions.SMTP, cfg.ResolvedExternalURL(), logger)
		logger.Info("status page subscriptions enabled")
	}

	escalationRunner := escalation.NewRunner(store, dispatcher, logger)
	go escalationRunner.Start(ctx)

	go pipeline.Run(ctx)

	heartbeatWatcher := monitor.NewHeartbeatWatcher(store, incMgr, pipeline, cfg.Monitor.HeartbeatCheckInterval, logger)
	go heartbeatWatcher.Run(ctx)

	retentionWorker := storage.NewRetentionWorker(store, cfg.Database.RetentionDays, cfg.Database.RequestLogRetentionDays, cfg.Database.RetentionPeriod, logger)
	go retentionWorker.Run(ctx)

	srv := server.NewServer(cfg, store, pipeline, dispatcher, subNotifier, logger, version)
	go forwardNotifications(ctx, pipeline, dispatcher, subNotifier, srv.EventBroker())
	go srv.RequestLogWriter().Run(ctx)
	go runRollupWorker(ctx, store, logger)
	httpServer := startHTTPServer(cfg, srv, logger, cancel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}

func forwardNotifications(ctx context.Context, pipeline *monitor.Pipeline, dispatcher *notifier.Dispatcher, subNotifier *notifier.SubscriberNotifier, broker *server.EventBroker) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-pipeline.NotifyChan():
			payload := &notifier.Payload{
				EventType: event.EventType,
				Incident:  event.Incident,
				Monitor:   event.Monitor,
				Change:    event.Change,
			}
			if event.MonitorID > 0 {
				dispatcher.NotifyForMonitor(event.MonitorID, payload)
			} else {
				dispatcher.NotifyWithPayload(payload)
			}

			if subNotifier != nil && event.MonitorID > 0 {
				switch event.EventType {
				case "incident.created", "incident.resolved":
					go subNotifier.NotifyForMonitor(ctx, event.MonitorID, event.EventType, event.Incident, event.Monitor)
				}
			}

			if broker != nil {
				broker.Broadcast(server.SSEEvent{
					Type: event.EventType,
					Data: payload,
				})
			}
		}
	}
}

func startHTTPServer(cfg *config.Config, handler http.Handler, logger *slog.Logger, cancel context.CancelFunc) *http.Server {
	httpServer := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		httpServer.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		go func() {
			logger.Info("starting HTTPS server", "listen", cfg.Server.Listen)
			if err := httpServer.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTPS server error", "error", err)
				cancel()
			}
		}()
	} else {
		go func() {
			logger.Info("starting HTTP server", "listen", cfg.Server.Listen)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", "error", err)
				cancel()
			}
		}()
	}

	return httpServer
}

func setupLogger(cfg config.LoggingConfig) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func runRollupWorker(ctx context.Context, store storage.Store, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
			if err := store.RollupRequestLogs(ctx, yesterday); err != nil {
				logger.Error("request log rollup failed", "date", yesterday, "error", err)
			}
		}
	}
}

func purgeStaleSessionsOnStartup(ctx context.Context, store storage.Store, cfg *config.Config, logger *slog.Logger) {
	validNames := make([]string, len(cfg.Auth.APIKeys))
	for i, k := range cfg.Auth.APIKeys {
		validNames[i] = k.Name
	}
	deleted, err := store.DeleteSessionsExceptKeyNames(ctx, validNames)
	if err != nil {
		logger.Warn("failed to purge stale sessions", "error", err)
		return
	}
	if deleted > 0 {
		logger.Info("purged sessions for removed API keys", "deleted", deleted)
	}
}
