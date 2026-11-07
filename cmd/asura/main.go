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
	"sync"
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

	if cfg.Database.EncryptionKey != "" {
		if err := enableEncryption(ctx, store, cfg.Database.EncryptionKey); err != nil {
			logger.Error("failed to enable encryption at rest", "error", err)
			os.Exit(1)
		}
		logger.Info("encryption at rest enabled")
	}

	purgeStaleSessionsOnStartup(ctx, store, cfg, logger)

	registry := checker.DefaultRegistry(cfg.Monitor.CommandAllowlist, cfg.Monitor.AllowPrivateTargets)
	incMgr := incident.NewManager(store, logger)
	pipeline := monitor.NewPipeline(store, registry, incMgr, cfg.Monitor.Workers, cfg.Monitor.AdaptiveIntervals, logger)
	dispatcher := notifier.NewDispatcher(store, logger, cfg.Monitor.AllowPrivateTargets)

	var subNotifier *notifier.SubscriberNotifier
	if cfg.Subscriptions.Enabled {
		subNotifier = notifier.NewSubscriberNotifier(store, cfg.Subscriptions.SMTP, cfg.ResolvedExternalURL(), logger, cfg.Monitor.AllowPrivateTargets)
		logger.Info("status page subscriptions enabled")
	}

	srv := server.NewServer(cfg, store, pipeline, dispatcher, subNotifier, logger, version)

	escalationRunner := escalation.NewRunner(store, dispatcher, logger)
	heartbeatWatcher := monitor.NewHeartbeatWatcher(store, incMgr, pipeline, cfg.Monitor.HeartbeatCheckInterval, logger)
	retentionWorker := storage.NewRetentionWorker(store, cfg.Database.RetentionDays, cfg.Database.RequestLogRetentionDays, cfg.Database.RetentionPeriod, logger)

	var wg sync.WaitGroup
	worker := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	worker(func() { escalationRunner.Start(ctx) })
	worker(func() { pipeline.Run(ctx) })
	worker(func() { heartbeatWatcher.Run(ctx) })
	worker(func() { retentionWorker.Run(ctx) })
	// The forwarder outlives producers so events already accepted by the
	// pipeline are drained before the delivery queues stop admission.
	producersDone := make(chan struct{})
	forwardDone := make(chan struct{})
	forwardCtx, cancelForward := context.WithCancel(context.Background())
	defer cancelForward()
	go func() {
		defer close(forwardDone)
		forwardNotifications(forwardCtx, producersDone, pipeline.NotifyChan(), dispatcher, subNotifier, srv.EventBroker(), logger)
	}()
	worker(func() { srv.RequestLogWriter().Run(ctx) })
	worker(func() { runRollupWorker(ctx, store, logger) })
	worker(func() { runBaselineWorker(ctx, store, logger) })
	worker(func() { runRotationAdvancer(ctx, store, logger) })
	worker(func() { runVacuumWorker(ctx, store, logger) })

	requests := &requestTracker{handler: srv}
	httpServer := startHTTPServer(cfg, requests, logger, cancel)
	// Shutdown does not cancel request contexts, so long-lived SSE streams
	// must be closed explicitly or they hold up graceful shutdown.
	httpServer.RegisterOnShutdown(srv.EventBroker().Close)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("received signal, shutting down", "signal", sig)
	case <-ctx.Done():
	}

	// Drain HTTP first so in-flight requests finish (and their request logs are
	// queued) while the background workers are still running; then stop them.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	requests.Stop()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
		_ = httpServer.Close()
	}
	requests.Wait()
	srv.Close()

	cancel()

	// Never close SQLite under a still-running producer or delivery worker.
	wg.Wait()
	close(producersDone)
	forwardGrace := time.AfterFunc(15*time.Second, cancelForward)
	<-forwardDone
	forwardGrace.Stop()
	cancelForward()

	deliveryCtx, deliveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer deliveryCancel()
	if err := dispatcher.Shutdown(deliveryCtx); err != nil {
		logger.Warn("notification drain interrupted", "error", err)
	}
	logger.Info("notification delivery stopped", "counts", dispatcher.Stats())
	if subNotifier != nil {
		if err := subNotifier.Shutdown(deliveryCtx); err != nil {
			logger.Warn("subscriber drain interrupted", "error", err)
		}
		logger.Info("subscriber delivery stopped", "counts", subNotifier.Stats())
	}
	logger.Info("shutdown complete")
}

func forwardNotifications(ctx context.Context, producersDone <-chan struct{}, events <-chan monitor.NotificationEvent, dispatcher *notifier.Dispatcher, subNotifier *notifier.SubscriberNotifier, broker *server.EventBroker, logger *slog.Logger) {
	forward := func(event monitor.NotificationEvent) {
		// Bound lookups and fan-out even when there are many configured
		// recipients; accepted sends have their own delivery lifetime.
		admissionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		payload := &notifier.Payload{EventType: event.EventType, Incident: event.Incident, Monitor: event.Monitor, Change: event.Change}
		var err error
		if event.MonitorID > 0 {
			err = dispatcher.NotifyForMonitor(admissionCtx, event.MonitorID, payload)
		} else {
			err = dispatcher.NotifyWithPayload(admissionCtx, payload)
		}
		if err != nil {
			logger.Error("forward notification", "monitor_id", event.MonitorID, "event", event.EventType, "error", err)
		}
		if subNotifier != nil && event.MonitorID > 0 {
			switch event.EventType {
			case "incident.created", "incident.resolved":
				if err := subNotifier.NotifyForMonitor(admissionCtx, event.MonitorID, event.EventType, event.Incident, event.Monitor); err != nil {
					logger.Error("forward subscriber notification", "monitor_id", event.MonitorID, "event", event.EventType, "error", err)
				}
			}
		}
		if broker != nil {
			broker.Broadcast(server.SSEEvent{Type: event.EventType, Data: payload})
		}
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			forward(event)
		case <-producersDone:
			// All producers have joined, so an empty buffer is now final.
			for {
				select {
				case event, ok := <-events:
					if !ok {
						return
					}
					forward(event)
				default:
					return
				}
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

func runBaselineWorker(ctx context.Context, store storage.Store, logger *slog.Logger) {
	recalculate := func() {
		monitors, err := store.GetAllEnabledMonitors(ctx)
		if err != nil {
			logger.Error("baseline: list monitors", "error", err)
			return
		}
		from := time.Now().UTC().AddDate(0, 0, -7)
		for _, mon := range monitors {
			if mon.AnomalySensitivity == "" || mon.AnomalySensitivity == "off" {
				continue
			}
			avg, stddev, count, err := store.GetResponseTimeStats(ctx, mon.ID, from)
			if err != nil || count < 30 {
				continue
			}
			if err := store.UpdateBaseline(ctx, mon.ID, avg, stddev); err != nil {
				logger.Error("baseline: update", "monitor_id", mon.ID, "error", err)
			}
		}
	}

	recalculate()

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recalculate()
		}
	}
}

func runRotationAdvancer(ctx context.Context, store storage.Store, logger *slog.Logger) {
	advance := func() {
		rotations, err := store.ListOnCallRotations(ctx)
		if err != nil {
			logger.Error("rotation advancer: list", "error", err)
			return
		}
		for _, rot := range rotations {
			if err := store.AdvanceRotation(ctx, rot.ID); err != nil {
				logger.Error("rotation advancer: advance", "id", rot.ID, "error", err)
			}
		}
	}
	advance()
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			advance()
		}
	}
}

func runVacuumWorker(ctx context.Context, store storage.Store, logger *slog.Logger) {
	if err := store.IncrementalVacuum(ctx); err != nil {
		logger.Error("incremental vacuum failed", "error", err)
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.IncrementalVacuum(ctx); err != nil {
				logger.Error("incremental vacuum failed", "error", err)
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
