package httpapi

// Google sign-in for the dashboard. Password accounts are gone: a human proves
// who they are by completing Google's authorization code flow, and this server
// trades the resulting ID token for a Marble Jar session.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/google"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// googleProvider is the auth_provider discriminator stored on users.
const googleProvider = "google"

// googleStateCookie binds a callback to the browser that started the flow, so
// someone else's authorization code cannot be replayed into this session.
const googleStateCookie = "mj_google_state"

// The state and invite tokens are signed with the same secret as the
// integration flow, so each carries a purpose tag: a sign-in state can never be
// replayed as an invite, or the other way round.
const (
	googlePurposeSignIn = "mj.google-signin"
	googlePurposeInvite = "mj.google-invite"
	googleSignInTTL     = 15 * time.Minute
	googleInviteTTL     = 7 * 24 * time.Hour
)

// googleAuthState is everything the callback must remember about how the flow
// started: which workspace an invite pointed at, the nonce that the ID token
// must carry, and the PKCE verifier the code exchange needs.
type googleAuthState struct {
	Workspace string `json:"w,omitempty"`
	Invited   bool   `json:"i,omitempty"`
	Nonce     string `json:"n"`
	Verifier  string `json:"v,omitempty"`
	Expires   int64  `json:"e"`
}

func (st *googleAuthState) expired() bool { return time.Now().Unix() > st.Expires }

// GET /v1/auth/config — lets the dashboard offer only what the server supports.
func (s *Server) authConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"methods": gin.H{"google": s.cfg.GoogleOAuthConfigured()},
	})
}

// POST /v1/auth/google/invite — an admin mints a link that joins *their*
// workspace. The link is the capability: a stranger cannot name a workspace the
// way a self-service form would let them, because only this endpoint sets
// Workspace, and it only ever sets the caller's own.
func (s *Server) googleInvite(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)
	if !s.cfg.GoogleOAuthConfigured() {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "google sign-in is not configured",
			"hint":  "set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET first",
		})
		return
	}

	nonce, err := randomSecret()
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	token, err := s.signGoogleState(googlePurposeInvite, googleAuthState{
		Workspace: p.OrganizationID, Nonce: nonce,
		Expires: time.Now().Add(googleInviteTTL).Unix(),
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"join_url":   s.cfg.PublicURL + "/v1/auth/google/start?invite=" + url.QueryEscape(token),
		"expires_at": time.Now().Add(googleInviteTTL),
	})
}

// GET /v1/auth/google/start — redirect the browser to Google.
//
// ?invite=<token> (see googleInvite) signs the visitor into that workspace as a
// member; without one they get a workspace of their own.
func (s *Server) googleStart(c *gin.Context) {
	if !s.cfg.GoogleOAuthConfigured() {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "google sign-in is not configured",
			"hint":  "set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET on marble_jar_core",
		})
		return
	}

	state := googleAuthState{
		Expires: time.Now().Add(googleSignInTTL).Unix(),
	}
	if raw := c.Query("invite"); raw != "" {
		invite, err := s.openGoogleState(googlePurposeInvite, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "that invite link is no longer valid"})
			return
		}
		state.Workspace, state.Invited = invite.Workspace, true
	}

	nonce, err := randomSecret()
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	// PKCE binds the authorization code to this exact flow instance, so a code
	// that leaked in transit cannot be redeemed elsewhere.
	verifier, err := randomSecret()
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	state.Nonce, state.Verifier = nonce, verifier

	signed, err := s.signGoogleState(googlePurposeSignIn, state)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	// Nothing here is cacheable: the response carries a state and a cookie.
	c.Header("Cache-Control", "no-store")
	http.SetCookie(c.Writer, s.googleStateCookie(signed))
	c.Redirect(http.StatusFound, google.AuthorizeURL+"?"+url.Values{
		"client_id":             {s.cfg.GoogleClientID},
		"redirect_uri":          {s.googleRedirectURI()},
		"scope":                 {google.OIDCScope},
		"response_type":         {"code"},
		"state":                 {signed},
		"nonce":                 {nonce},
		"prompt":                {"select_account"},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode())
}

// GET /v1/auth/google/callback — Google's redirect target. Unauthenticated by
// design: the signed state token and its cookie are the credentials.
func (s *Server) googleCallback(c *gin.Context) {
	// show is a message meant for the human; anything else is logged and
	// reported vaguely, because Google's error bodies and pgx messages are not
	// for the browser.
	redirectLogin := func(param, value string) {
		http.SetCookie(c.Writer, s.clearGoogleStateCookie())
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound,
			s.cfg.WebAppURL+"/login?"+param+"="+url.QueryEscape(value))
	}
	show := func(msg string) { redirectLogin("error", msg) }
	giveUp := func(err error) {
		s.log.Warn("google sign-in failed", "error", err.Error())
		show("Google sign-in failed — start again, and ask your workspace admin if it repeats")
	}

	if !s.cfg.GoogleOAuthConfigured() {
		show("google sign-in is not configured on this server")
		return
	}
	if c.Query("error") != "" {
		// Google reports user-facing consent failures in these two params.
		show("google said: " + c.Query("error_description"))
		return
	}

	state, err := s.openGoogleState(googlePurposeSignIn, c.Query("state"))
	if err != nil {
		show("that sign-in attempt expired — start again")
		return
	}
	if cookie, err := c.Cookie(googleStateCookie); err != nil ||
		cookie != c.Query("state") {
		show("state mismatch — start the sign-in again")
		return
	}
	if c.Query("code") == "" {
		show("google returned no authorization code")
		return
	}

	ctx := c.Request.Context()
	idToken, err := s.exchangeGoogleCode(ctx, c.Query("code"), state.Verifier)
	if err != nil {
		giveUp(err)
		return
	}
	identity, err := s.googleVerifier().Verify(ctx, idToken, state.Nonce)
	if err != nil {
		giveUp(err)
		return
	}
	if !s.cfg.AllowsGoogleDomain(identity.Domain()) {
		show("accounts from " + identity.Domain() + " are not allowed here")
		return
	}

	user, err := s.provisionGoogleUser(ctx, state, identity)
	if err != nil {
		giveUp(err)
		return
	}
	if !user.IsActive {
		show("this account has been disabled")
		return
	}

	// The session itself never appears in a URL: the browser redeems this code
	// over POST from the dashboard. A fragment keeps it out of server logs and
	// Referer headers on the way there.
	loginCode, err := s.loginCodes().Issue(user.ID)
	if err != nil {
		giveUp(err)
		return
	}

	if err := s.store.RecordAudit(ctx, store.AuditParams{
		OrganizationID: user.OrganizationID,
		ActingUserID:   &user.ID,
		Provider:       googleProvider,
		Action:         "auth.sign_in",
		Details:        map[string]any{"email": user.Email, "invited": state.Invited},
	}); err != nil {
		s.log.Warn("audit sign-in failed", "error", err)
	}

	// The sign-in state is spent now; drop the cookie so it cannot be reused.
	http.SetCookie(c.Writer, s.clearGoogleStateCookie())
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound,
		s.cfg.WebAppURL+"/login#login_code="+url.QueryEscape(loginCode))
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

	c.Header("Cache-Control", "no-store")
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

// provisionGoogleUser resolves a verified Google identity to a local user: the
// account already linked to it, else a not-yet-linked account with the same
// email, else a freshly provisioned one.
func (s *Server) provisionGoogleUser(ctx context.Context, state *googleAuthState,
	identity *google.Identity) (*store.User, error) {

	if user, err := s.store.GetUserByProviderSubject(ctx, googleProvider, identity.Subject); err == nil {
		// Returning user: refresh the profile Google claims for them.
		return s.store.RefreshProviderProfile(ctx, user.ID, googleProvider,
			identity.Subject, identity.Name, identity.Picture)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// First Google sign-in for an address that already owns an account. This is
	// how a workspace created before Google sign-in keeps its role and history.
	if existing, err := s.store.GetUserByEmail(ctx, identity.Email); err == nil {
		linked, err := s.store.ClaimUserForProvider(ctx, existing.ID, googleProvider,
			identity.Subject, identity.Name, identity.Picture)
		if errors.Is(err, store.ErrNotFound) {
			// Another Google identity already owns that row.
			return nil, fmt.Errorf("email %s is already linked to a different google account", identity.Email)
		}
		return linked, err
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	orgID, role := "", "member"
	switch {
	case state.Invited:
		// Only an admin-minted invite can point this sign-in at a workspace.
		org, err := s.store.GetOrganization(ctx, state.Workspace)
		if err != nil {
			return nil, fmt.Errorf("invite points at a workspace that no longer exists")
		}
		orgID = org.ID
	default:
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

func (s *Server) googleRedirectURI() string {
	return s.cfg.PublicURL + "/v1/auth/google/callback"
}

// googleStatePath honours a base path in PUBLIC_API_BASE_URL, e.g. when the API
// sits behind a proxy at https://host/api.
func (s *Server) googleStatePath() string {
	base := ""
	if u, err := url.Parse(s.cfg.PublicURL); err == nil {
		base = strings.TrimRight(u.Path, "/")
	}
	return base + "/v1/auth/google"
}

func (s *Server) googleStateCookie(value string) *http.Cookie {
	return &http.Cookie{
		Name:     googleStateCookie,
		Value:    value,
		Path:     s.googleStatePath(),
		MaxAge:   int(googleSignInTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicURL, "https://"),
		// Lax keeps the cookie on the top-level GET redirect back from Google.
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) clearGoogleStateCookie() *http.Cookie {
	return &http.Cookie{Name: googleStateCookie, Value: "",
		Path: s.googleStatePath(), MaxAge: -1, HttpOnly: true}
}

// signGoogleState serialises state for one purpose and authenticates it.
func (s *Server) signGoogleState(purpose string, state googleAuthState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.googleStateMAC(purpose, body), nil
}

func (s *Server) openGoogleState(purpose, token string) (*googleAuthState, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed state")
	}
	if !hmac.Equal([]byte(s.googleStateMAC(purpose, parts[0])), []byte(parts[1])) {
		return nil, fmt.Errorf("invalid state signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("malformed state payload")
	}
	var state googleAuthState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("malformed state fields")
	}
	if state.Nonce == "" || state.expired() {
		return nil, fmt.Errorf("state expired")
	}
	return &state, nil
}

func (s *Server) googleStateMAC(purpose, body string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.HMACDispatchSecret))
	mac.Write([]byte(purpose + "." + body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// exchangeGoogleCode swaps the authorization code for tokens and returns the ID
// token — the only one we need, since this is identity and not API access.
func (s *Server) exchangeGoogleCode(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {s.cfg.GoogleClientID},
		"client_secret": {s.cfg.GoogleClientSecret},
		"redirect_uri":  {s.googleRedirectURI()},
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
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

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// googleVerifier and loginCodes are built on first use so a Server assembled by
// hand (tests) still works without a constructor.
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
