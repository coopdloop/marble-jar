package dispatch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactTargetConfig(t *testing.T) {
	in := `{
		"channel": "#eng",
		"webhook_url": "https://hooks.slack.com/services/T00001/B00002/AAAABBBBccccDDDD",
		"secret": "per-target-hmac-key",
		"project_key": "PAY",
		"site_url": "https://acme.atlassian.net/rest/api/3/issue",
		"url": "https://example.com/hooks/marble?signature=deadbeef",
		"nested": {"bot_token": "xoxb-live-token", "team_id": "T9"},
		"headers": {"Authorization": "Bearer live-jira-token"}
	}`

	raw := redactTargetConfig(json.RawMessage(in))
	got := string(raw)

	for _, leak := range []string{
		"AAAABBBBccccDDDD", "T00001", "B00002", "per-target-hmac-key", "deadbeef",
		"xoxb-live-token", "live-jira-token", "rest/api/3",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted config still contains credential %q\n%s", leak, got)
		}
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("redacted config is not valid JSON: %v", err)
	}

	// Routing metadata has to survive, or the audit log stops explaining anything.
	for key, want := range map[string]string{
		"channel":     "#eng",
		"project_key": "PAY",
	} {
		if v, _ := out[key].(string); v != want {
			t.Errorf("%s = %q, want %q", key, v, want)
		}
	}

	for key, wantPrefix := range map[string]string{
		"webhook_url": "https://hooks.slack.com/",
		"site_url":    "https://acme.atlassian.net/",
		"url":         "https://example.com/",
	} {
		v, _ := out[key].(string)
		if !strings.HasPrefix(v, wantPrefix) || !strings.HasSuffix(v, "/[redacted]") {
			t.Errorf("%s = %q, want %s host with a redacted path", key, v, wantPrefix)
		}
	}
	if v, _ := out["secret"].(string); v != "[redacted]" {
		t.Errorf("secret = %q, want [redacted]", v)
	}

	nested, _ := out["nested"].(map[string]any)
	if v, _ := nested["bot_token"].(string); v != "[redacted]" {
		t.Errorf("nested bot_token = %q, want [redacted]", v)
	}
	if v, _ := nested["team_id"].(string); v != "T9" {
		t.Errorf("nested team_id = %q, want T9", v)
	}
	headers, _ := out["headers"].(map[string]any)
	if v, _ := headers["Authorization"].(string); v != "[redacted]" {
		t.Errorf("headers.Authorization = %q, want [redacted]", v)
	}
}

func TestRedactTargetConfig_Unwalkable(t *testing.T) {
	for _, in := range []string{`["https://hooks.slack.com/services/T/B/secret"]`, `17`, `"raw-secret"`} {
		raw := redactTargetConfig(json.RawMessage(in))
		if strings.Contains(string(raw), "secret") {
			t.Errorf("non-object config leaked: %s", raw)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("output must stay a JSON object: %v", err)
		}
	}

	// Empty in stays empty: nothing to redact, nothing to invent.
	if got := redactTargetConfig(nil); len(got) != 0 {
		t.Errorf("empty input produced %s", got)
	}
}
