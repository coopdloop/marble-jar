package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider token endpoints. Only "common" is configured for Entra, which accepts
// personal accounts and any organisation's workforce tenants for delegated
// refresh; a single-tenant deployment can pin ENTRA_ID_TENANT_ID later.
const (
	slackTokenURL     = "https://slack.com/api/oauth.v2.access"
	atlassianTokenURL = "https://auth.atlassian.com/oauth/token"
	entraTokenURL     = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	entraScope        = "https://graph.microsoft.com/.default offline_access"
)

// RefreshResult is a renewed provider credential.
type RefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
}

// Refresher exchanges a stored refresh token for a fresh provider access token.
// BaseURLs overrides provider endpoints so tests can point them at a local
// server; HTTP nil means a 15s-timeout default client.
type Refresher struct {
	BaseURLs map[string]string
	HTTP     *http.Client
}

func NewRefresher() *Refresher { return &Refresher{} }

// Supported reports whether Marble Jar knows how to refresh this integration.
// Webhooks carry no user credential, so they never do.
func (r *Refresher) Supported(provider string) bool {
	_, ok := r.spec(provider)
	return ok
}

type refreshSpec struct {
	endpoint string
	form     bool          // urlencoded vs JSON body
	basic    bool          // client credentials over HTTP Basic
	scope    string        // sent when non-empty
	okFlag   bool          // slack: HTTP 200 can still be an error
	fallback time.Duration // assumed life when the provider omits expires_in
}

func (r *Refresher) spec(provider string) (refreshSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(provider))
	s, ok := map[string]refreshSpec{
		// Slack reports failures as HTTP 200 with ok:false, so always read the flag.
		"slack": {slackTokenURL, true, false, "", true, time.Hour},
		// Atlassian takes a JSON body with the app credentials in Basic auth.
		"jira": {atlassianTokenURL, false, true, "", false, time.Hour},
		// Entra needs form encoding and an explicit resource scope.
		"teams": {entraTokenURL, true, false, entraScope, false, time.Hour},
	}[key]
	if !ok {
		return refreshSpec{}, false
	}
	if base := r.BaseURLs[key]; base != "" {
		s.endpoint = base
	}
	return s, true
}

// Refresh performs the grant_type=refresh_token exchange. A PermanentError means
// the stored connection is dead and a human must re-authorise; a RetryableError
// means the provider is unwell and the dispatch may still go through later.
func (r *Refresher) Refresh(ctx context.Context, provider, clientID, clientSecret, refreshToken string) (*RefreshResult, error) {
	spec, ok := r.spec(provider)
	if !ok {
		return nil, PermanentError{fmt.Errorf("cannot refresh %q: no provider refresh flow is implemented", provider)}
	}
	if clientID == "" || clientSecret == "" {
		return nil, PermanentError{fmt.Errorf("refresh %s: OAuth client credentials are not configured", provider)}
	}
	if refreshToken == "" {
		return nil, PermanentError{fmt.Errorf("refresh %s: no refresh token stored", provider)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.endpoint, spec.body(clientID, clientSecret, refreshToken))
	if err != nil {
		return nil, RetryableError{fmt.Errorf("refresh %s: build request: %w", provider, err)}
	}
	req.Header.Set("Accept", "application/json")
	if spec.basic {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(
			[]byte(clientID+":"+clientSecret)))
	} else if spec.form {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client().Do(req)
	if err != nil {
		return nil, RetryableError{fmt.Errorf("refresh %s: %w", provider, err)}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if readErr != nil {
		return nil, RetryableError{fmt.Errorf("refresh %s: read response: %w", provider, readErr)}
	}
	if resp.StatusCode >= 400 {
		// Provider bodies sometimes echo request parameters, so the excerpt is
		// clipped and filtered before it can reach a log line or an audit row.
		detail := sanitizeExcerpt(body, refreshToken, clientSecret)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, RetryableError{fmt.Errorf("refresh %s: http %d: %s", provider, resp.StatusCode, detail)}
		}
		return nil, PermanentError{fmt.Errorf("refresh %s: http %d: %s", provider, resp.StatusCode, detail)}
	}

	var parsed struct {
		Ok           *bool  `json:"ok"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, PermanentError{fmt.Errorf("refresh %s: unreadable response: %s",
			provider, sanitizeExcerpt(body, refreshToken, clientSecret))}
	}
	// Slack's ok:false arrives with a 200 status, so status alone is not enough.
	if spec.okFlag {
		if parsed.Ok != nil && !*parsed.Ok {
			cause := parsed.Error
			if cause == "" {
				cause = "provider reported failure"
			}
			return nil, permanentRefreshError(provider, cause)
		}
		if parsed.Ok == nil {
			return nil, PermanentError{fmt.Errorf("refresh %s: response carries no ok flag", provider)}
		}
	}
	if parsed.AccessToken == "" {
		cause := parsed.Error
		if cause == "" {
			cause = parsed.ErrorDesc
		}
		if cause == "" {
			cause = "no access_token in response"
		}
		return nil, permanentRefreshError(provider, cause)
	}

	out := &RefreshResult{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    spec.fallback,
	}
	if parsed.ExpiresIn > 0 {
		out.ExpiresIn = time.Duration(parsed.ExpiresIn) * time.Second
	}
	return out, nil
}

func (s refreshSpec) body(clientID, clientSecret, refreshToken string) io.Reader {
	fields := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	if scope := s.scope; scope != "" {
		fields["scope"] = scope
	}
	if !s.form {
		// Atlassian takes the grant as JSON; its credentials go in Basic auth.
		encoded, err := json.Marshal(fields)
		if err != nil { // unreachable for this shape
			return strings.NewReader("{}")
		}
		return strings.NewReader(string(encoded))
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		// The two form-encoded providers carry their credentials in the body;
		// the JSON one (Atlassian) never reaches this branch.
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	if s.scope != "" {
		form.Set("scope", s.scope)
	}
	return strings.NewReader(form.Encode())
}

func permanentRefreshError(provider, cause string) error {
	// invalid_grant and friends mean the human must reconnect, not that we retry.
	switch cause {
	case "invalid_grant", "invalid_request", "interaction_required", "login_required",
		"consent_required", "unauthorized_client", "invalid_client":
		return PermanentError{fmt.Errorf("refresh %s: %s — reconnect required", provider, cause)}
	}
	return PermanentError{fmt.Errorf("refresh %s: %s", provider, cause)}
}

// sanitizeExcerpt clips a provider response and strips the secrets we sent it,
// because Slack and friends occasionally echo request parameters back.
func sanitizeExcerpt(body []byte, secrets ...string) string {
	text := strings.TrimSpace(string(body))
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}

func (r *Refresher) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}
