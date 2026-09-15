package store_test

import (
	"context"
	"testing"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

func TestListMarbles_SearchAcrossFields(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	proj, err := testDB.UpsertProjectBySlug(ctx, org, "Billing Engine")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := testDB.UpsertAgentByName(ctx, org, "claude-code", "cli", "opus")
	if err != nil {
		t.Fatal(err)
	}

	p := baseMarble(org, "retry the webhook delivery")
	p.ProjectID = &proj.ID
	p.AgentID = &agent.ID
	p.Model = ptrStr("gpt-5-mini")
	p.Source = "mcp"
	mustCreateMarbleParams(t, p)

	// A marble with no project or agent attached must still be findable by text.
	mustCreateMarble(t, org, "nothing else matches this")

	for _, q := range []string{"billing", "claude", "gpt-5", "mcp", "webhook delivery"} {
		items, _, err := testDB.ListMarbles(ctx, org, store.ListMarblesFilter{Search: q})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(items) != 1 {
			t.Fatalf("search %q matched %d marbles, want 1", q, len(items))
		}
		if items[0].Summary != "retry the webhook delivery" {
			t.Fatalf("search %q matched the wrong marble: %q", q, items[0].Summary)
		}
	}

	// LIKE metacharacters in the query stay literal instead of matching everything.
	for _, q := range []string{"%", "_", "billing%"} {
		items, _, err := testDB.ListMarbles(ctx, org, store.ListMarblesFilter{Search: q})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(items) != 0 {
			t.Fatalf("search %q matched %d marbles, want none", q, len(items))
		}
	}

	// Whitespace-only queries are ignored rather than matching everything.
	all, _, err := testDB.ListMarbles(ctx, org, store.ListMarblesFilter{Search: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("blank search matched %d marbles, want both", len(all))
	}
}
