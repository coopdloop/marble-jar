package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

func TestObjectives_RollupAndBudget(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	obj, err := testDB.CreateObjective(ctx, store.CreateObjectiveParams{
		OrganizationID: org,
		Title:          "Budgeted",
		BudgetCostUSD:  ptrF64(1.00),
		BudgetTokens:   ptrInt64(1000),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		cost   float64
		inTok  int
		outTok int
	}{
		{0.30, 200, 100},
		{0.20, 300, 200},
	} {
		p := baseMarble(org, "costly")
		p.CostUSD = ptrF64(tc.cost)
		p.TokensIn = ptrInt(tc.inTok)
		p.TokensOut = ptrInt(tc.outTok)
		m := mustCreateMarbleParams(t, p)
		if _, err := testDB.AddMarbleToObjective(ctx, org, obj.ID, m.ID); err != nil {
			t.Fatal(err)
		}
	}

	got, err := testDB.GetObjective(ctx, org, obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rollup
	if r == nil {
		t.Fatal("rollup missing")
	}
	if r.MarbleCount != 2 {
		t.Fatalf("marble_count = %d, want 2", r.MarbleCount)
	}
	if r.TotalTokens != 800 {
		t.Fatalf("total_tokens = %d, want 800", r.TotalTokens)
	}
	if r.CostUSD < 0.499 || r.CostUSD > 0.501 {
		t.Fatalf("cost_usd = %v, want ~0.50", r.CostUSD)
	}
	if r.BudgetPctCost == nil || *r.BudgetPctCost < 49.9 || *r.BudgetPctCost > 50.1 {
		t.Fatalf("budget_pct_cost = %v, want ~50", r.BudgetPctCost)
	}
	if r.BudgetPctTok == nil || *r.BudgetPctTok != 80 {
		t.Fatalf("budget_pct_tokens = %v, want 80", r.BudgetPctTok)
	}

	// List view must carry the same rollup without N+1.
	items, _, err := testDB.ListObjectives(ctx, org, store.ListObjectivesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Rollup == nil || items[0].Rollup.MarbleCount != 2 {
		t.Fatalf("list objectives rollup wrong: %+v", items)
	}
}

func TestObjectiveMarbleAddRemove(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	obj, err := testDB.CreateObjective(ctx, store.CreateObjectiveParams{
		OrganizationID: org, Title: "Bucket",
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := testDB.CreateObjective(ctx, store.CreateObjectiveParams{
		OrganizationID: org, Title: "Other",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := mustCreateMarble(t, org, "movable")

	if _, err := testDB.AddMarbleToObjective(ctx, org, obj.ID, m.ID); err != nil {
		t.Fatal(err)
	}

	// Removing from a different objective is a 404, not a silent no-op.
	if _, err := testDB.RemoveMarbleFromObjective(ctx, org, other.ID, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove from wrong objective: got %v, want ErrNotFound", err)
	}

	if _, err := testDB.RemoveMarbleFromObjective(ctx, org, obj.ID, m.ID); err != nil {
		t.Fatal(err)
	}

	rollup, err := testDB.ObjectiveRollup(ctx, org, obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rollup.MarbleCount != 0 {
		t.Fatalf("after removal marble_count = %d, want 0", rollup.MarbleCount)
	}
}

func TestDispatchLifecycleAndReplay(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()
	user := newUser(t, org)
	m := mustCreateMarble(t, org, "to dispatch")

	d, err := testDB.CreateDispatch(ctx, store.CreateDispatchParams{
		OrganizationID:  org,
		MarbleID:        &m.ID,
		IntegrationType: "webhook",
		Action:          "post",
		ActingUserID:    &user.ID,
		RequestPayload: store.RawJSON(map[string]any{
			"target_config": map[string]any{"url": "http://localhost:9999/hook"},
			"payload":       map[string]any{"summary": "to dispatch"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "pending" {
		t.Fatalf("new dispatch status = %q, want pending", d.Status)
	}

	// A fresh pending dispatch is in-flight as far as the system knows; replay
	// must refuse it. Cutoff of now-2m: a dispatch created seconds ago is newer
	// than the cutoff, so it is not stale.
	staleCutoff := time.Now().UTC().Add(-2 * time.Minute)
	if _, err := testDB.ReplayDispatch(ctx, org, d.ID, staleCutoff); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("replay of fresh pending: got %v, want ErrNotFound", err)
	}

	// Run it: running -> succeeded with a webhook detail row.
	if _, err := testDB.RecordDispatchResult(ctx, org, d.ID, store.DispatchResultParams{Status: "running"}); err != nil {
		t.Fatal(err)
	}
	ref := "sink-1"
	done, err := testDB.RecordDispatchResult(ctx, org, d.ID, store.DispatchResultParams{
		Status:      "succeeded",
		ExternalRef: &ref,
		ResponsePayload: store.RawJSON(map[string]any{
			"target_url": "http://localhost:9999/hook", "http_status": 200,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "succeeded" || done.DispatchedAt == nil {
		t.Fatalf("dispatch not marked succeeded: %+v", done)
	}

	if err := testDB.RecordIntegrationDetail(ctx, "webhook", d.ID, map[string]any{
		"target_url":  "http://localhost:9999/hook",
		"http_status": 200,
	}); err != nil {
		t.Fatal(err)
	}
	var detailCount int
	if err := testDB.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM webhook_dispatch_details WHERE dispatch_id = $1`, d.ID,
	).Scan(&detailCount); err != nil {
		t.Fatal(err)
	}
	if detailCount != 1 {
		t.Fatalf("webhook detail rows = %d, want 1", detailCount)
	}

	// Succeeded dispatches are done; replay must refuse them.
	if _, err := testDB.ReplayDispatch(ctx, org, d.ID, time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("replay of succeeded: got %v, want ErrNotFound", err)
	}
}

func TestReplayDispatch_TerminalAndStale(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	d, err := testDB.CreateDispatch(ctx, store.CreateDispatchParams{
		OrganizationID:  org,
		IntegrationType: "slack",
		Action:          "post_message",
	})
	if err != nil {
		t.Fatal(err)
	}

	errMsg := "http 405"
	failed, err := testDB.RecordDispatchResult(ctx, org, d.ID, store.DispatchResultParams{
		Status:       "dead_lettered",
		ErrorMessage: &errMsg,
		AttemptCount: ptrInt(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "dead_lettered" {
		t.Fatalf("status = %q", failed.Status)
	}

	replayed, err := testDB.ReplayDispatch(ctx, org, d.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != "pending" {
		t.Fatalf("replayed status = %q, want pending", replayed.Status)
	}
	if replayed.ErrorMessage != nil {
		t.Fatalf("error not cleared: %v", *replayed.ErrorMessage)
	}
	if replayed.AttemptCount != 0 {
		t.Fatalf("attempt count not reset: %d", replayed.AttemptCount)
	}
}

func TestRulesCRUD(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	r, err := testDB.CreateRule(ctx, store.CreateRuleParams{
		OrganizationID: org,
		Name:           "costly to slack",
		Condition:      store.RawJSON(map[string]any{"field": "cost_usd", "op": "gt", "value": 0.5}),
		TargetType:     "slack",
		TargetConfig:   store.RawJSON(map[string]any{"channel": "#eng"}),
		IsActive:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	inactive := false
	updated, err := testDB.UpdateRule(ctx, org, r.ID, store.UpdateRuleParams{IsActive: &inactive})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IsActive {
		t.Fatal("rule still active after update")
	}

	activeOnly, err := testDB.ListRules(ctx, org, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeOnly) != 0 {
		t.Fatalf("active-only list returned %d rules, want 0", len(activeOnly))
	}

	if err := testDB.DeleteRule(ctx, org, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.GetRule(ctx, org, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted rule: got %v, want ErrNotFound", err)
	}
}

func TestAuditTrail(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()
	user := newUser(t, org)

	if err := testDB.RecordAudit(ctx, store.AuditParams{
		OrganizationID: org,
		ActingUserID:   &user.ID,
		Provider:       "slack",
		Action:         "dispatch.queued:post_message",
		Details:        map[string]any{"channel": "#eng"},
	}); err != nil {
		t.Fatal(err)
	}

	entries, _, err := testDB.ListAudit(ctx, org, store.ListAuditFilter{Provider: "slack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].ActingUserID == nil || *entries[0].ActingUserID != user.ID {
		t.Fatal("audit entry missing acting user attribution")
	}

	other, _, err := testDB.ListAudit(ctx, org, store.ListAuditFilter{Provider: "jira"})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("provider filter leaked: %d entries", len(other))
	}
}

func TestAPIKeysPrefixLookup(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	k, err := testDB.CreateAPIKey(ctx, org, nil, "agent-key", "mj_testprefix", "hash-abc", []string{"marbles:write"})
	if err != nil {
		t.Fatal(err)
	}

	found, err := testDB.APIKeysByPrefix(ctx, "mj_testprefix")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != k.ID {
		t.Fatalf("prefix lookup returned %+v", found)
	}

	if err := testDB.RevokeAPIKey(ctx, org, k.ID); err != nil {
		t.Fatal(err)
	}
	afterRevoke, err := testDB.APIKeysByPrefix(ctx, "mj_testprefix")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRevoke) != 0 {
		t.Fatal("revoked key still returned by prefix lookup")
	}
}
