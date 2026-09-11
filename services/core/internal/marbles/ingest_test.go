package marbles

import (
	"encoding/json"
	"testing"
)

func TestNormalizeAcceptsSnakeAndCamelCase(t *testing.T) {
	raw := `{
		"taskSummary": "Shipped the retry logic",
		"model": "claude-sonnet-4",
		"tokensInput": 900,
		"tokensOutput": 120,
		"costUsd": 0.031,
		"durationMs": 4200,
		"traceId": "abc123",
		"idempotencyKey": "key-1"
	}`

	var req LogMarbleRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if err := req.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if req.Summary != "Shipped the retry logic" {
		t.Errorf("summary = %q", req.Summary)
	}
	if *req.TokensInput != 900 || *req.TokensOutput != 120 {
		t.Errorf("tokens = %v/%v", *req.TokensInput, *req.TokensOutput)
	}
	if *req.CostUSD != 0.031 || *req.DurationMS != 4200 {
		t.Errorf("cost/duration = %v/%v", *req.CostUSD, *req.DurationMS)
	}
	if req.TraceID != "abc123" || req.IdempotencyKey != "key-1" {
		t.Errorf("trace/idempotency = %q/%q", req.TraceID, req.IdempotencyKey)
	}
	if req.Status != "logged" || req.Source != "sdk" {
		t.Errorf("defaults not applied: %q %q", req.Status, req.Source)
	}
}

func TestNormalizeNestedTokensObject(t *testing.T) {
	raw := `{"summary":"x","model":"m","tokens":{"in":10,"out":20}}`

	var req LogMarbleRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if *req.TokensInput != 10 || *req.TokensOutput != 20 {
		t.Errorf("nested tokens not folded: %v/%v", req.TokensInput, req.TokensOutput)
	}
}

func TestNormalizeValidation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing summary", `{"model":"m"}`},
		{"blank summary", `{"summary":"   ","model":"m"}`},
		{"missing model", `{"summary":"s"}`},
		{"negative cost", `{"summary":"s","model":"m","cost_usd":-1}`},
		{"bad status", `{"summary":"s","model":"m","status":"wat"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req LogMarbleRequest
			if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
				t.Fatal(err)
			}
			if err := req.normalize(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestNormalizeTruncatesLongSummary(t *testing.T) {
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'a'
	}
	req := LogMarbleRequest{Summary: string(long), Model: "m"}
	if err := req.normalize(); err != nil {
		t.Fatal(err)
	}
	if len(req.Summary) != 2000 {
		t.Errorf("summary length = %d, want 2000", len(req.Summary))
	}
}

func TestNormalizeRejectsInvalidMetadata(t *testing.T) {
	req := LogMarbleRequest{
		Summary:  "s",
		Model:    "m",
		Metadata: json.RawMessage(`{not json`),
	}
	if err := req.normalize(); err == nil {
		t.Error("expected invalid metadata to be rejected")
	}
}
