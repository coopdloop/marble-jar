package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const objectiveColumns = `
	id, organization_id, project_id, title, description, status, created_by_user_id,
	budget_tokens, budget_cost_usd, shipped_at, created_at, updated_at`

func scanObjective(row pgx.Row) (*Objective, error) {
	var o Objective
	err := row.Scan(
		&o.ID, &o.OrganizationID, &o.ProjectID, &o.Title, &o.Description, &o.Status,
		&o.CreatedByUserID, &o.BudgetTokens, &o.BudgetCostUSD, &o.ShippedAt,
		&o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

type CreateObjectiveParams struct {
	OrganizationID  string
	ProjectID       *string
	Title           string
	Description     *string
	Status          string
	CreatedByUserID *string
	BudgetTokens    *int64
	BudgetCostUSD   *float64
}

func (s *Store) CreateObjective(ctx context.Context, p CreateObjectiveParams) (*Objective, error) {
	if p.Status == "" {
		p.Status = "open"
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO objectives (
			organization_id, project_id, title, description, status,
			created_by_user_id, budget_tokens, budget_cost_usd
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+objectiveColumns,
		p.OrganizationID, p.ProjectID, p.Title, p.Description, p.Status,
		p.CreatedByUserID, p.BudgetTokens, p.BudgetCostUSD)
	return scanObjective(row)
}

func (s *Store) GetObjective(ctx context.Context, orgID, id string) (*Objective, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+objectiveColumns+` FROM objectives WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	o, err := scanObjective(row)
	if err != nil {
		return nil, err
	}
	rollup, err := s.ObjectiveRollup(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	applyBudget(o, rollup)
	o.Rollup = rollup
	return o, nil
}

type ListObjectivesFilter struct {
	Status  string
	Project string
	Limit   int
	Cursor  string
}

// ListObjectives returns objectives with their rollups attached in a single
// aggregate query so the Objectives grid renders without an N+1 fetch.
func (s *Store) ListObjectives(ctx context.Context, orgID string, f ListObjectivesFilter) ([]Objective, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}

	where := []string{"o.organization_id = $1"}
	args := []any{orgID}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.Status != "" {
		add("o.status = $%d", f.Status)
	}
	if f.Project != "" {
		add("(p.slug = $%d OR o.project_id::text = $%[1]d)", f.Project)
	}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, ts, id)
		where = append(where, fmt.Sprintf("(o.created_at, o.id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, f.Limit+1)

	q := `
		SELECT o.id, o.organization_id, o.project_id, o.title, o.description, o.status,
		       o.created_by_user_id, o.budget_tokens, o.budget_cost_usd, o.shipped_at,
		       o.created_at, o.updated_at,
		       COUNT(m.id) AS marble_count,
		       COALESCE(SUM(m.tokens_in),0) AS tokens_in,
		       COALESCE(SUM(m.tokens_out),0) AS tokens_out,
		       COALESCE(SUM(m.cost_usd),0) AS cost_usd,
		       COALESCE(SUM(m.duration_ms),0) AS duration_ms
		FROM objectives o
		LEFT JOIN projects p ON p.id = o.project_id
		LEFT JOIN marbles m ON m.objective_id = o.id
		WHERE ` + strings.Join(where, " AND ") + `
		GROUP BY o.id
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $` + fmt.Sprint(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer rows.Close()

	out := make([]Objective, 0, f.Limit)
	for rows.Next() {
		var o Objective
		var r Rollup
		if err := rows.Scan(
			&o.ID, &o.OrganizationID, &o.ProjectID, &o.Title, &o.Description, &o.Status,
			&o.CreatedByUserID, &o.BudgetTokens, &o.BudgetCostUSD, &o.ShippedAt,
			&o.CreatedAt, &o.UpdatedAt,
			&r.MarbleCount, &r.TokensIn, &r.TokensOut, &r.CostUSD, &r.DurationMS,
		); err != nil {
			return nil, "", err
		}
		r.TotalTokens = r.TokensIn + r.TokensOut
		applyBudget(&o, &r)
		rc := r
		o.Rollup = &rc
		out = append(out, o)
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

func (s *Store) ObjectiveRollup(ctx context.Context, orgID, objectiveID string) (*Rollup, error) {
	var r Rollup
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(tokens_in),0),
		       COALESCE(SUM(tokens_out),0),
		       COALESCE(SUM(cost_usd),0),
		       COALESCE(SUM(duration_ms),0)
		FROM marbles
		WHERE organization_id = $1 AND objective_id = $2`,
		orgID, objectiveID,
	).Scan(&r.MarbleCount, &r.TokensIn, &r.TokensOut, &r.CostUSD, &r.DurationMS)
	if err != nil {
		return nil, mapErr(err)
	}
	r.TotalTokens = r.TokensIn + r.TokensOut
	return &r, nil
}

// applyBudget computes percent-of-budget consumption for progress bars.
func applyBudget(o *Objective, r *Rollup) {
	if r == nil {
		return
	}
	if o.BudgetCostUSD != nil && *o.BudgetCostUSD > 0 {
		pct := (r.CostUSD / *o.BudgetCostUSD) * 100
		r.BudgetPctCost = &pct
	}
	if o.BudgetTokens != nil && *o.BudgetTokens > 0 {
		pct := (float64(r.TotalTokens) / float64(*o.BudgetTokens)) * 100
		r.BudgetPctTok = &pct
	}
}

type UpdateObjectiveParams struct {
	Title         *string
	Description   *string
	Status        *string
	ProjectID     *string
	BudgetTokens  *int64
	BudgetCostUSD *float64
	ShippedAt     *time.Time
}

func (s *Store) UpdateObjective(ctx context.Context, orgID, id string, p UpdateObjectiveParams) (*Objective, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{orgID, id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Description != nil {
		add("description", *p.Description)
	}
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.ProjectID != nil {
		add("project_id", *p.ProjectID)
	}
	if p.BudgetTokens != nil {
		add("budget_tokens", *p.BudgetTokens)
	}
	if p.BudgetCostUSD != nil {
		add("budget_cost_usd", *p.BudgetCostUSD)
	}
	if p.ShippedAt != nil {
		add("shipped_at", *p.ShippedAt)
	}

	q := fmt.Sprintf(
		`UPDATE objectives SET %s WHERE organization_id = $1 AND id = $2 RETURNING id`,
		strings.Join(sets, ", "))
	var updated string
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&updated); err != nil {
		return nil, mapErr(err)
	}
	return s.GetObjective(ctx, orgID, id)
}

func (s *Store) DeleteObjective(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM objectives WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddMarbleToObjective buckets a marble into an objective (drag-and-drop target).
func (s *Store) AddMarbleToObjective(ctx context.Context, orgID, objectiveID, marbleID string) (*Marble, error) {
	if _, err := s.GetObjectiveShallow(ctx, orgID, objectiveID); err != nil {
		return nil, err
	}
	return s.UpdateMarble(ctx, orgID, marbleID, UpdateMarbleParams{ObjectiveID: &objectiveID})
}

func (s *Store) RemoveMarbleFromObjective(ctx context.Context, orgID, objectiveID, marbleID string) (*Marble, error) {
	var current *string
	if err := s.pool.QueryRow(ctx,
		`SELECT objective_id FROM marbles WHERE organization_id = $1 AND id = $2`,
		orgID, marbleID).Scan(&current); err != nil {
		return nil, mapErr(err)
	}
	if current == nil || *current != objectiveID {
		return nil, ErrNotFound
	}
	return s.UpdateMarble(ctx, orgID, marbleID, UpdateMarbleParams{ClearObjective: true})
}

func (s *Store) GetObjectiveShallow(ctx context.Context, orgID, id string) (*Objective, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+objectiveColumns+` FROM objectives WHERE organization_id = $1 AND id = $2`,
		orgID, id)
	return scanObjective(row)
}

// ObjectiveTrend returns per-bucket rollups for the objective detail charts.
func (s *Store) ObjectiveTrend(ctx context.Context, orgID, objectiveID, interval string) ([]TrendPoint, error) {
	iv := normalizeInterval(interval)
	rows, err := s.pool.Query(ctx, `
		SELECT date_trunc($3, occurred_at) AS bucket,
		       COUNT(*),
		       COALESCE(SUM(cost_usd),0),
		       COALESCE(SUM(tokens_in),0),
		       COALESCE(SUM(tokens_out),0),
		       COALESCE(SUM(duration_ms),0)
		FROM marbles
		WHERE organization_id = $1 AND objective_id = $2
		GROUP BY bucket ORDER BY bucket ASC`,
		orgID, objectiveID, iv)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	return scanTrend(rows)
}
