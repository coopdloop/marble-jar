package dispatch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Executor performs one outbound integration action on behalf of a user.
type Executor interface {
	// Execute returns an external reference (ticket key, message ts, ...),
	// integration-specific detail, and an error.
	Execute(ctx context.Context, in Intent, token string) (externalRef string, detail map[string]any, err error)
}

// TokenProvider resolves a short-lived OBO access token for an integration.
type TokenProvider interface {
	OBOToken(ctx context.Context, orgID string, userID *string, provider string) (string, error)
}

// WorkerConfig configures the consolidated dispatch worker.
type WorkerConfig struct {
	CoreBaseURL   string
	ServiceToken  string
	HMACSecret    string
	Concurrency   int
	MaxAttempts   int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	RequestTimout time.Duration
}

func (c *WorkerConfig) applyDefaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = 500 * time.Millisecond
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = 60 * time.Second
	}
	if c.RequestTimout <= 0 {
		c.RequestTimout = 30 * time.Second
	}
}

// Worker consumes dispatch.intent events and executes them. Each integration
// gets its own goroutine pool so a slow Jira never starves Slack.
type Worker struct {
	cfg       WorkerConfig
	executors map[string]Executor
	tokens    TokenProvider
	client    *http.Client
	queues    map[string]chan Intent
	log       *slog.Logger
}

func NewWorker(cfg WorkerConfig, executors map[string]Executor, tokens TokenProvider, log *slog.Logger) *Worker {
	cfg.applyDefaults()
	w := &Worker{
		cfg:       cfg,
		executors: executors,
		tokens:    tokens,
		client:    &http.Client{Timeout: cfg.RequestTimout},
		queues:    map[string]chan Intent{},
		log:       log,
	}
	for name := range executors {
		w.queues[name] = make(chan Intent, 256)
	}
	return w
}

// Start launches the per-integration worker pools.
func (w *Worker) Start(ctx context.Context) {
	for name, q := range w.queues {
		for i := 0; i < w.cfg.Concurrency; i++ {
			go w.pool(ctx, name, q, i)
		}
		w.log.Info("dispatch pool started", "integration", name, "workers", w.cfg.Concurrency)
	}
}

// Submit routes a verified intent onto its integration queue.
func (w *Worker) Submit(ctx context.Context, ev interface {
	GetPayload() []byte
	GetSignature() string
}) error {
	payload := ev.GetPayload()
	// Signature, decode and routing failures are permanent — the message will
	// never become valid, so callers should commit past it rather than
	// redeliver forever.
	if w.cfg.HMACSecret != "" && ev.GetSignature() != "" {
		if !verifyHMAC(payload, ev.GetSignature(), w.cfg.HMACSecret) {
			return PermanentError{fmt.Errorf("dispatch intent failed signature verification")}
		}
	}

	var intent Intent
	if err := json.Unmarshal(payload, &intent); err != nil {
		return PermanentError{fmt.Errorf("decode intent: %w", err)}
	}

	q, ok := w.queues[intent.IntegrationType]
	if !ok {
		return PermanentError{fmt.Errorf("no executor for integration %q", intent.IntegrationType)}
	}

	select {
	case q <- intent:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("integration queue %q full", intent.IntegrationType)
	}
}

func (w *Worker) pool(ctx context.Context, name string, q <-chan Intent, id int) {
	log := w.log.With("integration", name, "worker", id)
	for {
		select {
		case <-ctx.Done():
			return
		case intent, ok := <-q:
			if !ok {
				return
			}
			w.handle(ctx, intent, log)
		}
	}
}

func (w *Worker) handle(ctx context.Context, intent Intent, log *slog.Logger) {
	exec := w.executors[intent.IntegrationType]
	log = log.With("dispatch_id", intent.DispatchID)

	w.report(ctx, intent, "running", nil, nil, nil, 0)

	var lastErr error
	for attempt := 1; attempt <= w.cfg.MaxAttempts; attempt++ {
		token, err := w.resolveToken(ctx, intent)
		if err != nil {
			lastErr = fmt.Errorf("obo token: %w", err)
			// A missing connection will not fix itself; fail fast.
			break
		}

		callCtx, cancel := context.WithTimeout(ctx, w.cfg.RequestTimout)
		ref, detail, err := exec.Execute(callCtx, intent, token)
		cancel()

		if err == nil {
			log.Info("dispatch succeeded", "external_ref", ref, "attempt", attempt)
			w.report(ctx, intent, "succeeded", &ref, detail, nil, attempt)
			return
		}

		lastErr = err
		if !retryable(err) {
			log.Warn("dispatch failed permanently", "error", err, "attempt", attempt)
			break
		}
		if attempt < w.cfg.MaxAttempts {
			d := w.backoff(attempt)
			log.Warn("dispatch failed, retrying", "error", err, "attempt", attempt, "backoff", d)
			w.report(ctx, intent, "retrying", nil, nil, errPtr(err), attempt)
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		}
	}

	log.Error("dispatch dead-lettered", "error", lastErr)
	w.report(ctx, intent, "dead_lettered", nil, nil, errPtr(lastErr), w.cfg.MaxAttempts)
}

func (w *Worker) resolveToken(ctx context.Context, intent Intent) (string, error) {
	if w.tokens == nil {
		return "", nil
	}
	return w.tokens.OBOToken(ctx, intent.OrganizationID, intent.ActingUserID, intent.IntegrationType)
}

// backoff is exponential with full jitter to avoid retry stampedes.
func (w *Worker) backoff(attempt int) time.Duration {
	exp := float64(w.cfg.BaseBackoff) * math.Pow(2, float64(attempt-1))
	if exp > float64(w.cfg.MaxBackoff) {
		exp = float64(w.cfg.MaxBackoff)
	}
	return time.Duration(rand.Int63n(int64(exp)) + int64(w.cfg.BaseBackoff))
}

// report calls back into core so the dispatch row and audit trail stay current.
func (w *Worker) report(ctx context.Context, intent Intent, status string, ref *string, detail map[string]any, errMsg *string, attempt int) {
	body := map[string]any{"status": status}
	if ref != nil && *ref != "" {
		body["external_ref"] = *ref
	}
	if detail != nil {
		body["detail"] = detail
		body["response_payload"] = detail
	}
	if errMsg != nil {
		body["error_message"] = *errMsg
	}
	if attempt > 0 {
		body["attempt_count"] = attempt
	}

	b, err := json.Marshal(body)
	if err != nil {
		return
	}

	url := fmt.Sprintf("%s/v1/dispatches/%s/result",
		strings.TrimRight(w.cfg.CoreBaseURL, "/"), intent.DispatchID)

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.cfg.ServiceToken)

	resp, err := w.client.Do(req)
	if err != nil {
		w.log.Warn("report dispatch result failed", "dispatch_id", intent.DispatchID, "error", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		w.log.Warn("report dispatch result rejected",
			"dispatch_id", intent.DispatchID, "status", resp.StatusCode)
	}
}

// ---- error classification ----

// RetryableError marks a transient failure worth retrying.
type RetryableError struct{ Err error }

func (e RetryableError) Error() string { return e.Err.Error() }
func (e RetryableError) Unwrap() error { return e.Err }

// PermanentError marks a failure that will not succeed on retry (4xx, config).
type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

func retryable(err error) bool {
	var perm PermanentError
	if asErr(err, &perm) {
		return false
	}
	return true
}

func asErr[T error](err error, target *T) bool {
	for err != nil {
		if v, ok := err.(T); ok {
			*target = v
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func errPtr(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	if len(s) > 1000 {
		s = s[:1000]
	}
	return &s
}

func verifyHMAC(payload []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
