package dispatch

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// payloadFields is the normalized body core sends for both marble and
// objective-summary dispatches.
type payloadFields struct {
	MarbleID    string  `json:"marble_id"`
	ObjectiveID string  `json:"objective_id"`
	Summary     string  `json:"summary"`
	Body        string  `json:"body"`
	Title       string  `json:"title"`
	Project     string  `json:"project"`
	Agent       string  `json:"agent"`
	Model       string  `json:"model"`
	Status      string  `json:"status"`
	CostUSD     float64 `json:"cost_usd"`
	TokensIn    int     `json:"tokens_in"`
	TokensOut   int     `json:"tokens_out"`
	TotalTokens int64   `json:"total_tokens"`
	DurationMS  int64   `json:"duration_ms"`
	MarbleCount int     `json:"marble_count"`
	TraceURL    string  `json:"trace_url"`
	Kind        string  `json:"kind"`
}

func parsePayload(raw json.RawMessage) payloadFields {
	var p payloadFields
	_ = json.Unmarshal(raw, &p)
	return p
}

// text builds the human-facing message body shared by chat integrations.
func (p payloadFields) text() string {
	if p.Body != "" {
		return p.Body
	}
	var b strings.Builder
	if p.Kind == "objective_summary" {
		b.WriteString("🫙 " + p.Summary)
	} else {
		b.WriteString("🔵 " + p.Summary)
		meta := []string{}
		if p.Model != "" {
			meta = append(meta, p.Model)
		}
		if p.Project != "" {
			meta = append(meta, p.Project)
		}
		if p.CostUSD > 0 {
			meta = append(meta, fmt.Sprintf("$%.4f", p.CostUSD))
		}
		if total := p.TokensIn + p.TokensOut; total > 0 {
			meta = append(meta, fmt.Sprintf("%d tokens", total))
		}
		if len(meta) > 0 {
			b.WriteString("\n" + strings.Join(meta, " · "))
		}
	}
	if p.TraceURL != "" {
		b.WriteString("\n" + p.TraceURL)
	}
	return b.String()
}

func doJSON(ctx context.Context, client *http.Client, method, url, token string, body any, extraHeaders map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, PermanentError{err}
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, PermanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, RetryableError{err} // network failures are transient
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, respBody, nil
}

// classify maps HTTP status onto the retry policy.
func classify(status int, body []byte) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 408, status == 429, status >= 500:
		return RetryableError{fmt.Errorf("http %d: %s", status, truncate(body))}
	default:
		return PermanentError{fmt.Errorf("http %d: %s", status, truncate(body))}
	}
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

func newClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

// ---------- Jira ----------

// JiraExecutor creates and updates Jira issues via the Atlassian Cloud REST API
// using the acting user's OBO token.
type JiraExecutor struct{ client *http.Client }

func NewJiraExecutor() *JiraExecutor { return &JiraExecutor{client: newClient()} }

type jiraConfig struct {
	CloudID    string `json:"cloud_id"`
	SiteURL    string `json:"site_url"`
	ProjectKey string `json:"project_key"`
	IssueType  string `json:"issue_type"`
	IssueKey   string `json:"issue_key"`
}

func (e *JiraExecutor) Execute(ctx context.Context, in Intent, token string) (string, map[string]any, error) {
	var cfg jiraConfig
	if err := json.Unmarshal(in.TargetConfig, &cfg); err != nil {
		return "", nil, PermanentError{fmt.Errorf("invalid jira target_config: %w", err)}
	}
	if token == "" {
		return "", nil, PermanentError{fmt.Errorf("no Jira OBO token for acting user")}
	}

	base := strings.TrimRight(cfg.SiteURL, "/")
	if cfg.CloudID != "" {
		base = "https://api.atlassian.com/ex/jira/" + cfg.CloudID
	}
	if base == "" {
		return "", nil, PermanentError{fmt.Errorf("jira target_config requires cloud_id or site_url")}
	}

	p := parsePayload(in.Payload)
	title := p.Title
	if title == "" {
		title = p.Summary
	}
	if len(title) > 250 {
		title = title[:250]
	}

	if in.Action == "update_issue" {
		if cfg.IssueKey == "" {
			return "", nil, PermanentError{fmt.Errorf("update_issue requires issue_key")}
		}
		url := fmt.Sprintf("%s/rest/api/3/issue/%s/comment", base, cfg.IssueKey)
		status, body, err := doJSON(ctx, e.client, http.MethodPost, url, token,
			map[string]any{"body": adf(p.text())}, nil)
		if err != nil {
			return "", nil, err
		}
		if err := classify(status, body); err != nil {
			return "", nil, err
		}
		return cfg.IssueKey, map[string]any{
			"issue_key": cfg.IssueKey, "action_type": "update", "site_id": cfg.CloudID,
		}, nil
	}

	if cfg.ProjectKey == "" {
		return "", nil, PermanentError{fmt.Errorf("create_issue requires project_key")}
	}
	issueType := cfg.IssueType
	if issueType == "" {
		issueType = "Task"
	}

	url := base + "/rest/api/3/issue"
	status, body, err := doJSON(ctx, e.client, http.MethodPost, url, token, map[string]any{
		"fields": map[string]any{
			"project":     map[string]string{"key": cfg.ProjectKey},
			"summary":     title,
			"issuetype":   map[string]string{"name": issueType},
			"description": adf(p.text()),
		},
	}, nil)
	if err != nil {
		return "", nil, err
	}
	if err := classify(status, body); err != nil {
		return "", nil, err
	}

	var created struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(body, &created)
	return created.Key, map[string]any{
		"issue_key": created.Key, "project_key": cfg.ProjectKey,
		"issue_type": issueType, "action_type": "create", "site_id": cfg.CloudID,
	}, nil
}

// adf wraps plain text in Atlassian Document Format.
func adf(text string) map[string]any {
	paragraphs := []any{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		paragraphs = append(paragraphs, map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": line}},
		})
	}
	if len(paragraphs) == 0 {
		paragraphs = append(paragraphs, map[string]any{"type": "paragraph"})
	}
	return map[string]any{"type": "doc", "version": 1, "content": paragraphs}
}

// ---------- Slack ----------

type SlackExecutor struct{ client *http.Client }

func NewSlackExecutor() *SlackExecutor { return &SlackExecutor{client: newClient()} }

type slackConfig struct {
	Channel    string `json:"channel"`
	ThreadTS   string `json:"thread_ts"`
	WebhookURL string `json:"webhook_url"`
}

func (e *SlackExecutor) Execute(ctx context.Context, in Intent, token string) (string, map[string]any, error) {
	var cfg slackConfig
	if err := json.Unmarshal(in.TargetConfig, &cfg); err != nil {
		return "", nil, PermanentError{fmt.Errorf("invalid slack target_config: %w", err)}
	}
	p := parsePayload(in.Payload)

	// Incoming-webhook mode: no OBO token, no message ts returned.
	if cfg.WebhookURL != "" {
		status, body, err := doJSON(ctx, e.client, http.MethodPost, cfg.WebhookURL, "",
			map[string]any{"text": p.text()}, nil)
		if err != nil {
			return "", nil, err
		}
		if err := classify(status, body); err != nil {
			return "", nil, err
		}
		return "", map[string]any{"channel_id": cfg.Channel}, nil
	}

	if token == "" {
		return "", nil, PermanentError{fmt.Errorf("no Slack OBO token for acting user")}
	}
	if cfg.Channel == "" {
		return "", nil, PermanentError{fmt.Errorf("slack target_config requires channel or webhook_url")}
	}

	req := map[string]any{"channel": cfg.Channel, "text": p.text()}
	if cfg.ThreadTS != "" {
		req["thread_ts"] = cfg.ThreadTS
	}

	status, body, err := doJSON(ctx, e.client, http.MethodPost,
		"https://slack.com/api/chat.postMessage", token, req, nil)
	if err != nil {
		return "", nil, err
	}
	if err := classify(status, body); err != nil {
		return "", nil, err
	}

	// Slack returns 200 with ok:false for logical errors.
	var res struct {
		OK      bool   `json:"ok"`
		TS      string `json:"ts"`
		Channel string `json:"channel"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", nil, RetryableError{fmt.Errorf("decode slack response: %w", err)}
	}
	if !res.OK {
		if res.Error == "ratelimited" || res.Error == "service_unavailable" {
			return "", nil, RetryableError{fmt.Errorf("slack: %s", res.Error)}
		}
		return "", nil, PermanentError{fmt.Errorf("slack: %s", res.Error)}
	}
	return res.TS, map[string]any{"channel_id": res.Channel, "message_ts": res.TS}, nil
}

// ---------- Microsoft Teams ----------

type TeamsExecutor struct{ client *http.Client }

func NewTeamsExecutor() *TeamsExecutor { return &TeamsExecutor{client: newClient()} }

type teamsConfig struct {
	TeamID     string `json:"team_id"`
	ChannelID  string `json:"channel_id"`
	WebhookURL string `json:"webhook_url"`
}

func (e *TeamsExecutor) Execute(ctx context.Context, in Intent, token string) (string, map[string]any, error) {
	var cfg teamsConfig
	if err := json.Unmarshal(in.TargetConfig, &cfg); err != nil {
		return "", nil, PermanentError{fmt.Errorf("invalid teams target_config: %w", err)}
	}
	p := parsePayload(in.Payload)

	// Incoming-webhook (connector) mode.
	if cfg.WebhookURL != "" {
		status, body, err := doJSON(ctx, e.client, http.MethodPost, cfg.WebhookURL, "",
			map[string]any{"text": p.text()}, nil)
		if err != nil {
			return "", nil, err
		}
		if err := classify(status, body); err != nil {
			return "", nil, err
		}
		return "", map[string]any{"channel_id": cfg.ChannelID}, nil
	}

	if token == "" {
		return "", nil, PermanentError{fmt.Errorf("no Teams OBO token for acting user")}
	}
	if cfg.TeamID == "" || cfg.ChannelID == "" {
		return "", nil, PermanentError{fmt.Errorf("teams target_config requires team_id and channel_id")}
	}

	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/teams/%s/channels/%s/messages",
		cfg.TeamID, cfg.ChannelID)
	status, body, err := doJSON(ctx, e.client, http.MethodPost, url, token, map[string]any{
		"body": map[string]any{"contentType": "text", "content": p.text()},
	}, nil)
	if err != nil {
		return "", nil, err
	}
	if err := classify(status, body); err != nil {
		return "", nil, err
	}

	var res struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &res)
	return res.ID, map[string]any{
		"team_id": cfg.TeamID, "channel_id": cfg.ChannelID, "message_id": res.ID,
	}, nil
}

// ---------- generic webhook ----------

// WebhookExecutor fires HMAC-signed payloads at customer-owned endpoints.
type WebhookExecutor struct {
	client *http.Client
	secret string
}

func NewWebhookExecutor(fallbackSecret string) *WebhookExecutor {
	return &WebhookExecutor{client: newClient(), secret: fallbackSecret}
}

type webhookConfig struct {
	URL    string            `json:"url"`
	Secret string            `json:"secret"`
	Header map[string]string `json:"headers"`
}

func (e *WebhookExecutor) Execute(ctx context.Context, in Intent, _ string) (string, map[string]any, error) {
	var cfg webhookConfig
	if err := json.Unmarshal(in.TargetConfig, &cfg); err != nil {
		return "", nil, PermanentError{fmt.Errorf("invalid webhook target_config: %w", err)}
	}
	if cfg.URL == "" {
		return "", nil, PermanentError{fmt.Errorf("webhook target_config requires url")}
	}

	body, err := json.Marshal(map[string]any{
		"event":        "marblejar.dispatch",
		"dispatch_id":  in.DispatchID,
		"marble_id":    in.MarbleID,
		"objective_id": in.ObjectiveID,
		"payload":      in.Payload,
		"timestamp":    time.Now().UTC(),
	})
	if err != nil {
		return "", nil, PermanentError{err}
	}

	secret := cfg.Secret
	if secret == "" {
		secret = e.secret
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return "", nil, PermanentError{err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MarbleJar-Signature", signature)
	req.Header.Set("X-MarbleJar-Dispatch-Id", in.DispatchID)
	for k, v := range cfg.Header {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return "", nil, RetryableError{err}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))

	detail := map[string]any{
		"target_url":    cfg.URL,
		"http_status":   resp.StatusCode,
		"signature":     signature,
		"response_body": truncate(respBody),
	}
	if err := classify(resp.StatusCode, respBody); err != nil {
		return "", detail, err
	}
	return in.DispatchID, detail, nil
}
