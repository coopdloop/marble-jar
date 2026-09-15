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
	"github.com/marble-jar/marble-jar/services/core/internal/secrets"
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

	tokenBox, err := secrets.New(cfg.OAuthTokenKey)
	if err != nil {
		return fmt.Errorf("token key: %w", err)
	}
	if tokenBox == nil {
		log.Warn("OAUTH_TOKEN_KEY unset — provider OAuth tokens are read and stored in plaintext")
	}

	// Connected provider accounts are the real credentials; the issuer's
	// RFC 8693 exchange is the fallback when no connection exists.
	st, err := store.New(ctx, cfg.DatabaseURL, store.WithTokenBox(tokenBox))
	if err != nil {
		return fmt.Errorf("connect store: %w", err)
	}
	defer st.Close()
	tokens := &connectionFirstTokens{
		store:     st,
		obo:       oboProvider,
		refresh:   dispatch.NewRefresher(),
		appCreds:  providerCreds(cfg),
		log:       log,
		expiryGap: time.Minute,
	}

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
// refreshing it when the provider says it is about to expire, and falling back
// to the configured issuer's OBO token exchange only when no connection exists.
type connectionFirstTokens struct {
	store     *store.Store
	obo       *dispatch.OBOProvider
	refresh   *dispatch.Refresher
	appCreds  func(provider string) (clientID, clientSecret string)
	log       *slog.Logger
	expiryGap time.Duration
}

func (t *connectionFirstTokens) OBOToken(ctx context.Context, orgID string, userID *string, provider string) (string, error) {
	if userID == nil || *userID == "" {
		return t.obo.OBOToken(ctx, orgID, userID, provider)
	}

	conn, err := t.store.GetOAuthConnection(ctx, orgID, *userID, provider)
	if errors.Is(err, store.ErrNotFound) {
		return t.obo.OBOToken(ctx, orgID, userID, provider)
	}
	if err != nil {
		return "", err
	}
	if conn.AccessToken == "" {
		return t.obo.OBOToken(ctx, orgID, userID, provider)
	}

	if !t.expiringSoon(conn.ExpiresAt) || conn.RefreshToken == nil || *conn.RefreshToken == "" {
		return conn.AccessToken, nil
	}

	clientID, clientSecret := t.appCreds(provider)
	res, err := t.refresh.Refresh(ctx, provider, clientID, clientSecret, *conn.RefreshToken)
	if err != nil {
		// A dead grant is not something the queue can wait out: report it now so
		// the dispatch dead-letters with a message that says "reconnect" instead
		// of failing later with an opaque 401 from the provider API.
		var permanent dispatch.PermanentError
		if errors.As(err, &permanent) {
			return "", fmt.Errorf("%s connection for the acting user can no longer be refreshed: %w", provider, err)
		}
		// The stored token may still be valid, and the issuer fallback may work,
		// so a provider blip must not stop this dispatch.
		t.log.Warn("token refresh failed; using stored token",
			"provider", provider, "acting_user_id", *userID, "error", err)
		return conn.AccessToken, nil
	}

	rotated := res.RefreshToken
	next := &rotated
	if rotated == "" {
		next = nil // provider did not rotate: UpdateOAuthConnectionTokens keeps the old one
	}
	var expiresAt *time.Time
	if res.ExpiresIn > 0 {
		at := time.Now().UTC().Add(res.ExpiresIn)
		expiresAt = &at
	}
	if err := t.store.UpdateOAuthConnectionTokens(ctx, orgID, *userID, provider,
		res.AccessToken, next, expiresAt); err != nil {
		t.log.Warn("could not persist refreshed token", "provider", provider, "error", err)
	}
	t.log.Info("refreshed provider token", "provider", provider, "acting_user_id", *userID)
	return res.AccessToken, nil
}

// expiringSoon treats a missing expiry as "never": Slack and Atlassian both hand
// out long-lived tokens with no expires_at, and those must keep working.
func (t *connectionFirstTokens) expiringSoon(expiresAt *time.Time) bool {
	if expiresAt == nil {
		return false
	}
	gap := t.expiryGap
	if gap <= 0 {
		gap = time.Minute
	}
	return time.Now().UTC().Add(gap).After(expiresAt.UTC())
}

// providerCreds maps an integration onto the OAuth app configured for it.
func providerCreds(cfg *config.Config) func(string) (string, string) {
	return func(provider string) (string, string) {
		switch provider {
		case "slack":
			return cfg.SlackClientID, cfg.SlackClientSecret
		case "jira":
			return cfg.AtlassianClientID, cfg.AtlassianClientSecret
		case "teams":
			return cfg.EntraClientID, cfg.EntraClientSecret
		}
		return "", ""
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
