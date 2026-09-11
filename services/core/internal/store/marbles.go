package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// CreateMarbleParams is the normalized ingestion payload.
type CreateMarbleParams struct {
	OrganizationID  string
	ProjectID       *string
	ObjectiveID     *string
	AgentID         *string
	CreatedByUserID *string
	Summary         string
	Model           *string
	TokensIn        *int
	TokensOut       *int
	CostUSD         *float64
	DurationMS      *int
	TraceID         *string
	PhoenixTraceURL *string
	Status          string
	Metadata        json.RawMessage
	Source          string
	IdempotencyKey  *string
	OccurredAt      time.Time
}

const marbleColumns = `
	m.id, m.organization_id, m.project_id, m.objective_id, m.agent_id, m.created_by_user_id,
	m.summary, m.model, m.tokens_in, m.tokens_out, m.cost_usd, m.duration_ms,
	m.trace_id, m.phoenix_trace_url, m.status, m.metadata, m.source, m.idempotency_key,
	m.occurred_at, m.created_at, m.updated_at,
	p.name AS project_name, a.name AS agent_name`

const marbleFrom = `
	FROM marbles m
	LEFT JOIN projects p ON p.id = m.project_id
	LEFT JOIN agents a ON a.id = m.agent_id`

func scanMarble(row pgx.Row) (*Marble, error) {
	var m Marble
	err := row.Scan(
		&m.ID, &m.OrganizationID, &m.ProjectID, &m.ObjectiveID, &m.AgentID, &m.CreatedByUserID,
		&m.Summary, &m.Model, &m.TokensIn, &m.TokensOut, &m.CostUSD, &m.DurationMS,
		&m.TraceID, &m.PhoenixTraceURL, &m.Status, &m.Metadata, &m.Source, &m.IdempotencyKey,
		&m.OccurredAt, &m.CreatedAt, &m.UpdatedAt,
		&m.ProjectName, &m.AgentName,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(m.Metadata) == 0 {
		m.Metadata = json.RawMessage(`{}`)
	}
	return &m, nil
}

// CreateMarble inserts a marble and mirrors its metrics into the time-series
// table. When an idempotency key replays, the existing marble is returned with
// created=false so ingestion stays safely retryable.
func (s *Store) CreateMarble(ctx context.Context, p CreateMarbleParams) (m *Marble, created bool, err error) {
	if p.Status == "" {
		p.Status = "logged"
	}
	if p.Source == "" {
		p.Source = "sdk"
	}
	if len(p.Metadata) == 0 {
		p.Metadata = json.RawMessage(`{}`)
	}
	if p.OccurredAt.IsZero() {
		p.OccurredAt = time.Now().UTC()
	}

	if p.IdempotencyKey != nil {
		existing, lookupErr := s.marbleByIdempotencyKey(ctx, p.OrganizationID, *p.IdempotencyKey)
		if lookupErr == nil {
			return existing, false, nil
		}
		if lookupErr != ErrNotFound {
			return nil, false, lookupErr
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO marbles (
			organization_id, project_id, objective_id, agent_id, created_by_user_id,
			summary, model, tokens_in, tokens_out, cost_usd, duration_ms,
			trace_id, phoenix_trace_url, status, metadata, source, idempotency_key, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id`,
		p.OrganizationID, p.ProjectID, p.ObjectiveID, p.AgentID, p.CreatedByUserID,
		p.Summary, p.Model, p.TokensIn, p.TokensOut, p.CostUSD, p.DurationMS,
		p.TraceID, p.PhoenixTraceURL, p.Status, p.Metadata, p.Source, p.IdempotencyKey, p.OccurredAt,
	).Scan(&id)
	if err != nil {
		if mapped := mapErr(err); mapped == ErrConflict && p.IdempotencyKey != nil {
			_ = tx.Rollback(ctx)
			existing, lookupErr := s.marbleByIdempotencyKey(ctx, p.OrganizationID, *p.IdempotencyKey)
			if lookupErr != nil {
				return nil, false, lookupErr
			}
			return existing, false, nil
		}
		return nil, false, mapErr(err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO marble_metrics (
			time, organization_id, marble_id, project_id, objective_id, model,
			tokens_in, tokens_out, cost_usd, duration_ms
		) VALUES ($1,$2,$3,$4,$5,$6,
			COALESCE($7,0), COALESCE($8,0), COALESCE($9,0), COALESCE($10,0))`,
		p.OccurredAt, p.OrganizationID, id, p.ProjectID, p.ObjectiveID, p.Model,
		p.TokensIn, p.TokensOut, p.CostUSD, p.DurationMS,
	); err != nil {
		return nil, false, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}

	m, err = s.GetMarble(ctx, p.OrganizationID, id)
	if err != nil {
		return nil, false, err
	}
	return m, true, nil
}

func (s *Store) marbleByIdempotencyKey(ctx context.Context, orgID, key string) (*Marble, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+marbleColumns+marbleFrom+
			` WHERE m.organization_id = $1 AND m.idempotency_key = $2`, orgID, key)
	return scanMarble(row)
}

func (s *Store) GetMarble(ctx context.Context, orgID, id string) (*Marble, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+marbleColumns+marbleFrom+
			` WHERE m.organization_id = $1 AND m.id = $2`, orgID, id)
	return scanMarble(row)
}

// ListMarblesFilter mirrors the query params on GET /v1/marbles.
type ListMarblesFilter struct {
	Project     string
	ObjectiveID string
	Model       string
	AgentID     string
	Status      string
	Search      string
	From        *time.Time
	To          *time.Time
	Unassigned  bool
	Limit       int
	Cursor      string
}

// ListMarbles returns a page of marbles ordered newest-first with an opaque
// keyset cursor (occurred_at, id) so live inserts never shift the page window.
func (s *Store) ListMarbles(ctx context.Context, orgID string, f ListMarblesFilter) ([]Marble, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}

	where := []string{"m.organization_id = $1"}
	args := []any{orgID}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	if f.Project != "" {
		add("(p.slug = $%d OR m.project_id::text = $%[1]d)", f.Project)
	}
	if f.ObjectiveID != "" {
		add("m.objective_id = $%d", f.ObjectiveID)
	}
	if f.Model != "" {
		add("m.model = $%d", f.Model)
	}
	if f.AgentID != "" {
		add("m.agent_id = $%d", f.AgentID)
	}
	if f.Status != "" {
		add("m.status = $%d", f.Status)
	}
	if f.Search != "" {
		add("m.summary ILIKE '%%' || $%d || '%%'", f.Search)
	}
	if f.From != nil {
		add("m.occurred_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("m.occurred_at <= $%d", *f.To)
	}
	if f.Unassigned {
		where = append(where, "m.objective_id IS NULL")
	}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(m.occurred_at, m.id) < ($%d, $%d)", len(args)-1, len(args)))
	}

	args = append(args, f.Limit+1)
	q := `SELECT ` + marbleColumns + marbleFrom +
		` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(` ORDER BY m.occurred_at DESC, m.id DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer rows.Close()

	out := make([]Marble, 0, f.Limit)
	for rows.Next() {
		m, err := scanMarble(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = encodeCursor(last.OccurredAt, last.ID)
	}
	return out, next, nil
}

// UpdateMarbleParams holds the PATCH-able subset of a marble.
type UpdateMarbleParams struct {
	Summary         *string
	Status          *string
	ObjectiveID     *string
	ClearObjective  bool
	ProjectID       *string
	Metadata        json.RawMessage
	TraceID         *string
	PhoenixTraceURL *string
}

func (s *Store) UpdateMarble(ctx context.Context, orgID, id string, p UpdateMarbleParams) (*Marble, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{orgID, id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if p.Summary != nil {
		add("summary", *p.Summary)
	}
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.ClearObjective {
		sets = append(sets, "objective_id = NULL")
	} else if p.ObjectiveID != nil {
		add("objective_id", *p.ObjectiveID)
	}
	if p.ProjectID != nil {
		add("project_id", *p.ProjectID)
	}
	if len(p.Metadata) > 0 {
		add("metadata", p.Metadata)
	}
	if p.TraceID != nil {
		add("trace_id", *p.TraceID)
	}
	if p.PhoenixTraceURL != nil {
		add("phoenix_trace_url", *p.PhoenixTraceURL)
	}

	q := fmt.Sprintf(
		`UPDATE marbles SET %s WHERE organization_id = $1 AND id = $2 RETURNING id`,
		strings.Join(sets, ", "))

	var updatedID string
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&updatedID); err != nil {
		return nil, mapErr(err)
	}

	// Keep the metrics hypertable consistent with objective reassignment.
	if p.ClearObjective || p.ObjectiveID != nil {
		var objID *string
		if !p.ClearObjective {
			objID = p.ObjectiveID
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE marble_metrics SET objective_id = $1 WHERE marble_id = $2`, objID, id); err != nil {
			return nil, err
		}
	}
	return s.GetMarble(ctx, orgID, id)
}

func (s *Store) DeleteMarble(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM marbles WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `DELETE FROM marble_metrics WHERE marble_id = $1`, id)
	return nil
}

// RollupParams is the phoenix_bridge-supplied cost/time/token rollup.
type RollupParams struct {
	TokensIn        *int
	TokensOut       *int
	CostUSD         *float64
	DurationMS      *int
	TraceID         *string
	PhoenixTraceURL *string
	PhoenixProject  *string
	SpanID          *string
	Metrics         json.RawMessage
}

// WriteMarbleRollup updates marble metrics and records the Phoenix trace link.
func (s *Store) WriteMarbleRollup(ctx context.Context, orgID, marbleID string, p RollupParams) (*Marble, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE marbles SET
			tokens_in = COALESCE($3, tokens_in),
			tokens_out = COALESCE($4, tokens_out),
			cost_usd = COALESCE($5, cost_usd),
			duration_ms = COALESCE($6, duration_ms),
			trace_id = COALESCE($7, trace_id),
			phoenix_trace_url = COALESCE($8, phoenix_trace_url),
			updated_at = NOW()
		WHERE organization_id = $1 AND id = $2`,
		orgID, marbleID, p.TokensIn, p.TokensOut, p.CostUSD, p.DurationMS, p.TraceID, p.PhoenixTraceURL)
	if err != nil {
		return nil, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}

	if _, err = tx.Exec(ctx, `
		UPDATE marble_metrics SET
			tokens_in = COALESCE($2, tokens_in),
			tokens_out = COALESCE($3, tokens_out),
			cost_usd = COALESCE($4, cost_usd),
			duration_ms = COALESCE($5, duration_ms)
		WHERE marble_id = $1`,
		marbleID, p.TokensIn, p.TokensOut, p.CostUSD, p.DurationMS); err != nil {
		return nil, err
	}

	if p.TraceID != nil && *p.TraceID != "" {
		metrics := p.Metrics
		if len(metrics) == 0 {
			metrics = json.RawMessage(`{}`)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO phoenix_trace_links (
				organization_id, marble_id, phoenix_project_name, trace_id, span_id,
				trace_url, rollup_metrics, synced_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
			orgID, marbleID, p.PhoenixProject, *p.TraceID, p.SpanID, p.PhoenixTraceURL, metrics,
		); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetMarble(ctx, orgID, marbleID)
}

// RecordIngestEvent persists the raw ingestion payload for replay/debugging.
func (s *Store) RecordIngestEvent(ctx context.Context, orgID string, marbleID *string, apiKeyID *string, raw json.RawMessage, status string, errMsg *string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO marble_events (organization_id, marble_id, api_key_id, raw_payload, ingest_status, error_message)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, marbleID, apiKeyID, raw, status, errMsg)
	return err
}

func encodeCursor(ts time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(ts.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return ts, parts[1], nil
}
