// Package dispatch turns a marble (or a shipped objective) into an outbound
// integration action. Core never calls Jira/Slack/Teams directly: it records a
// dispatch row, publishes a signed dispatch.intent event, and the worker
// executes it on-behalf-of the acting user.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
	"github.com/marble-jar/marble-jar/services/core/internal/rules"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
	"github.com/marble-jar/marble-jar/services/core/internal/telemetry"
)

// SupportedIntegrations are the target types the worker knows how to execute.
var SupportedIntegrations = map[string]bool{
	"jira": true, "slack": true, "teams": true, "webhook": true,
}

// Intent is the payload published on the dispatch.intent topic.
type Intent struct {
	DispatchID      string          `json:"dispatch_id"`
	OrganizationID  string          `json:"organization_id"`
	IntegrationType string          `json:"integration_type"`
	Action          string          `json:"action"`
	MarbleID        *string         `json:"marble_id,omitempty"`
	ObjectiveID     *string         `json:"objective_id,omitempty"`
	RuleID          *string         `json:"rule_id,omitempty"`
	ActingUserID    *string         `json:"acting_user_id,omitempty"`
	AgentIdentity   string          `json:"agent_identity,omitempty"`
	TargetConfig    json.RawMessage `json:"target_config"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Service struct {
	store      *store.Store
	publisher  eventbus.Publisher
	metrics    *telemetry.Metrics
	hmacSecret string
	log        *slog.Logger
}

func NewService(st *store.Store, pub eventbus.Publisher, metrics *telemetry.Metrics, hmacSecret string, log *slog.Logger) *Service {
	return &Service{store: st, publisher: pub, metrics: metrics, hmacSecret: hmacSecret, log: log}
}

// Request describes a manual dispatch from the UI.
type Request struct {
	IntegrationType string
	Action          string
	MarbleID        *string
	ObjectiveID     *string
	RuleID          *string
	TargetConfig    json.RawMessage
	Payload         json.RawMessage
}

// Create records the dispatch and publishes the intent for the worker.
func (s *Service) Create(ctx context.Context, p *auth.Principal, req Request) (*store.Dispatch, error) {
	if !SupportedIntegrations[req.IntegrationType] {
		return nil, fmt.Errorf("unsupported integration type %q", req.IntegrationType)
	}
	if req.Action == "" {
		req.Action = defaultAction(req.IntegrationType)
	}

	requestPayload := mergeRequestPayload(req.TargetConfig, req.Payload)

	d, err := s.store.CreateDispatch(ctx, store.CreateDispatchParams{
		OrganizationID:  p.OrganizationID,
		MarbleID:        req.MarbleID,
		ObjectiveID:     req.ObjectiveID,
		DispatchRuleID:  req.RuleID,
		IntegrationType: req.IntegrationType,
		Action:          req.Action,
		ActingUserID:    p.ActingUserID(),
		RequestPayload:  requestPayload,
	})
	if err != nil {
		return nil, err
	}

	intent := Intent{
		DispatchID:      d.ID,
		OrganizationID:  p.OrganizationID,
		IntegrationType: req.IntegrationType,
		Action:          req.Action,
		MarbleID:        req.MarbleID,
		ObjectiveID:     req.ObjectiveID,
		RuleID:          req.RuleID,
		ActingUserID:    p.ActingUserID(),
		AgentIdentity:   p.AgentIdentity,
		TargetConfig:    orEmptyJSON(req.TargetConfig),
		Payload:         orEmptyJSON(req.Payload),
		CreatedAt:       time.Now().UTC(),
	}

	ev, err := eventbus.NewEvent(eventbus.TopicDispatchIntent, p.OrganizationID, intent, s.hmacSecret)
	if err != nil {
		return nil, err
	}
	if err := s.publisher.Publish(ctx, eventbus.TopicDispatchIntent, p.OrganizationID, ev); err != nil {
		msg := "publish failed: " + err.Error()
		if _, rerr := s.store.RecordDispatchResult(ctx, p.OrganizationID, d.ID, store.DispatchResultParams{
			Status: "failed", ErrorMessage: &msg,
		}); rerr != nil {
			s.log.Error("record publish failure", "dispatch_id", d.ID, "error", rerr)
		}
		return nil, fmt.Errorf("queue dispatch: %w", err)
	}

	s.metrics.DispatchQueued(req.IntegrationType)

	// Every OBO-attributed action is auditable from the moment it is queued.
	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: p.OrganizationID,
		ActingUserID:   p.ActingUserID(),
		AgentIdentity:  agentIdentity(p),
		Provider:       req.IntegrationType,
		Action:         "dispatch.queued:" + req.Action,
		MarbleID:       req.MarbleID,
		DispatchID:     &d.ID,
		Details: map[string]any{
			"rule_id":      req.RuleID,
			"objective_id": req.ObjectiveID,
			"principal":    string(p.Kind),
			// Redacted: the audit log is readable by every member of the org, and
			// target_config carries live Slack/Teams webhook tokens.
			"target_config": redactTargetConfig(orEmptyJSON(req.TargetConfig)),
		},
	}); err != nil {
		s.log.Warn("audit record failed", "dispatch_id", d.ID, "error", err)
	}

	return d, nil
}

// EvaluateAndDispatch runs the rules engine for a newly ingested marble.
func (s *Service) EvaluateAndDispatch(ctx context.Context, p *auth.Principal, m *store.Marble) {
	all, err := s.store.ListRules(ctx, p.OrganizationID, true)
	if err != nil {
		s.log.Warn("load rules failed", "error", err)
		return
	}

	for _, r := range rules.Evaluate(all, m) {
		marbleID := m.ID
		ruleID := r.ID
		payload := MarblePayload(m)

		if _, err := s.Create(ctx, p, Request{
			IntegrationType: r.TargetType,
			Action:          defaultAction(r.TargetType),
			MarbleID:        &marbleID,
			RuleID:          &ruleID,
			TargetConfig:    r.TargetConfig,
			Payload:         payload,
		}); err != nil {
			s.log.Error("auto dispatch failed",
				"rule_id", r.ID, "marble_id", m.ID, "error", err)
			continue
		}
		s.log.Info("rule fired", "rule", r.Name, "marble_id", m.ID, "target", r.TargetType)
		s.metrics.RuleFired(r.TargetType)
	}
}

// stalePendingThreshold is how old an unconsumed pending dispatch must be
// before it counts as orphaned and becomes replayable.
const stalePendingThreshold = 2 * time.Minute

// Replay requeues a dispatch that failed permanently or was never consumed.
// The intent is rebuilt from the request payload captured at creation, so a
// replay retries exactly what was originally attempted.
func (s *Service) Replay(ctx context.Context, p *auth.Principal, dispatchID string) (*store.Dispatch, error) {
	d, err := s.store.ReplayDispatch(ctx, p.OrganizationID, dispatchID,
		time.Now().UTC().Add(-stalePendingThreshold))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("dispatch is not replayable (must be failed, dead_lettered, or an orphaned pending dispatch)")
		}
		return nil, err
	}

	// Unwrap the {target_config, payload} envelope written at creation.
	var stored struct {
		TargetConfig json.RawMessage `json:"target_config"`
		Payload      json.RawMessage `json:"payload"`
	}
	_ = json.Unmarshal(d.RequestPayload, &stored)

	intent := Intent{
		DispatchID:      d.ID,
		OrganizationID:  d.OrganizationID,
		IntegrationType: d.IntegrationType,
		Action:          d.Action,
		MarbleID:        d.MarbleID,
		ObjectiveID:     d.ObjectiveID,
		RuleID:          d.DispatchRuleID,
		ActingUserID:    d.ActingUserID,
		TargetConfig:    orEmptyJSON(stored.TargetConfig),
		Payload:         orEmptyJSON(stored.Payload),
		CreatedAt:       time.Now().UTC(),
	}

	ev, err := eventbus.NewEvent(eventbus.TopicDispatchIntent, p.OrganizationID, intent, s.hmacSecret)
	if err != nil {
		return nil, err
	}
	if err := s.publisher.Publish(ctx, eventbus.TopicDispatchIntent, p.OrganizationID, ev); err != nil {
		return nil, fmt.Errorf("requeue dispatch: %w", err)
	}

	s.metrics.DispatchQueued(d.IntegrationType)

	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: p.OrganizationID,
		ActingUserID:   p.ActingUserID(),
		AgentIdentity:  agentIdentity(p),
		Provider:       d.IntegrationType,
		Action:         "dispatch.replayed:" + d.Action,
		MarbleID:       d.MarbleID,
		DispatchID:     &d.ID,
		Details:        map[string]any{"replayed_by": string(p.Kind)},
	}); err != nil {
		s.log.Warn("audit replay failed", "dispatch_id", d.ID, "error", err)
	}

	s.log.Info("dispatch replayed", "dispatch_id", d.ID, "integration", d.IntegrationType)
	return d, nil
}

// RecordResult applies a worker callback and closes the audit loop.
func (s *Service) RecordResult(ctx context.Context, orgID, dispatchID string, p store.DispatchResultParams, detail map[string]any) (*store.Dispatch, error) {
	d, err := s.store.RecordDispatchResult(ctx, orgID, dispatchID, p)
	if err != nil {
		return nil, err
	}
	if len(detail) > 0 {
		if err := s.store.RecordIntegrationDetail(ctx, d.IntegrationType, d.ID, detail); err != nil {
			s.log.Warn("record integration detail failed", "dispatch_id", d.ID, "error", err)
		}
	}

	s.metrics.DispatchCompleted(d.IntegrationType, p.Status)

	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: orgID,
		ActingUserID:   d.ActingUserID,
		Provider:       d.IntegrationType,
		Action:         "dispatch." + p.Status + ":" + d.Action,
		MarbleID:       d.MarbleID,
		DispatchID:     &d.ID,
		Details: map[string]any{
			"external_ref": d.ExternalRef,
			"error":        p.ErrorMessage,
			"attempt":      d.AttemptCount,
		},
	}); err != nil {
		s.log.Warn("audit record failed", "dispatch_id", d.ID, "error", err)
	}

	// Mark the marble as dispatched so the queue reflects downstream state.
	if p.Status == "succeeded" && d.MarbleID != nil {
		status := "dispatched"
		if _, err := s.store.UpdateMarble(ctx, orgID, *d.MarbleID,
			store.UpdateMarbleParams{Status: &status}); err != nil {
			s.log.Warn("mark marble dispatched failed", "marble_id", *d.MarbleID, "error", err)
		}
	}
	return d, nil
}

// MarblePayload builds the canonical outbound body for a marble dispatch.
func MarblePayload(m *store.Marble) json.RawMessage {
	body := map[string]any{
		"marble_id":   m.ID,
		"summary":     m.Summary,
		"status":      m.Status,
		"occurred_at": m.OccurredAt,
		"source":      m.Source,
	}
	if m.Model != nil {
		body["model"] = *m.Model
	}
	if m.ProjectName != nil {
		body["project"] = *m.ProjectName
	}
	if m.AgentName != nil {
		body["agent"] = *m.AgentName
	}
	if m.CostUSD != nil {
		body["cost_usd"] = *m.CostUSD
	}
	if m.TokensIn != nil {
		body["tokens_in"] = *m.TokensIn
	}
	if m.TokensOut != nil {
		body["tokens_out"] = *m.TokensOut
	}
	if m.DurationMS != nil {
		body["duration_ms"] = *m.DurationMS
	}
	if m.TraceID != nil {
		body["trace_id"] = *m.TraceID
	}
	if m.PhoenixTraceURL != nil {
		body["trace_url"] = *m.PhoenixTraceURL
	}
	return store.RawJSON(body)
}

func defaultAction(integration string) string {
	switch integration {
	case "jira":
		return "create_issue"
	case "slack", "teams":
		return "post_message"
	case "webhook":
		return "post"
	}
	return "execute"
}

func agentIdentity(p *auth.Principal) *string {
	if p.AgentIdentity == "" {
		return nil
	}
	id := p.AgentIdentity
	return &id
}

func orEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func mergeRequestPayload(target, payload json.RawMessage) json.RawMessage {
	return store.RawJSON(map[string]any{
		"target_config": json.RawMessage(orEmptyJSON(target)),
		"payload":       json.RawMessage(orEmptyJSON(payload)),
	})
}
