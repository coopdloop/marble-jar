// Package rules evaluates dispatch-rule conditions against marbles. Conditions
// are stored as JSON so the frontend chip builder and the backend share one
// declarative shape:
//
//	{"all": [
//	   {"field": "project", "op": "eq", "value": "payments-api"},
//	   {"field": "cost_usd", "op": "gt", "value": 0.5}
//	]}
//
// "all"/"any"/"not" compose; a bare {"field":...} is also accepted.
package rules

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// Condition is a recursive boolean tree.
type Condition struct {
	All   []Condition `json:"all,omitempty"`
	Any   []Condition `json:"any,omitempty"`
	Not   *Condition  `json:"not,omitempty"`
	Field string      `json:"field,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value any         `json:"value,omitempty"`
}

// Parse decodes stored JSONB into a Condition tree.
func Parse(raw json.RawMessage) (*Condition, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return &Condition{}, nil
	}
	var c Condition
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("invalid condition: %w", err)
	}
	return &c, nil
}

// Validate rejects malformed conditions before they are persisted.
func (c *Condition) Validate() error {
	if c == nil {
		return nil
	}
	branches := 0
	if len(c.All) > 0 {
		branches++
	}
	if len(c.Any) > 0 {
		branches++
	}
	if c.Not != nil {
		branches++
	}
	if c.Field != "" {
		branches++
	}

	if branches == 0 {
		return nil // empty condition == match-all
	}
	if branches > 1 {
		return fmt.Errorf("condition must use exactly one of all/any/not/field")
	}

	for i := range c.All {
		if err := c.All[i].Validate(); err != nil {
			return err
		}
	}
	for i := range c.Any {
		if err := c.Any[i].Validate(); err != nil {
			return err
		}
	}
	if c.Not != nil {
		return c.Not.Validate()
	}
	if c.Field != "" {
		if !validFields[c.Field] {
			return fmt.Errorf("unknown condition field %q", c.Field)
		}
		if !validOps[c.Op] {
			return fmt.Errorf("unknown condition operator %q", c.Op)
		}
	}
	return nil
}

var validFields = map[string]bool{
	"project": true, "project_id": true, "model": true, "agent": true,
	"agent_id": true, "status": true, "source": true, "summary": true,
	"cost_usd": true, "tokens_in": true, "tokens_out": true,
	"tokens_total": true, "duration_ms": true, "objective_id": true,
}

var validOps = map[string]bool{
	"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"contains": true, "in": true, "exists": true,
}

// Matches evaluates the condition against a marble.
func (c *Condition) Matches(m *store.Marble) bool {
	if c == nil {
		return true
	}
	switch {
	case len(c.All) > 0:
		for i := range c.All {
			if !c.All[i].Matches(m) {
				return false
			}
		}
		return true
	case len(c.Any) > 0:
		for i := range c.Any {
			if c.Any[i].Matches(m) {
				return true
			}
		}
		return false
	case c.Not != nil:
		return !c.Not.Matches(m)
	case c.Field != "":
		return evalLeaf(c, m)
	default:
		return true // empty condition matches everything
	}
}

func evalLeaf(c *Condition, m *store.Marble) bool {
	if num, ok := numericField(c.Field, m); ok {
		return compareNumeric(c.Op, num, c.Value)
	}
	sv, present := stringField(c.Field, m)
	return compareString(c.Op, sv, present, c.Value)
}

func numericField(field string, m *store.Marble) (float64, bool) {
	switch field {
	case "cost_usd":
		if m.CostUSD == nil {
			return 0, true
		}
		return *m.CostUSD, true
	case "tokens_in":
		return floatOrZero(m.TokensIn), true
	case "tokens_out":
		return floatOrZero(m.TokensOut), true
	case "tokens_total":
		return floatOrZero(m.TokensIn) + floatOrZero(m.TokensOut), true
	case "duration_ms":
		return floatOrZero(m.DurationMS), true
	}
	return 0, false
}

func floatOrZero(v *int) float64 {
	if v == nil {
		return 0
	}
	return float64(*v)
}

func stringField(field string, m *store.Marble) (string, bool) {
	deref := func(p *string) (string, bool) {
		if p == nil {
			return "", false
		}
		return *p, true
	}
	switch field {
	case "project":
		if m.ProjectName != nil {
			return *m.ProjectName, true
		}
		return deref(m.ProjectID)
	case "project_id":
		return deref(m.ProjectID)
	case "model":
		return deref(m.Model)
	case "agent":
		if m.AgentName != nil {
			return *m.AgentName, true
		}
		return deref(m.AgentID)
	case "agent_id":
		return deref(m.AgentID)
	case "objective_id":
		return deref(m.ObjectiveID)
	case "status":
		return m.Status, true
	case "source":
		return m.Source, true
	case "summary":
		return m.Summary, true
	}
	return "", false
}

func compareNumeric(op string, actual float64, want any) bool {
	if op == "exists" {
		return truthy(want) == (actual != 0)
	}
	if op == "in" {
		list, ok := want.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if f, ok := toFloat(item); ok && f == actual {
				return true
			}
		}
		return false
	}

	expected, ok := toFloat(want)
	if !ok {
		return false
	}
	switch op {
	case "eq":
		return actual == expected
	case "neq":
		return actual != expected
	case "gt":
		return actual > expected
	case "gte":
		return actual >= expected
	case "lt":
		return actual < expected
	case "lte":
		return actual <= expected
	}
	return false
}

func compareString(op, actual string, present bool, want any) bool {
	if op == "exists" {
		return truthy(want) == (present && actual != "")
	}
	if op == "in" {
		list, ok := want.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if s, ok := item.(string); ok && strings.EqualFold(s, actual) {
				return true
			}
		}
		return false
	}

	expected, ok := want.(string)
	if !ok {
		return false
	}
	switch op {
	case "eq":
		return strings.EqualFold(actual, expected)
	case "neq":
		return !strings.EqualFold(actual, expected)
	case "contains":
		return strings.Contains(strings.ToLower(actual), strings.ToLower(expected))
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func truthy(v any) bool {
	if v == nil {
		return true
	}
	b, ok := v.(bool)
	if !ok {
		return true
	}
	return b
}

// Match is one rule that fired for a marble.
type Match struct {
	Rule   store.DispatchRule
	Marble store.Marble
}

// Evaluate returns every active rule whose condition matches the marble.
func Evaluate(rs []store.DispatchRule, m *store.Marble) []store.DispatchRule {
	out := []store.DispatchRule{}
	for _, r := range rs {
		if !r.IsActive {
			continue
		}
		// Project-scoped rules only apply to their project.
		if r.ProjectID != nil && (m.ProjectID == nil || *r.ProjectID != *m.ProjectID) {
			continue
		}
		cond, err := Parse(r.Condition)
		if err != nil {
			continue
		}
		if cond.Matches(m) {
			out = append(out, r)
		}
	}
	return out
}
