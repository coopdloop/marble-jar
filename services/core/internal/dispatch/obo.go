package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OBOProvider performs RFC 8693 token exchange against Ory Hydra/Auth0 so every
// outbound action carries the acting user's identity rather than a shared
// service credential.
type OBOProvider struct {
	issuerURL    string
	clientID     string
	clientSecret string
	client       *http.Client
	log          *slog.Logger

	mu    sync.RWMutex
	cache map[string]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

func NewOBOProvider(issuerURL, clientID, clientSecret string, log *slog.Logger) *OBOProvider {
	return &OBOProvider{
		issuerURL:    strings.TrimRight(issuerURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       &http.Client{Timeout: 15 * time.Second},
		log:          log,
		cache:        map[string]cachedToken{},
	}
}

// audienceFor maps an integration onto its resource audience.
func audienceFor(provider string) string {
	switch provider {
	case "jira":
		return "https://api.atlassian.com"
	case "slack":
		return "https://slack.com/api"
	case "teams":
		return "https://graph.microsoft.com"
	}
	return provider
}

// OBOToken returns a short-lived access token for (org, user, provider).
func (p *OBOProvider) OBOToken(ctx context.Context, orgID string, userID *string, provider string) (string, error) {
	if p.issuerURL == "" {
		// No issuer configured: executors that require a token fail loudly,
		// webhook-style targets keep working.
		return "", nil
	}
	if userID == nil || *userID == "" {
		return "", fmt.Errorf("dispatch has no acting user for OBO exchange")
	}

	key := orgID + "|" + *userID + "|" + provider
	p.mu.RLock()
	if c, ok := p.cache[key]; ok && time.Now().Before(c.expiresAt.Add(-30*time.Second)) {
		p.mu.RUnlock()
		return c.token, nil
	}
	p.mu.RUnlock()

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("subject_token", *userID)
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:id_token")
	form.Set("requested_token_type", "urn:ietf:params:oauth:token-type:access_token")
	form.Set("audience", audienceFor(provider))
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.issuerURL+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", RetryableError{fmt.Errorf("token exchange: %w", err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))

	if resp.StatusCode >= 400 {
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			return "", RetryableError{fmt.Errorf("token exchange http %d: %s", resp.StatusCode, truncate(body))}
		}
		return "", PermanentError{fmt.Errorf("token exchange http %d: %s", resp.StatusCode, truncate(body))}
	}

	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if res.AccessToken == "" {
		return "", PermanentError{fmt.Errorf("token exchange returned no access_token")}
	}

	ttl := time.Duration(res.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	p.mu.Lock()
	p.cache[key] = cachedToken{token: res.AccessToken, expiresAt: time.Now().Add(ttl)}
	p.mu.Unlock()

	return res.AccessToken, nil
}
