package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AuditParams records an OBO-attributed action for the audit trail.
type AuditParams struct {
	OrganizationID string
	ActingUserID   *string
	AgentIdentity  *string
	Provider       string
	Action         string
	MarbleID       *string
	DispatchID     *string
	Details        map[string]any
}

func (s *Store) RecordAudit(ctx context.Context, p AuditParams) error {
	details := json.RawMessage(`{}`)
	if p.Details != nil {
		if b, err := json.Marshal(p.Details); err == nil {
			details = b
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO obo_audit_log (
			organization_id, acting_user_id, agent_identity, provider, action,
			marble_id, dispatch_id, details
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.OrganizationID, p.ActingUserID, p.AgentIdentity, p.Provider, p.Action,
		p.MarbleID, p.DispatchID, details)
	return err
}

type ListAuditFilter struct {
	Provider   string
	Action     string
	MarbleID   string
	DispatchID string
	Limit      int
	Cursor     string
}

func (s *Store) ListAudit(ctx context.Context, orgID string, f ListAuditFilter) ([]AuditEntry, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := []string{"organization_id = $1"}
	args := []any{orgID}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Provider != "" {
		add("provider = $%d", f.Provider)
	}
	if f.Action != "" {
		add("action = $%d", f.Action)
	}
	if f.MarbleID != "" {
		add("marble_id = $%d", f.MarbleID)
	}
	if f.DispatchID != "" {
		add("dispatch_id = $%d", f.DispatchID)
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

	q := `
		SELECT id, organization_id, acting_user_id, agent_identity, provider, action,
		       marble_id, dispatch_id, details, created_at
		FROM obo_audit_log WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer rows.Close()

	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.ActingUserID, &a.AgentIdentity,
			&a.Provider, &a.Action, &a.MarbleID, &a.DispatchID, &a.Details, &a.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, a)
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

func (s *Store) GetAuditEntry(ctx context.Context, orgID, id string) (*AuditEntry, error) {
	var a AuditEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, acting_user_id, agent_identity, provider, action,
		       marble_id, dispatch_id, details, created_at
		FROM obo_audit_log WHERE organization_id = $1 AND id = $2`, orgID, id,
	).Scan(&a.ID, &a.OrganizationID, &a.ActingUserID, &a.AgentIdentity, &a.Provider,
		&a.Action, &a.MarbleID, &a.DispatchID, &a.Details, &a.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// ---------- trends ----------

// TrendFilter drives GET /v1/trends/cost and /v1/trends/tokens.
type TrendFilter struct {
	Interval    string
	Days        int
	ProjectID   string
	ObjectiveID string
	Model       string
}

func normalizeInterval(iv string) string {
	switch strings.ToLower(iv) {
	case "hour", "hourly":
		return "hour"
	case "week", "weekly":
		return "week"
	case "month", "monthly":
		return "month"
	default:
		return "day"
	}
}

// Trends aggregates cost/token/duration buckets over a rolling window.
func (s *Store) Trends(ctx context.Context, orgID string, f TrendFilter) ([]TrendPoint, error) {
	if f.Days <= 0 || f.Days > 365 {
		f.Days = 30
	}
	iv := normalizeInterval(f.Interval)

	// $2 multiplies an interval rather than being concatenated into text, so pgx
	// can encode it as a plain integer.
	where := []string{
		"organization_id = $1",
		"occurred_at >= NOW() - ($2::int * interval '1 day')",
	}
	args := []any{orgID, f.Days}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.ProjectID != "" {
		add("project_id = $%d", f.ProjectID)
	}
	if f.ObjectiveID != "" {
		add("objective_id = $%d", f.ObjectiveID)
	}
	if f.Model != "" {
		add("model = $%d", f.Model)
	}
	args = append(args, iv)

	q := fmt.Sprintf(`
		SELECT date_trunc($%d, occurred_at) AS bucket,
		       COUNT(*),
		       COALESCE(SUM(cost_usd),0),
		       COALESCE(SUM(tokens_in),0),
		       COALESCE(SUM(tokens_out),0),
		       COALESCE(SUM(duration_ms),0)
		FROM marbles WHERE %s
		GROUP BY bucket ORDER BY bucket ASC`, len(args), strings.Join(where, " AND "))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return scanTrend(rows)
}

func scanTrend(rows pgx.Rows) ([]TrendPoint, error) {
	out := []TrendPoint{}
	for rows.Next() {
		var t TrendPoint
		if err := rows.Scan(&t.Bucket, &t.MarbleCount, &t.CostUSD,
			&t.TokensIn, &t.TokensOut, &t.DurationMS); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// JarStatus powers the monitor MCP's get_jar_status tool.
type JarStatus struct {
	MarblesTotal   int      `json:"marbles_total"`
	MarblesToday   int      `json:"marbles_today"`
	MarblesLastHr  int      `json:"marbles_last_hour"`
	CostTodayUSD   float64  `json:"cost_today_usd"`
	TokensToday    int64    `json:"tokens_today"`
	OpenObjectives int      `json:"open_objectives"`
	PendingDisp    int      `json:"pending_dispatches"`
	TopModels      []string `json:"top_models"`
}

func (s *Store) JarStatus(ctx context.Context, orgID string) (*JarStatus, error) {
	var j JarStatus
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM marbles WHERE organization_id = $1),
			(SELECT COUNT(*) FROM marbles WHERE organization_id = $1 AND occurred_at >= date_trunc('day', NOW())),
			(SELECT COUNT(*) FROM marbles WHERE organization_id = $1 AND occurred_at >= NOW() - interval '1 hour'),
			(SELECT COALESCE(SUM(cost_usd),0) FROM marbles WHERE organization_id = $1 AND occurred_at >= date_trunc('day', NOW())),
			(SELECT COALESCE(SUM(COALESCE(tokens_in,0) + COALESCE(tokens_out,0)),0) FROM marbles WHERE organization_id = $1 AND occurred_at >= date_trunc('day', NOW())),
			(SELECT COUNT(*) FROM objectives WHERE organization_id = $1 AND status = 'open'),
			(SELECT COUNT(*) FROM dispatches WHERE organization_id = $1 AND status IN ('pending','retrying'))`,
		orgID,
	).Scan(&j.MarblesTotal, &j.MarblesToday, &j.MarblesLastHr, &j.CostTodayUSD,
		&j.TokensToday, &j.OpenObjectives, &j.PendingDisp)
	if err != nil {
		return nil, mapErr(err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT model FROM marbles
		WHERE organization_id = $1 AND model IS NOT NULL
		  AND occurred_at >= NOW() - interval '7 days'
		GROUP BY model ORDER BY COUNT(*) DESC LIMIT 5`, orgID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	j.TopModels = []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		j.TopModels = append(j.TopModels, m)
	}
	return &j, rows.Err()
}
