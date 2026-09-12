package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// The connect flow is a browser redirect, so it cannot carry the session JWT.
// Identity rides in an HMAC-signed, short-lived state token instead.
const oauthStateTTL = 15 * time.Minute

type oauthState struct {
	OrgID    string
	UserID   string
	Provider string
	Nonce    string
	Expiry   time.Time
}

func (s *Server) signState(st oauthState) (string, error) {
	nonce, err := randomSecret()
	if err != nil {
		return "", err
	}
	st.Nonce = nonce
	if st.Expiry.IsZero() {
		st.Expiry = time.Now().Add(oauthStateTTL)
	}
	payload := strings.Join([]string{
		st.OrgID, st.UserID, st.Provider, st.Nonce,
		strconv.FormatInt(st.Expiry.Unix(), 10),
	}, "|")
	mac := hmac.New(sha256.New, []byte(s.cfg.HMACDispatchSecret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) verifyState(token, provider string) (*oauthState, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed state")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed state payload")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("malformed state signature")
	}
	mac := hmac.New(sha256.New, []byte(s.cfg.HMACDispatchSecret))
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, fmt.Errorf("invalid state signature")
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 5 {
		return nil, fmt.Errorf("malformed state fields")
	}
	exp, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || time.Now().After(time.Unix(exp, 0)) {
		return nil, fmt.Errorf("state expired — restart the connect flow")
	}
	if fields[2] != provider {
		return nil, fmt.Errorf("state/provider mismatch")
	}
	return &oauthState{
		OrgID: fields[0], UserID: fields[1], Provider: fields[2],
		Nonce: fields[3], Expiry: time.Unix(exp, 0),
	}, nil
}

// providerApp bundles everything the connect flow needs for one provider.
type providerApp struct {
	clientID     string
	clientSecret string
	scopes       []string
}

func (s *Server) appFor(provider string) (*providerApp, error) {
	switch provider {
	case "slack":
		if s.cfg.SlackClientID == "" || s.cfg.SlackClientSecret == "" {
			return nil, fmt.Errorf("set SLACK_OAUTH_CLIENT_ID and SLACK_OAUTH_CLIENT_SECRET to enable Slack")
		}
		return &providerApp{s.cfg.SlackClientID, s.cfg.SlackClientSecret, providerScopes(provider)}, nil
	case "jira":
		if s.cfg.AtlassianClientID == "" || s.cfg.AtlassianClientSecret == "" {
			return nil, fmt.Errorf("set ATLASSIAN_OAUTH_CLIENT_ID and ATLASSIAN_OAUTH_CLIENT_SECRET to enable Jira")
		}
		return &providerApp{s.cfg.AtlassianClientID, s.cfg.AtlassianClientSecret, providerScopes(provider)}, nil
	case "teams":
		if s.cfg.EntraTenantID == "" || s.cfg.EntraClientID == "" || s.cfg.EntraClientSecret == "" {
			return nil, fmt.Errorf("set ENTRA_ID_TENANT_ID, ENTRA_ID_CLIENT_ID and ENTRA_ID_CLIENT_SECRET to enable Teams")
		}
		return &providerApp{s.cfg.EntraClientID, s.cfg.EntraClientSecret, providerScopes(provider)}, nil
	}
	return nil, fmt.Errorf("unsupported integration")
}

func (s *Server) redirectURI(provider string) string {
	return s.cfg.PublicURL + "/v1/integrations/" + provider + "/callback"
}

func (s *Server) authorizeURL(provider string, app *providerApp, state string) string {
	redirect := s.redirectURI(provider)
	switch provider {
	case "slack":
		// user_scope (not scope) so the token acts as the connecting person.
		return "https://slack.com/oauth/v2/authorize?" + url.Values{
			"client_id":    {app.clientID},
			"user_scope":   {strings.Join(app.scopes, ",")},
			"redirect_uri": {redirect},
			"state":        {state},
		}.Encode()
	case "jira":
		return "https://auth.atlassian.com/authorize?" + url.Values{
			"audience":      {"api.atlassian.com"},
			"client_id":     {app.clientID},
			"scope":         {strings.Join(app.scopes, " ")},
			"redirect_uri":  {redirect},
			"state":         {state},
			"response_type": {"code"},
			"prompt":        {"consent"},
		}.Encode()
	case "teams":
		return "https://login.microsoftonline.com/" + s.cfg.EntraTenantID + "/oauth2/v2.0/authorize?" + url.Values{
			"client_id":     {app.clientID},
			"scope":         {strings.Join(app.scopes, " ")},
			"redirect_uri":  {redirect},
			"state":         {state},
			"response_type": {"code"},
			"response_mode": {"query"},
		}.Encode()
	}
	return ""
}

type exchangedToken struct {
	AccessToken  string
	RefreshToken *string
	AccountID    string
	Scopes       []string
	ExpiresAt    *time.Time
}

func (s *Server) exchangeCode(ctx context.Context, provider, code string) (*exchangedToken, error) {
	app, err := s.appFor(provider)
	if err != nil {
		return nil, err
	}

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {s.redirectURI(provider)},
	}
	var endpoint string
	switch provider {
	case "slack":
		endpoint = "https://slack.com/api/oauth.v2.access"
		form.Set("client_id", app.clientID)
		form.Set("client_secret", app.clientSecret)
	case "jira":
		endpoint = "https://auth.atlassian.com/oauth/token"
	case "teams":
		endpoint = "https://login.microsoftonline.com/" + s.cfg.EntraTenantID + "/oauth2/v2.0/token"
		form.Set("client_id", app.clientID)
		form.Set("client_secret", app.clientSecret)
		form.Set("scope", strings.Join(app.scopes, " "))
	default:
		return nil, fmt.Errorf("unsupported integration")
	}

	var body io.Reader = strings.NewReader(form.Encode())
	contentType := "application/x-www-form-urlencoded"
	if provider == "jira" {
		// Atlassian wants JSON for the code exchange.
		b, err := json.Marshal(map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     app.clientID,
			"client_secret": app.clientSecret,
			"code":          code,
			"redirect_uri":  s.redirectURI(provider),
		})
		if err != nil {
			return nil, err
		}
		body = strings.NewReader(string(b))
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s token exchange: %w", provider, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))

	if provider == "slack" {
		return parseSlackToken(raw)
	}
	return parseOAuthToken(provider, resp.StatusCode, raw)
}

// Slack wraps errors in 200s and nests the user token under authed_user.
func parseSlackToken(raw []byte) (*exchangedToken, error) {
	var res struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		Scope      string `json:"scope"`
		AuthedUser struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
			Scope       string `json:"scope"`
		} `json:"authed_user"`
		Team struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("slack token exchange: decode: %w", err)
	}
	if !res.OK {
		return nil, fmt.Errorf("slack token exchange: %s", res.Error)
	}
	token := res.AuthedUser.AccessToken
	scopes := res.AuthedUser.Scope
	if token == "" {
		return nil, fmt.Errorf("slack returned no user token — is user_scope set on the authorize request?")
	}
	account := res.AuthedUser.ID
	if res.Team.ID != "" {
		account = res.Team.ID + "/" + res.AuthedUser.ID
		if res.Team.Name != "" {
			account = res.Team.Name + " (" + account + ")"
		}
	}
	return &exchangedToken{
		AccessToken: token,
		AccountID:   account,
		Scopes:      strings.Split(scopes, ","),
	}, nil
}

func parseOAuthToken(provider string, status int, raw []byte) (*exchangedToken, error) {
	if status >= 400 {
		return nil, fmt.Errorf("%s token exchange http %d: %s", provider, status, truncateBody(raw))
	}
	var res struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("%s token exchange: decode: %w", provider, err)
	}
	if res.AccessToken == "" {
		return nil, fmt.Errorf("%s token exchange returned no access_token", provider)
	}
	out := &exchangedToken{AccessToken: res.AccessToken}
	if res.RefreshToken != "" {
		out.RefreshToken = &res.RefreshToken
	}
	if res.Scope != "" {
		out.Scopes = strings.Fields(res.Scope)
	}
	if res.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
		out.ExpiresAt = &exp
	}
	return out, nil
}

func truncateBody(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}

// GET /v1/integrations/:integration/callback — provider redirect target.
// Unauthenticated by design: the signed state token is the credential.
func (s *Server) integrationCallback(c *gin.Context) {
	provider := c.Param("integration")
	fail := func(msg string) {
		s.log.Warn("oauth callback failed", "provider", provider, "error", msg)
		c.Redirect(http.StatusFound, s.cfg.WebAppURL+"/integrations?error="+url.QueryEscape(msg))
	}

	if errMsg := c.Query("error"); errMsg != "" {
		fail(provider + " denied the request: " + c.Query("error_description"))
		return
	}

	state, err := s.verifyState(c.Query("state"), provider)
	if err != nil {
		fail(err.Error())
		return
	}
	code := c.Query("code")
	if code == "" {
		fail("missing authorization code")
		return
	}

	tok, err := s.exchangeCode(c.Request.Context(), provider, code)
	if err != nil {
		fail(err.Error())
		return
	}
	if len(tok.Scopes) == 0 {
		tok.Scopes = providerScopes(provider)
	}

	ctx := c.Request.Context()
	if _, err := s.store.UpsertOAuthConnection(ctx, state.OrgID, state.UserID,
		provider, tok.AccountID, tok.AccessToken, tok.RefreshToken, tok.Scopes, tok.ExpiresAt); err != nil {
		respondStoreErr(c, err)
		return
	}

	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: state.OrgID,
		ActingUserID:   &state.UserID,
		Provider:       provider,
		Action:         "integration.connected",
		Details:        map[string]any{"account_id": tok.AccountID, "scopes": tok.Scopes},
	}); err != nil {
		s.log.Warn("audit connect failed", "error", err)
	}

	c.Redirect(http.StatusFound, s.cfg.WebAppURL+"/integrations?connected="+provider)
}
