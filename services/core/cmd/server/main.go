// Command server runs marble_jar_core: ingestion API, REST API, WebSocket
// queue gateway, objectives, rules engine and dispatch orchestration.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/config"
	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
	"github.com/marble-jar/marble-jar/services/core/internal/httpapi"
	"github.com/marble-jar/marble-jar/services/core/internal/marbles"
	"github.com/marble-jar/marble-jar/services/core/internal/objectives"
	"github.com/marble-jar/marble-jar/services/core/internal/realtime"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
	"github.com/marble-jar/marble-jar/services/core/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	log.Info("migrations applied")

	// Durable backbone: Redpanda when configured, in-process otherwise.
	var publisher eventbus.Publisher
	if len(cfg.RedpandaBrokers) > 0 {
		publisher = eventbus.NewKafkaPublisher(cfg.RedpandaBrokers, log)
		log.Info("event backbone: redpanda", "brokers", cfg.RedpandaBrokers)
	} else {
		publisher = eventbus.NewMemoryPublisher(log)
		log.Warn("REDPANDA_BROKERS unset — using in-process event bus (dev only)")
	}
	defer publisher.Close()

	// Realtime fanout: Redis pub/sub when configured, in-process otherwise.
	var broadcaster eventbus.Broadcaster
	if cfg.RedisURL != "" {
		broadcaster, err = eventbus.NewRedisBroadcaster(cfg.RedisURL, log)
		if err != nil {
			return err
		}
		log.Info("realtime fanout: redis pub/sub")
	} else {
		broadcaster = eventbus.NewMemoryBroadcaster()
		log.Warn("REDIS_URL unset — using in-process fanout (single replica only)")
	}
	defer broadcaster.Close()

	hub := realtime.NewHub(broadcaster, cfg.CORSOrigins, log)
	metrics := telemetry.New(func() float64 { return float64(hub.Connections()) })

	authManager := auth.NewManager(cfg.JWTSigningKey, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	dispatcher := dispatch.NewService(st, publisher, metrics, cfg.HMACDispatchSecret, log)
	ingestor := marbles.NewIngestor(st, publisher, broadcaster, metrics,
		cfg.PhoenixBaseURL, cfg.HMACDispatchSecret, log)
	ingestor.SetDispatcher(dispatcher)
	objSvc := objectives.NewService(st, dispatcher)

	go func() {
		if err := hub.Run(ctx); err != nil {
			log.Error("realtime hub stopped", "error", err)
		}
	}()

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewServer(cfg, st, authManager, hub, ingestor,
			objSvc, dispatcher, metrics, log).Router(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the queue WebSocket is a long-lived connection.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("marble_jar_core listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
	slog.SetDefault(l)
	return l
}
