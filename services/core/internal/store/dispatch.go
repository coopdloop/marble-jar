package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- dispatch rules ----------

const ruleColumns = `
	id, organization_id, project_id, name, condition, target_type, target_config,
	is_active, created_by_user_id, created_at, updated_at`

func scanRule(row pgx.Row) (*DispatchRule, error) {
	var r DispatchRule
	err := row.Scan(&r.ID, &r.OrganizationID, &r.ProjectID, &r.Name, &r.Condition,
		&r.TargetType, &r.TargetConfig, &r.IsActive, &r.CreatedByUserID,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &r, nil
}

type CreateRuleParams struct {
	OrganizationID  string
	ProjectID       *string
	Name            string
	Condition       json.RawMessage
	TargetType      string
	TargetConfig    json.RawMessage
	IsActive        bool
	CreatedByUserID *string
}

func (s *Store) CreateRule(ctx context.Context, p CreateRuleParams) (*DispatchRule, error) {
	if len(p.Condition) == 0 {
		p.Condition = json.RawMessage(`{}`)
	}
	if len(p.TargetConfig) == 0 {
		p.TargetConfig = json.RawMessage(`{}`)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO dispatch_rules (
			organization_id, project_id, name, condition, target_type,
			target_config, is_active, created_by_user_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+ruleColumns,
		p.OrganizationID, p.ProjectID, p.Name, p.Condition, p.TargetType,
		p.TargetConfig, p.IsActive, p.CreatedByUserID)
	return scanRule(row)
}

func (s *Store) GetRule(ctx context.Context, orgID, id string) (*DispatchRule, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM dispatch_rules WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	return scanRule(row)
}

func (s *Store) ListRules(ctx context.Context, orgID string, activeOnly bool) ([]DispatchRule, error) {
	q := `SELECT ` + ruleColumns + ` FROM dispatch_rules WHERE organization_id = $1`
	if activeOnly {
		q += ` AND is_active = TRUE`
	}
	q += ` ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, q, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []DispatchRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

type UpdateRuleParams struct {
	Name         *string
	Condition    json.RawMessage
	TargetType   *string
	TargetConfig json.RawMessage
	IsActive     *bool
	ProjectID    *string
}

func (s *Store) UpdateRule(ctx context.Context, orgID, id string, p UpdateRuleParams) (*DispatchRule, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{orgID, id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Name != nil {
		add("name", *p.Name)
	}
	if len(p.Condition) > 0 {
		add("condition", p.Condition)
	}
	if p.TargetType != nil {
		add("target_type", *p.TargetType)
	}
	if len(p.TargetConfig) > 0 {
		add("target_config", p.TargetConfig)
	}
	if p.IsActive != nil {
		add("is_active", *p.IsActive)
	}
	if p.ProjectID != nil {
		add("project_id", *p.ProjectID)
	}

	q := fmt.Sprintf(`UPDATE dispatch_rules SET %s WHERE organization_id = $1 AND id = $2 RETURNING `+ruleColumns,
		strings.Join(sets, ", "))
	return scanRule(s.pool.QueryRow(ctx, q, args...))
}

func (s *Store) DeleteRule(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM dispatch_rules WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- dispatches ----------

const dispatchColumns = `
	id, organization_id, marble_id, objective_id, dispatch_rule_id, integration_type,
	action, acting_user_id, status, request_payload, response_payload, external_ref,
	error_message, attempt_count, dispatched_at, created_at, updated_at`

func scanDispatch(row pgx.Row) (*Dispatch, error) {
	var d Dispatch
	err := row.Scan(&d.ID, &d.OrganizationID, &d.MarbleID, &d.ObjectiveID, &d.DispatchRuleID,
		&d.IntegrationType, &d.Action, &d.ActingUserID, &d.Status, &d.RequestPayload,
		&d.ResponsePayload, &d.ExternalRef, &d.ErrorMessage, &d.AttemptCount,
		&d.DispatchedAt, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

type CreateDispatchParams struct {
	OrganizationID  string
	MarbleID        *string
	ObjectiveID     *string
	DispatchRuleID  *string
	IntegrationType string
	Action          string
	ActingUserID    *string
	RequestPayload  json.RawMessage
}

func (s *Store) CreateDispatch(ctx context.Context, p CreateDispatchParams) (*Dispatch, error) {
	if len(p.RequestPayload) == 0 {
		p.RequestPayload = json.RawMessage(`{}`)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO dispatches (
			organization_id, marble_id, objective_id, dispatch_rule_id,
			integration_type, action, acting_user_id, status, request_payload
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8)
		RETURNING `+dispatchColumns,
		p.OrganizationID, p.MarbleID, p.ObjectiveID, p.DispatchRuleID,
		p.IntegrationType, p.Action, p.ActingUserID, p.RequestPayload)
	return scanDispatch(row)
}

func (s *Store) GetDispatch(ctx context.Context, orgID, id string) (*Dispatch, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+dispatchColumns+` FROM dispatches WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	return scanDispatch(row)
}

type ListDispatchesFilter struct {
	Status          string
	IntegrationType string
	MarbleID        string
	ObjectiveID     string
	Limit           int
	Cursor          string
}

func (s *Store) ListDispatches(ctx context.Context, orgID string, f ListDispatchesFilter) ([]Dispatch, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := []string{"organization_id = $1"}
	args := []any{orgID}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.IntegrationType != "" {
		add("integration_type = $%d", f.IntegrationType)
	}
	if f.MarbleID != "" {
		add("marble_id = $%d", f.MarbleID)
	}
	if f.ObjectiveID != "" {
		add("objective_id = $%d", f.ObjectiveID)
	}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(created_at, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)

	q := `SELECT ` + dispatchColumns + ` FROM dispatches WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer rows.Close()

	out := []Dispatch{}
	for rows.Next() {
		d, err := scanDispatch(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, next, nil
}

// DispatchResultParams is the worker callback payload.
type DispatchResultParams struct {
	Status          string
	ExternalRef     *string
	ResponsePayload json.RawMessage
	ErrorMessage    *string
	AttemptCount    *int
}

func (s *Store) RecordDispatchResult(ctx context.Context, orgID, id string, p DispatchResultParams) (*Dispatch, error) {
	var dispatchedAt *time.Time
	if p.Status == "succeeded" {
		now := time.Now().UTC()
		dispatchedAt = &now
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE dispatches SET
			status = $3,
			external_ref = COALESCE($4, external_ref),
			response_payload = COALESCE($5, response_payload),
			error_message = $6,
			attempt_count = COALESCE($7, attempt_count + 1),
			dispatched_at = COALESCE($8, dispatched_at),
			updated_at = NOW()
		WHERE organization_id = $1 AND id = $2
		RETURNING `+dispatchColumns,
		orgID, id, p.Status, p.ExternalRef, p.ResponsePayload, p.ErrorMessage,
		p.AttemptCount, dispatchedAt)
	return scanDispatch(row)
}

// RecordIntegrationDetail writes the per-integration detail row (jira/teams/slack/webhook).
func (s *Store) RecordIntegrationDetail(ctx context.Context, integration, dispatchID string, detail map[string]any) error {
	switch integration {
	case "jira":
		_, err := s.pool.Exec(ctx, `
			INSERT INTO jira_dispatch_details (dispatch_id, jira_site_id, issue_key, issue_type, project_key, action_type)
			VALUES ($1,$2,$3,$4,$5,COALESCE($6,'create'))
			ON CONFLICT (dispatch_id) DO UPDATE SET
				issue_key = EXCLUDED.issue_key, updated_at = NOW()`,
			dispatchID, str(detail["site_id"]), str(detail["issue_key"]),
			str(detail["issue_type"]), str(detail["project_key"]), str(detail["action_type"]))
		return err
	case "teams":
		_, err := s.pool.Exec(ctx, `
			INSERT INTO teams_dispatch_details (dispatch_id, team_id, channel_id, message_id)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (dispatch_id) DO UPDATE SET
				message_id = EXCLUDED.message_id, updated_at = NOW()`,
			dispatchID, str(detail["team_id"]), str(detail["channel_id"]), str(detail["message_id"]))
		return err
	case "slack":
		_, err := s.pool.Exec(ctx, `
			INSERT INTO slack_dispatch_details (dispatch_id, slack_team_id, channel_id, message_ts)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (dispatch_id) DO UPDATE SET
				message_ts = EXCLUDED.message_ts, updated_at = NOW()`,
			dispatchID, str(detail["team_id"]), str(detail["channel_id"]), str(detail["message_ts"]))
		return err
	case "webhook":
		var httpStatus *int
		if v, ok := detail["http_status"].(float64); ok {
			iv := int(v)
			httpStatus = &iv
		} else if v, ok := detail["http_status"].(int); ok {
			httpStatus = &v
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO webhook_dispatch_details (dispatch_id, target_url, http_status, signature, response_body)
			VALUES ($1,COALESCE($2,''),$3,$4,$5)
			ON CONFLICT (dispatch_id) DO UPDATE SET
				http_status = EXCLUDED.http_status, response_body = EXCLUDED.response_body, updated_at = NOW()`,
			dispatchID, str(detail["target_url"]), httpStatus, str(detail["signature"]), str(detail["response_body"]))
		return err
	}
	return nil
}

func str(v any) *string {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

// ---------- webhook endpoints ----------

type WebhookEndpoint struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Secret         string    `json:"-"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *Store) ListWebhookEndpoints(ctx context.Context, orgID string) ([]WebhookEndpoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, name, url, secret, is_active, created_at, updated_at
		FROM webhook_endpoints WHERE organization_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []WebhookEndpoint{}
	for rows.Next() {
		var w WebhookEndpoint
		if err := rows.Scan(&w.ID, &w.OrganizationID, &w.Name, &w.URL, &w.Secret,
			&w.IsActive, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) CreateWebhookEndpoint(ctx context.Context, orgID, name, url, secret string) (*WebhookEndpoint, error) {
	var w WebhookEndpoint
	err := s.pool.QueryRow(ctx, `
		INSERT INTO webhook_endpoints (organization_id, name, url, secret)
		VALUES ($1,$2,$3,$4)
		RETURNING id, organization_id, name, url, secret, is_active, created_at, updated_at`,
		orgID, name, url, secret,
	).Scan(&w.ID, &w.OrganizationID, &w.Name, &w.URL, &w.Secret, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &w, nil
}

func (s *Store) DeleteWebhookEndpoint(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM webhook_endpoints WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
