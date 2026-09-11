package rules

import (
	"encoding/json"
	"testing"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

func ptrS(s string) *string   { return &s }
func ptrI(i int) *int         { return &i }
func ptrF(f float64) *float64 { return &f }

func marble() *store.Marble {
	return &store.Marble{
		ID:          "m1",
		Summary:     "Refactored the auth module",
		Model:       ptrS("claude-sonnet-4"),
		ProjectName: ptrS("payments-api"),
		AgentName:   ptrS("claude-code"),
		CostUSD:     ptrF(0.42),
		TokensIn:    ptrI(12000),
		TokensOut:   ptrI(3000),
		DurationMS:  ptrI(45000),
		Status:      "logged",
		Source:      "sdk",
	}
}

func mustCond(t *testing.T, raw string) *Condition {
	t.Helper()
	c, err := Parse(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate %s: %v", raw, err)
	}
	return c
}

func TestConditionMatching(t *testing.T) {
	m := marble()

	cases := []struct {
		name string
		cond string
		want bool
	}{
		{"empty matches all", `{}`, true},
		{"project eq", `{"field":"project","op":"eq","value":"payments-api"}`, true},
		{"project eq is case-insensitive", `{"field":"project","op":"eq","value":"Payments-API"}`, true},
		{"project neq", `{"field":"project","op":"neq","value":"billing"}`, true},
		{"model mismatch", `{"field":"model","op":"eq","value":"gpt-4"}`, false},
		{"cost gt", `{"field":"cost_usd","op":"gt","value":0.1}`, true},
		{"cost gt above value", `{"field":"cost_usd","op":"gt","value":1.0}`, false},
		{"cost lte boundary", `{"field":"cost_usd","op":"lte","value":0.42}`, true},
		{"tokens_total sum", `{"field":"tokens_total","op":"gte","value":15000}`, true},
		{"summary contains", `{"field":"summary","op":"contains","value":"auth"}`, true},
		{"model in list", `{"field":"model","op":"in","value":["gpt-4","claude-sonnet-4"]}`, true},
		{"model in list miss", `{"field":"model","op":"in","value":["gpt-4"]}`, false},
		{"objective_id exists false", `{"field":"objective_id","op":"exists","value":false}`, true},
		{
			"all composite",
			`{"all":[{"field":"project","op":"eq","value":"payments-api"},
			         {"field":"cost_usd","op":"gt","value":0.2}]}`,
			true,
		},
		{
			"all composite one fails",
			`{"all":[{"field":"project","op":"eq","value":"payments-api"},
			         {"field":"cost_usd","op":"gt","value":9.0}]}`,
			false,
		},
		{
			"any composite",
			`{"any":[{"field":"model","op":"eq","value":"gpt-4"},
			         {"field":"agent","op":"eq","value":"claude-code"}]}`,
			true,
		},
		{"not", `{"not":{"field":"project","op":"eq","value":"billing"}}`, true},
		{
			"nested",
			`{"all":[{"field":"status","op":"eq","value":"logged"},
			         {"any":[{"field":"cost_usd","op":"gt","value":10},
			                 {"field":"duration_ms","op":"gt","value":30000}]}]}`,
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustCond(t, tc.cond).Matches(m); got != tc.want {
				t.Errorf("Matches() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNilFieldsTreatedAsZero(t *testing.T) {
	m := &store.Marble{Summary: "no metrics", Status: "logged"}

	if !mustCond(t, `{"field":"cost_usd","op":"eq","value":0}`).Matches(m) {
		t.Error("nil cost should compare as zero")
	}
	if mustCond(t, `{"field":"model","op":"eq","value":"gpt-4"}`).Matches(m) {
		t.Error("nil model should not match a concrete value")
	}
	if !mustCond(t, `{"field":"model","op":"exists","value":false}`).Matches(m) {
		t.Error("nil model should report as not existing")
	}
}

func TestValidateRejectsBadConditions(t *testing.T) {
	bad := []string{
		`{"field":"nonexistent","op":"eq","value":1}`,
		`{"field":"model","op":"regex","value":"x"}`,
		`{"all":[{"field":"model","op":"eq","value":"x"}],"any":[{"field":"status","op":"eq","value":"y"}]}`,
		`{"all":[{"field":"bogus","op":"eq","value":1}]}`,
	}
	for _, raw := range bad {
		c, err := Parse(json.RawMessage(raw))
		if err != nil {
			continue // parse rejection is also acceptable
		}
		if err := c.Validate(); err == nil {
			t.Errorf("expected validation error for %s", raw)
		}
	}
}

func TestEvaluateSkipsInactiveAndOtherProjects(t *testing.T) {
	m := marble()
	m.ProjectID = ptrS("proj-1")

	rs := []store.DispatchRule{
		{ID: "active", IsActive: true, TargetType: "slack",
			Condition: json.RawMessage(`{"field":"project","op":"eq","value":"payments-api"}`)},
		{ID: "inactive", IsActive: false, TargetType: "slack",
			Condition: json.RawMessage(`{}`)},
		{ID: "other-project", IsActive: true, TargetType: "jira",
			ProjectID: ptrS("proj-2"), Condition: json.RawMessage(`{}`)},
		{ID: "same-project", IsActive: true, TargetType: "jira",
			ProjectID: ptrS("proj-1"), Condition: json.RawMessage(`{}`)},
	}

	got := Evaluate(rs, m)
	if len(got) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(got))
	}
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids["active"] || !ids["same-project"] {
		t.Errorf("unexpected matches: %v", ids)
	}
}
