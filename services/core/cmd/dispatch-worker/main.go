// Command dispatch-worker consumes dispatch.intent events from Redpanda and
// executes Jira/Teams/Slack/webhook actions on behalf of the acting user.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/config"
	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

const consumerGroup = "marble-jar-dispatch"

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
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if len(cfg.RedpandaBrokers) == 0 {
		return errors.New("REDPANDA_BROKERS is required for the dispatch worker")
	}
	coreURL := envOr("CORE_API_BASE_URL", "http://localhost:"+cfg.Port)
	serviceToken := os.Getenv("CORE_SERVICE_TOKEN")
	if serviceToken == "" {
		return errors.New("CORE_SERVICE_TOKEN is required so the worker can report results")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	oboProvider := dispatch.NewOBOProvider(
		cfg.OAuthIssuerURL, cfg.OAuthClientID, cfg.OAuthClientSecret, log)

	// Connected provider accounts are the real credentials; the issuer's
	// RFC 8693 exchange is the fallback when no connection exists.
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect store: %w", err)
	}
	defer st.Close()
	tokens := &connectionFirstTokens{store: st, obo: oboProvider}

	worker := dispatch.NewWorker(dispatch.WorkerConfig{
		CoreBaseURL:  coreURL,
		ServiceToken: serviceToken,
		HMACSecret:   cfg.HMACDispatchSecret,
		Concurrency:  cfg.DispatchWorkers,
	}, map[string]dispatch.Executor{
		"jira":    dispatch.NewJiraExecutor(),
		"slack":   dispatch.NewSlackExecutor(),
		"teams":   dispatch.NewTeamsExecutor(),
		"webhook": dispatch.NewWebhookExecutor(cfg.HMACDispatchSecret),
	}, tokens, log)

	worker.Start(ctx)

	// Health/metrics surface for Kubernetes probes.
	go serveHealth(ctx, envOr("WORKER_PORT", "8081"), log)

	reader := eventbus.NewKafkaReader(cfg.RedpandaBrokers, eventbus.TopicDispatchIntent, consumerGroup)
	defer reader.Close()

	log.Info("dispatch worker consuming",
		"topic", eventbus.TopicDispatchIntent, "group", consumerGroup,
		"concurrency", cfg.DispatchWorkers)

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Info("dispatch worker shutting down")
				return nil
			}
			log.Error("fetch message failed", "error", err)
			time.Sleep(time.Second)
			continue
		}

		var ev eventbus.Event
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			log.Error("decode event failed", "error", err, "offset", msg.Offset)
			// Undecodable messages will never succeed; commit and move on.
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		if err := worker.Submit(ctx, eventEnvelope{ev}); err != nil {
			var perm dispatch.PermanentError
			if errors.As(err, &perm) {
				// Poison message: will never succeed. Commit past it.
				log.Error("discarding invalid intent", "error", err, "event_id", ev.ID)
				_ = reader.CommitMessages(ctx, msg)
				continue
			}
			log.Error("submit intent failed", "error", err, "event_id", ev.ID)
			// Do not commit: the message is redelivered after rebalance.
			continue
		}

		if err := reader.CommitMessages(ctx, msg); err != nil {
			log.Warn("commit failed", "error", err, "offset", msg.Offset)
		}
	}
}

// eventEnvelope adapts eventbus.Event to the worker's Submit interface.
type eventEnvelope struct{ ev eventbus.Event }

func (e eventEnvelope) GetPayload() []byte   { return e.ev.Payload }
func (e eventEnvelope) GetSignature() string { return e.ev.Signature }

func serveHealth(ctx context.Context, port string, log *slog.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("worker health server failed", "error", err)
	}
}

// connectionFirstTokens resolves dispatch credentials: the stored provider
// connection for the acting user first (a real Slack/Atlassian/Entra token),
// falling back to the configured issuer's OBO token exchange.
type connectionFirstTokens struct {
	store *store.Store
	obo   *dispatch.OBOProvider
}

func (t *connectionFirstTokens) OBOToken(ctx context.Context, orgID string, userID *string, provider string) (string, error) {
	if userID != nil && *userID != "" {
		conn, err := t.store.GetOAuthConnection(ctx, orgID, *userID, provider)
		if err == nil && conn.AccessTokenEncrypted != "" {
			return conn.AccessTokenEncrypted, nil
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
	}
	return t.obo.OBOToken(ctx, orgID, userID, provider)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
