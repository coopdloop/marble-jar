package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

func TestCreateMarble_IdempotentReplay(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	params := baseMarble(org, "idempotent work")
	params.IdempotencyKey = ptrStr("key-123")

	first, created, err := testDB.CreateMarble(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first insert should report created=true")
	}

	second, created, err := testDB.CreateMarble(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("replay must report created=false")
	}
	if first.ID != second.ID {
		t.Fatalf("replay returned a different marble: %s != %s", first.ID, second.ID)
	}

	// The durable copy is singular too.
	items, _, err := testDB.ListMarbles(ctx, org, store.ListMarblesFilter{Search: "idempotent work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 marble, got %d", len(items))
	}
}

func TestListMarbles_PaginationNoOverlap(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	// Two marbles share an occurred_at exactly — the (ts, id) tuple cursor must
	// not drop or duplicate them across the page boundary.
	shared := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		p := baseMarble(org, "pageable")
		p.OccurredAt = shared.Add(time.Duration(i) * time.Second)
		if i < 2 {
			p.OccurredAt = shared // identical timestamps for the tie-breaker
		}
		if _, created, err := testDB.CreateMarble(ctx, p); err != nil || !created {
			t.Fatalf("seed %d: %v created=%v", i, err, created)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, next, err := testDB.ListMarbles(ctx, org, store.ListMarblesFilter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, m := range page {
			if seen[m.ID] {
				t.Fatalf("marble %s appeared on two pages", m.ID)
			}
			seen[m.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	if len(seen) != 7 {
		t.Fatalf("expected 7 unique marbles across pages, got %d", len(seen))
	}
}

func TestListMarbles_Filters(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	proj, err := testDB.UpsertProjectBySlug(ctx, org, "Payments API")
	if err != nil {
		t.Fatal(err)
	}

	paid := baseMarble(org, "expensive work")
	paid.ProjectID = &proj.ID
	paid.Model = ptrStr("claude-sonnet-4")
	mustCreateMarbleParams(t, paid)

	cheap := baseMarble(org, "cheap work")
	cheap.Model = ptrStr("gpt-4o-mini")
	mustCreateMarbleParams(t, cheap)

	cases := []struct {
		name   string
		filter store.ListMarblesFilter
		want   int
	}{
		{"by project slug", store.ListMarblesFilter{Project: "payments-api"}, 1},
		{"by project id", store.ListMarblesFilter{Project: proj.ID}, 1},
		{"by model", store.ListMarblesFilter{Model: "gpt-4o-mini"}, 1},
		{"by search substring", store.ListMarblesFilter{Search: "EXPENSIVE"}, 1},
		{"unassigned", store.ListMarblesFilter{Unassigned: true}, 2},
		{"no match", store.ListMarblesFilter{Model: "nonexistent"}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, _, err := testDB.ListMarbles(ctx, org, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, len(items))
			}
		})
	}
}

func TestListMarbles_OrgIsolation(t *testing.T) {
	orgA, orgB := newOrg(t), newOrg(t)
	mustCreateMarble(t, orgA, "org A marble")

	items, _, err := testDB.ListMarbles(context.Background(), orgB, store.ListMarblesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("org B must not see org A marbles, got %d", len(items))
	}
}

func TestUpdateMarble_ObjectiveReassignmentSyncsMetrics(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	obj, err := testDB.CreateObjective(ctx, store.CreateObjectiveParams{
		OrganizationID: org, Title: "Rollup target",
	})
	if err != nil {
		t.Fatal(err)
	}

	m := mustCreateMarble(t, org, "bucketable")

	updated, err := testDB.UpdateMarble(ctx, org, m.ID, store.UpdateMarbleParams{ObjectiveID: &obj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ObjectiveID == nil || *updated.ObjectiveID != obj.ID {
		t.Fatalf("objective not assigned: %+v", updated.ObjectiveID)
	}

	rollup, err := testDB.ObjectiveRollup(ctx, org, obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rollup.MarbleCount != 1 {
		t.Fatalf("rollup count = %d, want 1", rollup.MarbleCount)
	}

	cleared, err := testDB.UpdateMarble(ctx, org, m.ID, store.UpdateMarbleParams{ClearObjective: true})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ObjectiveID != nil {
		t.Fatal("objective not cleared")
	}
}

func TestDeleteMarble(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()
	m := mustCreateMarble(t, org, "doomed")

	if err := testDB.DeleteMarble(ctx, org, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.GetMarble(ctx, org, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := testDB.DeleteMarble(ctx, org, m.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete must be ErrNotFound, got %v", err)
	}
}

func TestWriteMarbleRollup(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()
	m := mustCreateMarble(t, org, "rollup target")

	got, err := testDB.WriteMarbleRollup(ctx, org, m.ID, store.RollupParams{
		TokensIn:        ptrInt(5000),
		TokensOut:       ptrInt(1200),
		CostUSD:         ptrF64(0.31),
		TraceID:         ptrStr("trace-xyz"),
		PhoenixTraceURL: ptrStr("http://phoenix:6006/v1/traces/trace-xyz"),
		PhoenixProject:  ptrStr("agent-runs"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TokensIn == nil || *got.TokensIn != 5000 {
		t.Fatalf("tokens_in not written: %+v", got.TokensIn)
	}
	if got.PhoenixTraceURL == nil {
		t.Fatal("phoenix url not written")
	}

	// The phoenix_trace_links row must exist for lineage.
	var linkCount int
	if err := testDB.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM phoenix_trace_links WHERE marble_id = $1`, m.ID,
	).Scan(&linkCount); err != nil {
		t.Fatal(err)
	}
	if linkCount != 1 {
		t.Fatalf("expected 1 trace link, got %d", linkCount)
	}
}

// TestTrends regression-covers the interval-encoding bug: a plain int parameter
// must work against the real Postgres query.
func TestTrends_DayBuckets(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	today := time.Now().UTC()
	weekAgo := today.Add(-6 * 24 * time.Hour)

	p1 := baseMarble(org, "today")
	p1.OccurredAt = today
	mustCreateMarbleParams(t, p1)

	p2 := baseMarble(org, "week ago")
	p2.OccurredAt = weekAgo
	mustCreateMarbleParams(t, p2)

	points, err := testDB.Trends(ctx, org, store.TrendFilter{Interval: "day", Days: 7})
	if err != nil {
		t.Fatal(err)
	}

	var totalCount int
	var totalCost float64
	for _, p := range points {
		totalCount += p.MarbleCount
		totalCost += p.CostUSD
	}
	if totalCount != 2 {
		t.Fatalf("trend buckets counted %d marbles, want 2", totalCount)
	}
	if totalCost < 0.019 || totalCost > 0.021 {
		t.Fatalf("trend cost = %v, want ~0.02", totalCost)
	}

	// Hourly interval must also work.
	hourly, err := testDB.Trends(ctx, org, store.TrendFilter{Interval: "hour", Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) == 0 {
		t.Fatal("hourly trend returned no buckets for today's marble")
	}
}

func TestJarStatus(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	mustCreateMarble(t, org, "one")
	mustCreateMarble(t, org, "two")

	if _, err := testDB.CreateObjective(ctx, store.CreateObjectiveParams{
		OrganizationID: org, Title: "open one",
	}); err != nil {
		t.Fatal(err)
	}

	status, err := testDB.JarStatus(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if status.MarblesTotal != 2 {
		t.Fatalf("marbles_total = %d, want 2", status.MarblesTotal)
	}
	if status.OpenObjectives != 1 {
		t.Fatalf("open_objectives = %d, want 1", status.OpenObjectives)
	}
	if len(status.TopModels) == 0 || status.TopModels[0] != "test-model" {
		t.Fatalf("top_models = %v", status.TopModels)
	}
}

func mustCreateMarbleParams(t *testing.T, p store.CreateMarbleParams) *store.Marble {
	t.Helper()
	m, created, err := testDB.CreateMarble(context.Background(), p)
	if err != nil {
		t.Fatalf("create marble %q: %v", p.Summary, err)
	}
	if !created {
		t.Fatalf("expected marble %q to be created", p.Summary)
	}
	return m
}
