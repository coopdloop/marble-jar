package store

import (
	"encoding/json"
	"time"
)

// Marble is the atomic unit of agent work — the product's central noun.
type Marble struct {
	ID              string          `json:"id" db:"id"`
	OrganizationID  string          `json:"organization_id" db:"organization_id"`
	ProjectID       *string         `json:"project_id" db:"project_id"`
	ObjectiveID     *string         `json:"objective_id" db:"objective_id"`
	AgentID         *string         `json:"agent_id" db:"agent_id"`
	CreatedByUserID *string         `json:"created_by_user_id" db:"created_by_user_id"`
	Summary         string          `json:"summary" db:"summary"`
	Model           *string         `json:"model" db:"model"`
	TokensIn        *int            `json:"tokens_in" db:"tokens_in"`
	TokensOut       *int            `json:"tokens_out" db:"tokens_out"`
	CostUSD         *float64        `json:"cost_usd" db:"cost_usd"`
	DurationMS      *int            `json:"duration_ms" db:"duration_ms"`
	TraceID         *string         `json:"trace_id" db:"trace_id"`
	PhoenixTraceURL *string         `json:"phoenix_trace_url" db:"phoenix_trace_url"`
	Status          string          `json:"status" db:"status"`
	Metadata        json.RawMessage `json:"metadata" db:"metadata"`
	Source          string          `json:"source" db:"source"`
	IdempotencyKey  *string         `json:"-" db:"idempotency_key"`
	OccurredAt      time.Time       `json:"occurred_at" db:"occurred_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`

	// Denormalized read-side fields (populated by list/get queries).
	ProjectName *string `json:"project_name,omitempty" db:"project_name"`
	AgentName   *string `json:"agent_name,omitempty" db:"agent_name"`
}

// Objective is a "bigger jar": a rollup bucket of marbles.
type Objective struct {
	ID              string     `json:"id" db:"id"`
	OrganizationID  string     `json:"organization_id" db:"organization_id"`
	ProjectID       *string    `json:"project_id" db:"project_id"`
	Title           string     `json:"title" db:"title"`
	Description     *string    `json:"description" db:"description"`
	Status          string     `json:"status" db:"status"`
	CreatedByUserID *string    `json:"created_by_user_id" db:"created_by_user_id"`
	BudgetTokens    *int64     `json:"budget_tokens" db:"budget_tokens"`
	BudgetCostUSD   *float64   `json:"budget_cost_usd" db:"budget_cost_usd"`
	ShippedAt       *time.Time `json:"shipped_at" db:"shipped_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	Rollup *Rollup `json:"rollup,omitempty" db:"-"`
}

// Rollup aggregates cost/time/token totals for an objective or time window.
type Rollup struct {
	MarbleCount   int      `json:"marble_count" db:"marble_count"`
	TokensIn      int64    `json:"tokens_in" db:"tokens_in"`
	TokensOut     int64    `json:"tokens_out" db:"tokens_out"`
	TotalTokens   int64    `json:"total_tokens" db:"total_tokens"`
	CostUSD       float64  `json:"cost_usd" db:"cost_usd"`
	DurationMS    int64    `json:"duration_ms" db:"duration_ms"`
	BudgetPctCost *float64 `json:"budget_pct_cost,omitempty" db:"-"`
	BudgetPctTok  *float64 `json:"budget_pct_tokens,omitempty" db:"-"`
}

// DispatchRule drives automated dispatch when a marble matches its condition.
type DispatchRule struct {
	ID              string          `json:"id" db:"id"`
	OrganizationID  string          `json:"organization_id" db:"organization_id"`
	ProjectID       *string         `json:"project_id" db:"project_id"`
	Name            string          `json:"name" db:"name"`
	Condition       json.RawMessage `json:"condition" db:"condition"`
	TargetType      string          `json:"target_type" db:"target_type"`
	TargetConfig    json.RawMessage `json:"target_config" db:"target_config"`
	IsActive        bool            `json:"is_active" db:"is_active"`
	CreatedByUserID *string         `json:"created_by_user_id" db:"created_by_user_id"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

// Dispatch is one outbound integration action, executed on-behalf-of a user.
type Dispatch struct {
	ID              string          `json:"id" db:"id"`
	OrganizationID  string          `json:"organization_id" db:"organization_id"`
	MarbleID        *string         `json:"marble_id" db:"marble_id"`
	ObjectiveID     *string         `json:"objective_id" db:"objective_id"`
	DispatchRuleID  *string         `json:"dispatch_rule_id" db:"dispatch_rule_id"`
	IntegrationType string          `json:"integration_type" db:"integration_type"`
	Action          string          `json:"action" db:"action"`
	ActingUserID    *string         `json:"acting_user_id" db:"acting_user_id"`
	Status          string          `json:"status" db:"status"`
	RequestPayload  json.RawMessage `json:"request_payload" db:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload" db:"response_payload"`
	ExternalRef     *string         `json:"external_ref" db:"external_ref"`
	ErrorMessage    *string         `json:"error_message" db:"error_message"`
	AttemptCount    int             `json:"attempt_count" db:"attempt_count"`
	DispatchedAt    *time.Time      `json:"dispatched_at" db:"dispatched_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at" db:"updated_at"`
}

type Organization struct {
	ID        string          `json:"id" db:"id"`
	Name      string          `json:"name" db:"name"`
	Slug      string          `json:"slug" db:"slug"`
	Settings  json.RawMessage `json:"settings" db:"settings"`
	CreatedAt time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt time.Time       `json:"updated_at" db:"updated_at"`
}

type User struct {
	ID              string    `json:"id" db:"id"`
	OrganizationID  string    `json:"organization_id" db:"organization_id"`
	Email           string    `json:"email" db:"email"`
	DisplayName     *string   `json:"display_name" db:"display_name"`
	AvatarURL       *string   `json:"avatar_url" db:"avatar_url"`
	Role            string    `json:"role" db:"role"`
	IsActive        bool      `json:"is_active" db:"is_active"`
	AuthProvider    string    `json:"auth_provider" db:"auth_provider"`
	ProviderSubject *string   `json:"-" db:"provider_subject"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

type Project struct {
	ID             string    `json:"id" db:"id"`
	OrganizationID string    `json:"organization_id" db:"organization_id"`
	Name           string    `json:"name" db:"name"`
	Slug           string    `json:"slug" db:"slug"`
	Description    *string   `json:"description" db:"description"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

type Agent struct {
	ID             string          `json:"id" db:"id"`
	OrganizationID string          `json:"organization_id" db:"organization_id"`
	Name           string          `json:"name" db:"name"`
	Harness        *string         `json:"harness" db:"harness"`
	DefaultModel   *string         `json:"default_model" db:"default_model"`
	Metadata       json.RawMessage `json:"metadata" db:"metadata"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at" db:"updated_at"`
}

type APIKey struct {
	ID              string     `json:"id" db:"id"`
	OrganizationID  string     `json:"organization_id" db:"organization_id"`
	CreatedByUserID *string    `json:"created_by_user_id" db:"created_by_user_id"`
	Name            string     `json:"name" db:"name"`
	KeyPrefix       string     `json:"key_prefix" db:"key_prefix"`
	HashedKey       string     `json:"-" db:"hashed_key"`
	Scopes          []string   `json:"scopes" db:"scopes"`
	LastUsedAt      *time.Time `json:"last_used_at" db:"last_used_at"`
	RevokedAt       *time.Time `json:"revoked_at" db:"revoked_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`
}

type AuditEntry struct {
	ID             string          `json:"id" db:"id"`
	OrganizationID string          `json:"organization_id" db:"organization_id"`
	ActingUserID   *string         `json:"acting_user_id" db:"acting_user_id"`
	AgentIdentity  *string         `json:"agent_identity" db:"agent_identity"`
	Provider       string          `json:"provider" db:"provider"`
	Action         string          `json:"action" db:"action"`
	MarbleID       *string         `json:"marble_id" db:"marble_id"`
	DispatchID     *string         `json:"dispatch_id" db:"dispatch_id"`
	Details        json.RawMessage `json:"details" db:"details"`
	CreatedAt      time.Time       `json:"created_at" db:"created_at"`
}

// TrendPoint is one bucket of a cost/token time series.
type TrendPoint struct {
	Bucket      time.Time `json:"bucket" db:"bucket"`
	MarbleCount int       `json:"marble_count" db:"marble_count"`
	CostUSD     float64   `json:"cost_usd" db:"cost_usd"`
	TokensIn    int64     `json:"tokens_in" db:"tokens_in"`
	TokensOut   int64     `json:"tokens_out" db:"tokens_out"`
	DurationMS  int64     `json:"duration_ms" db:"duration_ms"`
}

type OAuthConnection struct {
	ID                string  `json:"id" db:"id"`
	OrganizationID    string  `json:"organization_id" db:"organization_id"`
	UserID            string  `json:"user_id" db:"user_id"`
	Provider          string  `json:"provider" db:"provider"`
	ProviderAccountID *string `json:"provider_account_id" db:"provider_account_id"`
	// AccessToken and RefreshToken are plaintext in memory and sealed in the
	// database; json:"-" keeps them out of every API response.
	AccessToken  string     `json:"-" db:"access_token_encrypted"`
	RefreshToken *string    `json:"-" db:"refresh_token_encrypted"`
	Scopes       []string   `json:"scopes" db:"scopes"`
	ExpiresAt    *time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}
