// Package objectives implements "bigger jar" rollups and the Ship It flow that
// posts an aggregate summary of everything an objective accomplished.
package objectives

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

type Service struct {
	store      *store.Store
	dispatcher *dispatch.Service
}

func NewService(st *store.Store, d *dispatch.Service) *Service {
	return &Service{store: st, dispatcher: d}
}

// Summary is the generated "ship it" narrative plus its supporting numbers.
type Summary struct {
	ObjectiveID  string        `json:"objective_id"`
	Title        string        `json:"title"`
	Text         string        `json:"text"`
	Markdown     string        `json:"markdown"`
	Rollup       *store.Rollup `json:"rollup"`
	MarbleCount  int           `json:"marble_count"`
	Highlights   []string      `json:"highlights"`
	ModelsUsed   []string      `json:"models_used"`
	BudgetStatus string        `json:"budget_status"`
}

// BuildSummary aggregates an objective's marbles into a human-readable update.
func (s *Service) BuildSummary(ctx context.Context, orgID, objectiveID string) (*Summary, error) {
	obj, err := s.store.GetObjective(ctx, orgID, objectiveID)
	if err != nil {
		return nil, err
	}

	marbles, _, err := s.store.ListMarbles(ctx, orgID, store.ListMarblesFilter{
		ObjectiveID: objectiveID,
		Limit:       200,
	})
	if err != nil {
		return nil, err
	}

	modelCounts := map[string]int{}
	highlights := make([]string, 0, len(marbles))
	for _, m := range marbles {
		if m.Model != nil {
			modelCounts[*m.Model]++
		}
		highlights = append(highlights, m.Summary)
	}

	models := make([]string, 0, len(modelCounts))
	for k := range modelCounts {
		models = append(models, k)
	}
	sort.Slice(models, func(i, j int) bool {
		if modelCounts[models[i]] != modelCounts[models[j]] {
			return modelCounts[models[i]] > modelCounts[models[j]]
		}
		return models[i] < models[j]
	})

	if len(highlights) > 10 {
		highlights = highlights[:10]
	}

	rollup := obj.Rollup
	if rollup == nil {
		rollup = &store.Rollup{}
	}

	budget := "no budget set"
	switch {
	case rollup.BudgetPctCost != nil && *rollup.BudgetPctCost > 100:
		budget = fmt.Sprintf("over cost budget (%.0f%%)", *rollup.BudgetPctCost)
	case rollup.BudgetPctCost != nil:
		budget = fmt.Sprintf("%.0f%% of cost budget used", *rollup.BudgetPctCost)
	case rollup.BudgetPctTok != nil:
		budget = fmt.Sprintf("%.0f%% of token budget used", *rollup.BudgetPctTok)
	}

	var md strings.Builder
	fmt.Fprintf(&md, "## %s — shipped\n\n", obj.Title)
	if obj.Description != nil && *obj.Description != "" {
		fmt.Fprintf(&md, "%s\n\n", *obj.Description)
	}
	fmt.Fprintf(&md, "**%d marbles** · **$%.4f** · **%s tokens** · **%s**\n\n",
		rollup.MarbleCount, rollup.CostUSD,
		humanInt(rollup.TotalTokens), humanDuration(rollup.DurationMS))

	if len(highlights) > 0 {
		md.WriteString("### Completed work\n")
		for _, h := range highlights {
			fmt.Fprintf(&md, "- %s\n", h)
		}
		md.WriteString("\n")
	}
	if len(models) > 0 {
		fmt.Fprintf(&md, "_Models: %s_\n", strings.Join(models, ", "))
	}

	text := fmt.Sprintf("%s shipped: %d units of agent work, $%.4f, %s tokens, %s total runtime.",
		obj.Title, rollup.MarbleCount, rollup.CostUSD,
		humanInt(rollup.TotalTokens), humanDuration(rollup.DurationMS))

	return &Summary{
		ObjectiveID:  obj.ID,
		Title:        obj.Title,
		Text:         text,
		Markdown:     md.String(),
		Rollup:       rollup,
		MarbleCount:  rollup.MarbleCount,
		Highlights:   highlights,
		ModelsUsed:   models,
		BudgetStatus: budget,
	}, nil
}

// ShipRequest configures where the aggregate summary is posted.
type ShipRequest struct {
	IntegrationType string          `json:"integration_type"`
	Action          string          `json:"action"`
	TargetConfig    json.RawMessage `json:"target_config"`
	// SummaryOverride lets a human edit the generated text before sending.
	SummaryOverride string `json:"summary_override"`
	// MarkStatus closes the objective when the dispatch is queued.
	MarkShipped bool `json:"mark_shipped"`
}

type ShipResult struct {
	Dispatch  *store.Dispatch  `json:"dispatch"`
	Summary   *Summary         `json:"summary"`
	Objective *store.Objective `json:"objective"`
}

// Ship generates the aggregate summary and queues it for dispatch.
func (s *Service) Ship(ctx context.Context, p *auth.Principal, objectiveID string, req ShipRequest) (*ShipResult, error) {
	summary, err := s.BuildSummary(ctx, p.OrganizationID, objectiveID)
	if err != nil {
		return nil, err
	}
	if summary.MarbleCount == 0 {
		return nil, fmt.Errorf("objective has no marbles to ship")
	}

	body := summary.Markdown
	if strings.TrimSpace(req.SummaryOverride) != "" {
		body = req.SummaryOverride
	}

	payload := store.RawJSON(map[string]any{
		"objective_id": objectiveID,
		"title":        summary.Title,
		"summary":      summary.Text,
		"body":         body,
		"marble_count": summary.MarbleCount,
		"cost_usd":     summary.Rollup.CostUSD,
		"total_tokens": summary.Rollup.TotalTokens,
		"duration_ms":  summary.Rollup.DurationMS,
		"models":       summary.ModelsUsed,
		"kind":         "objective_summary",
	})

	d, err := s.dispatcher.Create(ctx, p, dispatch.Request{
		IntegrationType: req.IntegrationType,
		Action:          req.Action,
		ObjectiveID:     &objectiveID,
		TargetConfig:    req.TargetConfig,
		Payload:         payload,
	})
	if err != nil {
		return nil, err
	}

	obj, err := s.store.GetObjective(ctx, p.OrganizationID, objectiveID)
	if err != nil {
		return nil, err
	}
	if req.MarkShipped {
		now := time.Now().UTC()
		shipped := "shipped"
		obj, err = s.store.UpdateObjective(ctx, p.OrganizationID, objectiveID,
			store.UpdateObjectiveParams{Status: &shipped, ShippedAt: &now})
		if err != nil {
			return nil, err
		}
	}

	return &ShipResult{Dispatch: d, Summary: summary, Objective: obj}, nil
}

func humanInt(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func humanDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.1fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dms", ms)
}
