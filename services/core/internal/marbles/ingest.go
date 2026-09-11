// Package marbles owns the ingestion hot path: normalize an agent-reported
// unit of work, persist it, publish to the durable backbone, broadcast to live
// dashboards, and evaluate auto-dispatch rules.
package marbles

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
	"github.com/marble-jar/marble-jar/services/core/internal/telemetry"
)

// LogMarbleRequest is the wire shape accepted by POST /v1/marbles. It tolerates
// both the spec's snake_case REST body and the SDK's camelCase field names.
type LogMarbleRequest struct {
	Summary      string  `json:"summary"`
	TaskSummary  string  `json:"task_summary"`
	SummaryAlt   string  `json:"taskSummary"`
	Model        string  `json:"model"`
	Project      string  `json:"project"`
	Agent        string  `json:"agent"`
	AgentID      string  `json:"agent_id"`
	AgentIDAlt   string  `json:"agentId"`
	Harness      string  `json:"harness"`
	Status       string  `json:"status"`
	Source       string  `json:"source"`
	ObjectiveID  *string `json:"objective_id"`
	ObjectiveID2 *string `json:"objectiveId"`

	Tokens *struct {
		In  *int `json:"in"`
		Out *int `json:"out"`
	} `json:"tokens"`
	TokensInput  *int `json:"tokens_input"`
	TokensOutput *int `json:"tokens_output"`
	TokensInAlt  *int `json:"tokensInput"`
	TokensOutAlt *int `json:"tokensOutput"`

	CostUSD    *float64 `json:"cost_usd"`
	CostUSDAlt *float64 `json:"costUsd"`

	DurationMS    *int `json:"duration_ms"`
	DurationMSAlt *int `json:"durationMs"`

	TraceID    string `json:"trace_id"`
	TraceIDAlt string `json:"traceId"`

	PhoenixTraceURL string `json:"phoenix_trace_url"`

	OccurredAt *time.Time `json:"occurred_at"`

	Metadata json.RawMessage `json:"metadata"`

	IdempotencyKey    string `json:"idempotency_key"`
	IdempotencyKeyAlt string `json:"idempotencyKey"`
}

// normalize folds the alias fields into canonical values and validates.
func (r *LogMarbleRequest) normalize() error {
	if r.Summary == "" {
		r.Summary = firstNonEmpty(r.TaskSummary, r.SummaryAlt)
	}
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		return fmt.Errorf("summary is required")
	}
	if len(r.Summary) > 2000 {
		r.Summary = r.Summary[:2000]
	}

	r.Model = strings.TrimSpace(r.Model)
	if r.Model == "" {
		return fmt.Errorf("model is required")
	}

	if r.AgentID == "" {
		r.AgentID = r.AgentIDAlt
	}
	if r.TraceID == "" {
		r.TraceID = r.TraceIDAlt
	}
	if r.IdempotencyKey == "" {
		r.IdempotencyKey = r.IdempotencyKeyAlt
	}
	if r.ObjectiveID == nil {
		r.ObjectiveID = r.ObjectiveID2
	}
	if r.CostUSD == nil {
		r.CostUSD = r.CostUSDAlt
	}
	if r.DurationMS == nil {
		r.DurationMS = r.DurationMSAlt
	}

	if r.Tokens != nil {
		if r.TokensInput == nil {
			r.TokensInput = r.Tokens.In
		}
		if r.TokensOutput == nil {
			r.TokensOutput = r.Tokens.Out
		}
	}
	if r.TokensInput == nil {
		r.TokensInput = r.TokensInAlt
	}
	if r.TokensOutput == nil {
		r.TokensOutput = r.TokensOutAlt
	}

	switch r.Status {
	case "", "completed", "logged":
		r.Status = "logged"
	case "failed", "partial", "dispatched":
		// allowed as-is
	default:
		return fmt.Errorf("invalid status %q", r.Status)
	}

	if r.Source == "" {
		r.Source = "sdk"
	}
	if r.CostUSD != nil && *r.CostUSD < 0 {
		return fmt.Errorf("cost_usd must be non-negative")
	}
	if len(r.Metadata) == 0 {
		r.Metadata = json.RawMessage(`{}`)
	} else if !json.Valid(r.Metadata) {
		return fmt.Errorf("metadata must be valid JSON")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// RuleDispatcher is implemented by the dispatch service; kept as an interface
// so ingestion does not depend on integration plumbing.
type RuleDispatcher interface {
	EvaluateAndDispatch(ctx context.Context, p *auth.Principal, m *store.Marble)
}

// Ingestor is the ingestion pipeline.
type Ingestor struct {
	store       *store.Store
	publisher   eventbus.Publisher
	broadcaster eventbus.Broadcaster
	dispatcher  RuleDispatcher
	metrics     *telemetry.Metrics
	phoenixBase string
	hmacSecret  string
	log         *slog.Logger
}

func NewIngestor(
	st *store.Store,
	pub eventbus.Publisher,
	bc eventbus.Broadcaster,
	metrics *telemetry.Metrics,
	phoenixBase, hmacSecret string,
	log *slog.Logger,
) *Ingestor {
	return &Ingestor{
		store: st, publisher: pub, broadcaster: bc, metrics: metrics,
		phoenixBase: phoenixBase, hmacSecret: hmacSecret, log: log,
	}
}

// SetDispatcher wires the rules engine after construction (avoids a cycle).
func (i *Ingestor) SetDispatcher(d RuleDispatcher) { i.dispatcher = d }

func (i *Ingestor) Broadcaster() eventbus.Broadcaster { return i.broadcaster }

// Result reports whether the marble was newly created or an idempotent replay.
type Result struct {
	Marble  *store.Marble
	Created bool
}

// Ingest runs the full pipeline for one reported unit of work.
func (i *Ingestor) Ingest(ctx context.Context, p *auth.Principal, req *LogMarbleRequest, raw json.RawMessage) (*Result, error) {
	start := time.Now()
	if err := req.normalize(); err != nil {
		i.recordEvent(ctx, p, nil, raw, "rejected", err.Error())
		i.metrics.IngestRejected()
		return nil, err
	}

	params := store.CreateMarbleParams{
		OrganizationID:  p.OrganizationID,
		ObjectiveID:     req.ObjectiveID,
		CreatedByUserID: p.ActingUserID(),
		Summary:         req.Summary,
		Model:           &req.Model,
		TokensIn:        req.TokensInput,
		TokensOut:       req.TokensOutput,
		CostUSD:         req.CostUSD,
		DurationMS:      req.DurationMS,
		Status:          req.Status,
		Metadata:        req.Metadata,
		Source:          req.Source,
	}
	if req.OccurredAt != nil {
		params.OccurredAt = *req.OccurredAt
	}
	if req.IdempotencyKey != "" {
		params.IdempotencyKey = &req.IdempotencyKey
	}
	if req.TraceID != "" {
		params.TraceID = &req.TraceID
		url := req.PhoenixTraceURL
		if url == "" && i.phoenixBase != "" {
			url = fmt.Sprintf("%s/v1/traces/%s", i.phoenixBase, req.TraceID)
		}
		if url != "" {
			params.PhoenixTraceURL = &url
		}
	}

	// Lazily resolve project/agent identities from the free-form names agents send.
	if req.Project != "" {
		if proj, err := i.store.UpsertProjectBySlug(ctx, p.OrganizationID, req.Project); err == nil {
			params.ProjectID = &proj.ID
		} else {
			i.log.Warn("resolve project failed", "project", req.Project, "error", err)
		}
	}
	agentName := firstNonEmpty(req.Agent, p.AgentIdentity)
	if req.AgentID != "" {
		params.AgentID = &req.AgentID
	} else if agentName != "" {
		if ag, err := i.store.UpsertAgentByName(ctx, p.OrganizationID, agentName, req.Harness, req.Model); err == nil {
			params.AgentID = &ag.ID
		} else {
			i.log.Warn("resolve agent failed", "agent", agentName, "error", err)
		}
	}

	m, created, err := i.store.CreateMarble(ctx, params)
	if err != nil {
		i.recordEvent(ctx, p, nil, raw, "failed", err.Error())
		i.metrics.IngestFailed()
		return nil, err
	}

	i.recordEvent(ctx, p, &m.ID, raw, "accepted", "")
	i.metrics.IngestAccepted(time.Since(start))

	if !created {
		// Idempotent replay: no duplicate events, no duplicate dispatch.
		return &Result{Marble: m, Created: false}, nil
	}

	// Durable backbone: replayable log for analytics + downstream consumers.
	if ev, err := eventbus.NewEvent(eventbus.TopicMarbleLogged, p.OrganizationID, m, i.hmacSecret); err == nil {
		if err := i.publisher.Publish(ctx, eventbus.TopicMarbleLogged, p.OrganizationID, ev); err != nil {
			i.log.Warn("publish marble.logged failed", "marble_id", m.ID, "error", err)
		}
	}

	// Ephemeral fanout: the live jar animation.
	if ev, err := eventbus.NewEvent("marble.created", p.OrganizationID, m, ""); err == nil {
		if err := i.broadcaster.Broadcast(ctx, ev); err != nil {
			i.log.Warn("broadcast marble.created failed", "marble_id", m.ID, "error", err)
		}
	}

	// Rules evaluation runs off the hot path so ingestion latency stays flat.
	if i.dispatcher != nil {
		go func(marble store.Marble, principal auth.Principal) {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			i.dispatcher.EvaluateAndDispatch(bg, &principal, &marble)
		}(*m, *p)
	}

	return &Result{Marble: m, Created: true}, nil
}

func (i *Ingestor) recordEvent(ctx context.Context, p *auth.Principal, marbleID *string, raw json.RawMessage, status, errMsg string) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var ep *string
	if errMsg != "" {
		ep = &errMsg
	}
	var keyID *string
	if p.APIKeyID != "" {
		keyID = &p.APIKeyID
	}
	if err := i.store.RecordIngestEvent(ctx, p.OrganizationID, marbleID, keyID, raw, status, ep); err != nil {
		i.log.Warn("record ingest event failed", "error", err)
	}
}
