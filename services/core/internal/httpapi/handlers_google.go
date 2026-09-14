package httpapi

// Google sign-in for the dashboard. Password accounts are gone: a human proves
// who they are by completing Google's authorization code flow, and this server
// trades the resulting ID token for a Marble Jar session.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/google"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// googleProvider is the auth_provider discriminator stored on users.
const googleProvider = "google"

// googleStateCookie binds a callback to the browser that started the flow, so
// someone else's authorization code cannot be replayed into this session.
const googleStateCookie = "mj_google_state"

// GET /v1/auth/config — lets the dashboard offer only what the server supports.
func (s *Server) authConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"methods": gin.H{"google": s.cfg.GoogleOAuthConfigured()},
	})
}

// GET /v1/auth/google/start — redirect the browser to Google.
//
// Optional ?workspace=<slug-or-uuid> joins that workspace as a member instead of
// provisioning a personal one.
func (s *Server) googleStart(c *gin.Context) {
	if !s.cfg.GoogleOAuthConfigured() {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "google sign-in is not configured",
			"hint":  "set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET on marble_jar_core",
		})
		return
	}

	// Identity cannot ride in a header on a browser redirect, so — like the
	// integration connect flow — it rides in the signed state token.
	state, err := s.signState(oauthState{
		OrgID:    strings.TrimSpace(c.Query("workspace")),
		Provider: googleProvider,
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	http.SetCookie(c.Writer, newGoogleStateCookie(s.cfg.PublicURL, state))
	c.Redirect(http.StatusFound, google.AuthorizeURL+"?"+url.Values{
		"client_id":     {s.cfg.GoogleClientID},
		"redirect_uri":  {s.googleRedirectURI()},
		"scope":         {google.OIDCScope},
		"response_type": {"code"},
		"state":         {state},
		"prompt":        {"select_account"},
	}.Encode())
}

// GET /v1/auth/google/callback — Google's redirect target. Unauthenticated by
// design: the signed state token and its cookie are the credentials.
func (s *Server) googleCallback(c *gin.Context) {
	fail := func(msg string) {
		s.log.Warn("google sign-in failed", "error", msg)
		http.SetCookie(c.Writer, &http.Cookie{
			Name: googleStateCookie, Value: "", Path: "/v1/auth/google",
			MaxAge: -1, HttpOnly: true,
		})
		c.Redirect(http.StatusFound,
			s.cfg.WebAppURL+"/login?error="+url.QueryEscape(msg))
	}

	if !s.cfg.GoogleOAuthConfigured() {
		fail("google sign-in is not configured on this server")
		return
	}
	if c.Query("error") != "" {
		fail("google said: " + c.Query("error_description"))
		return
	}

	state, err := s.verifyState(c.Query("state"), googleProvider)
	if err != nil {
		fail(err.Error())
		return
	}
	if cookie, err := c.Cookie(googleStateCookie); err != nil || cookie != c.Query("state") {
		fail("state mismatch — start the sign-in again")
		return
	}
	if c.Query("code") == "" {
		fail("google returned no authorization code")
		return
	}

	ctx := c.Request.Context()
	idToken, err := s.exchangeGoogleCode(ctx, c.Query("code"))
	if err != nil {
		fail(err.Error())
		return
	}
	identity, err := s.googleVerifier().Verify(ctx, idToken)
	if err != nil {
		fail(err.Error())
		return
	}
	if !s.cfg.AllowsGoogleDomain(identity.Domain()) {
		fail("accounts from " + identity.Domain() + " are not allowed here")
		return
	}

	user, err := s.provisionGoogleUser(ctx, state.OrgID, identity)
	if err != nil {
		fail(err.Error())
		return
	}
	if !user.IsActive {
		fail("this account has been disabled")
		return
	}

	// The session itself never appears in a URL: the browser redeems this code
	// over POST from the dashboard.
	loginCode, err := s.loginCodes().Issue(user.ID)
	if err != nil {
		fail("could not start the session")
		return
	}

	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: user.OrganizationID,
		ActingUserID:   &user.ID,
		Provider:       googleProvider,
		Action:         "auth.sign_in",
		Details:        map[string]any{"email": user.Email},
	}); err != nil {
		s.log.Warn("audit sign-in failed", "error", err)
	}

	c.Redirect(http.StatusFound,
		s.cfg.WebAppURL+"/login?login_code="+url.QueryEscape(loginCode))
}

type exchangeBody struct {
	LoginCode string `json:"login_code" binding:"required"`
}

// POST /v1/auth/exchange — trade the one-time login code for session tokens.
func (s *Server) exchangeLoginCode(c *gin.Context) {
	var body exchangeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, ok := s.loginCodes().Consume(body.LoginCode)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired login code"})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil || !user.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found or inactive"})
		return
	}
	org, err := s.store.GetOrganization(ctx, user.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	tokens, err := s.auth.IssueTokens(user.ID, user.OrganizationID, user.Email, user.Role, nil)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user, "organization": org, "tokens": tokens})
}

// provisionGoogleUser resolves a verified Google identity to a local user:
// the account already linked to it, else the account with the same email (which
// gets linked now), else a freshly provisioned one.
func (s *Server) provisionGoogleUser(ctx context.Context, workspaceHint string,
	identity *google.Identity) (*store.User, error) {

	if user, err := s.store.GetUserByProviderSubject(ctx, googleProvider, identity.Subject); err == nil {
		// Returning user: refresh the profile Google claims for them.
		return s.store.LinkProviderIdentity(ctx, user.ID, googleProvider,
			identity.Subject, identity.Name, identity.Picture)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// First Google sign-in for an address that already owns an account — this is
	// how a workspace created under passwords keeps its history and roles.
	if existing, err := s.store.GetUserByEmail(ctx, identity.Email); err == nil {
		return s.store.LinkProviderIdentity(ctx, existing.ID, googleProvider,
			identity.Subject, identity.Name, identity.Picture)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	orgID, role := "", "member"
	if workspaceHint != "" {
		org, err := s.findOrganization(ctx, workspaceHint)
		if err != nil {
			return nil, fmt.Errorf("workspace %q not found", workspaceHint)
		}
		orgID = org.ID
	} else {
		name := strings.SplitN(identity.Email, "@", 2)[0] + "'s jar"
		org, err := s.store.CreateOrganization(ctx, name, orgSlug(name))
		if err != nil {
			return nil, err
		}
		orgID = org.ID
		role = "owner" // whoever opens a jar owns it
	}

	user, err := s.store.CreateUser(ctx, store.NewUser{
		OrganizationID: orgID, Email: identity.Email, DisplayName: identity.Name,
		AvatarURL: identity.Picture, Role: role,
		AuthProvider: googleProvider, ProviderSubject: identity.Subject,
	})
	if errors.Is(err, store.ErrConflict) {
		// A simultaneous first sign-in for this person won the unique index;
		// continue as them rather than failing the redirect.
		if existing, lookupErr := s.store.GetUserByProviderSubject(ctx, googleProvider, identity.Subject); lookupErr == nil {
			return existing, nil
		}
		if existing, lookupErr := s.store.GetUserByEmail(ctx, identity.Email); lookupErr == nil {
			return existing, nil
		}
	}
	return user, err
}

func (s *Server) findOrganization(ctx context.Context, hint string) (*store.Organization, error) {
	if _, err := uuid.Parse(hint); err == nil {
		return s.store.GetOrganization(ctx, hint)
	}
	return s.store.GetOrganizationBySlug(ctx, orgSlug(hint))
}

func (s *Server) googleRedirectURI() string {
	return s.cfg.PublicURL + "/v1/auth/google/callback"
}

func newGoogleStateCookie(publicURL, value string) *http.Cookie {
	return &http.Cookie{
		Name:     googleStateCookie,
		Value:    value,
		Path:     "/v1/auth/google",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(publicURL, "https://"),
		// Lax keeps the cookie on the top-level GET redirect back from Google.
		SameSite: http.SameSiteLaxMode,
	}
}

// exchangeGoogleCode swaps the authorization code for tokens and returns the
// ID token, which is the only one we need — identity, not API access.
func (s *Server) exchangeGoogleCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {s.cfg.GoogleClientID},
		"client_secret": {s.cfg.GoogleClientSecret},
		"redirect_uri":  {s.googleRedirectURI()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, google.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("google token exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("google token exchange http %d: %s", resp.StatusCode, truncateBody(raw))
	}
	var res struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("google token exchange: decode: %w", err)
	}
	if res.IDToken == "" {
		return "", fmt.Errorf("google token exchange returned no id_token")
	}
	return res.IDToken, nil
}

// googleVerifier and loginCodes are built on first use so a Server assembled
// by hand (tests) still works without a constructor.
func (s *Server) googleVerifier() *google.Verifier {
	s.lazyMu.Lock()
	defer s.lazyMu.Unlock()
	if s.googleV == nil {
		s.googleV = google.NewVerifier(s.cfg.GoogleClientID)
	}
	return s.googleV
}

func (s *Server) loginCodes() *auth.LoginCodeStore {
	s.lazyMu.Lock()
	defer s.lazyMu.Unlock()
	if s.codes == nil {
		s.codes = auth.NewLoginCodeStore()
	}
	return s.codes
}
