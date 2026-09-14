package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/config"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func googleTestServer() *Server {
	return &Server{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg: &config.Config{
			GoogleClientID:     "client-id",
			GoogleClientSecret: "client-secret",
			HMACDispatchSecret: "test-secret",
			PublicURL:          "http://api.test",
			WebAppURL:          "http://app.test",
		},
	}
}

func doRequest(r http.Handler, req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Result()
}

func TestGoogleStartRedirectsToGoogle(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.GET("/v1/auth/google/start", s.googleStart)

	res := doRequest(r, httptest.NewRequest(http.MethodGet, "/v1/auth/google/start", nil))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status: got %d", res.StatusCode)
	}

	loc := res.Header.Get("Location")
	for _, want := range []string{
		"https://accounts.google.com/o/oauth2/v2/auth",
		"client_id=client-id",
		"redirect_uri=http%3A%2F%2Fapi.test%2Fv1%2Fauth%2Fgoogle%2Fcallback",
		"scope=openid+email+profile",
		"response_type=code",
		"code_challenge_method=S256",
	} {
		if !strings.Contains(loc, want) {
			t.Fatalf("authorize url missing %q:\n%s", want, loc)
		}
	}

	// The state must be bound to the browser as well as the URL.
	setCookie := res.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, googleStateCookie+"=") ||
		!strings.Contains(setCookie, "HttpOnly") {
		t.Fatalf("expected a bound state cookie, got %q", setCookie)
	}

	state := queryOf(t, loc, "state")
	// PKCE and the ID-token nonce both come from the same flow instance.
	if got := queryOf(t, loc, "nonce"); got == "" {
		t.Fatal("expected a nonce on the authorize request")
	}
	opened, err := s.openGoogleState(googlePurposeSignIn, state)
	if err != nil {
		t.Fatalf("open own state: %v", err)
	}
	if opened.Nonce == "" || opened.Verifier == "" {
		t.Fatalf("state must carry the nonce and pkce verifier: %+v", opened)
	}
	if want := pkceChallenge(opened.Verifier); queryOf(t, loc, "code_challenge") != want {
		t.Fatalf("code_challenge mismatch: state says %q, url says %q",
			want, queryOf(t, loc, "code_challenge"))
	}
}

// A visitor must not get to pick which workspace a sign-in provisions them into.
func TestGoogleStartIgnoresSelfServiceWorkspace(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.GET("/v1/auth/google/start", s.googleStart)

	res := doRequest(r, httptest.NewRequest(http.MethodGet,
		"/v1/auth/google/start?workspace=00000000-0000-0000-0000-000000000000", nil))
	state, err := s.openGoogleState(googlePurposeSignIn,
		queryOf(t, res.Header.Get("Location"), "state"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if state.Invited || state.Workspace != "" {
		t.Fatalf("a query parameter must not choose the workspace: %+v", state)
	}
}

func TestGoogleStartHonoursAdminInvite(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.GET("/v1/auth/google/start", s.googleStart)

	orgID := "00000000-0000-0000-0000-000000000000"
	invite, err := s.signGoogleState(googlePurposeInvite, googleAuthState{
		Workspace: orgID, Nonce: "invite-nonce",
		Expires: time.Now().Add(googleInviteTTL).Unix(),
	})
	if err != nil {
		t.Fatalf("sign invite: %v", err)
	}

	res := doRequest(r, httptest.NewRequest(http.MethodGet,
		"/v1/auth/google/start?invite="+url.QueryEscape(invite), nil))
	state, err := s.openGoogleState(googlePurposeSignIn,
		queryOf(t, res.Header.Get("Location"), "state"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if !state.Invited || state.Workspace != orgID {
		t.Fatalf("invite was not carried through: %+v", state)
	}
}

// An invite is not a sign-in state, and vice versa: the purpose tag separates them.
func TestGoogleStatePurposeSeparation(t *testing.T) {
	s := googleTestServer()
	invite, err := s.signGoogleState(googlePurposeInvite, googleAuthState{
		Workspace: "org-1", Nonce: "n", Expires: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := s.openGoogleState(googlePurposeSignIn, invite); err == nil {
		t.Fatal("expected an invite token to be rejected as a sign-in state")
	}

	session, err := s.signGoogleState(googlePurposeSignIn, googleAuthState{
		Nonce: "n", Expires: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := s.openGoogleState(googlePurposeInvite, session); err == nil {
		t.Fatal("expected a sign-in state to be rejected as an invite")
	}
}

func TestGoogleInviteRequiresAdmin(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.POST("/v1/auth/google/invite", s.googleInvite)

	res := doRequest(r, httptest.NewRequest(http.MethodPost, "/v1/auth/google/invite", nil))
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", res.StatusCode)
	}
}

func TestGoogleStateCookieHonoursBasePath(t *testing.T) {
	s := googleTestServer()
	s.cfg.PublicURL = "https://host.example/api"

	cookie := s.googleStateCookie("value")
	if cookie.Path != "/api/v1/auth/google" {
		t.Fatalf("cookie path ignored the base path: %q", cookie.Path)
	}
	if !cookie.Secure {
		t.Fatal("expected the state cookie to be Secure on https")
	}
	if got := s.googleRedirectURI(); got != "https://host.example/api/v1/auth/google/callback" {
		t.Fatalf("redirect uri: %q", got)
	}
}

func TestGoogleStartUnconfigured(t *testing.T) {
	s := googleTestServer()
	s.cfg.GoogleClientSecret = ""
	r := gin.New()
	r.GET("/v1/auth/google/start", s.googleStart)

	res := doRequest(r, httptest.NewRequest(http.MethodGet, "/v1/auth/google/start", nil))
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status: got %d, want 501", res.StatusCode)
	}
}

// The callback must reject anything that did not start in this browser.
func TestGoogleCallbackRejectsUnboundState(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.GET("/v1/auth/google/start", s.googleStart)
	r.GET("/v1/auth/google/callback", s.googleCallback)

	started := doRequest(r, httptest.NewRequest(http.MethodGet, "/v1/auth/google/start", nil))
	state := queryOf(t, started.Header.Get("Location"), "state")

	// Same state, no cookie: cannot belong to this browser.
	req := httptest.NewRequest(http.MethodGet,
		"/v1/auth/google/callback?state="+state+"&code=abc", nil)
	res := doRequest(r, req)
	assertLoginError(t, res, "state mismatch")

	// Right cookie, forged state: signature check has to catch it.
	req = httptest.NewRequest(http.MethodGet,
		"/v1/auth/google/callback?state="+state+"x&code=abc", nil)
	req.AddCookie(&http.Cookie{Name: googleStateCookie, Value: state})
	res = doRequest(r, req)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status: got %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Location"), "error=") {
		t.Fatalf("expected a signed-state failure, got %s", res.Header.Get("Location"))
	}
}

func TestGoogleCallbackProviderDenied(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.GET("/v1/auth/google/callback", s.googleCallback)

	res := doRequest(r, httptest.NewRequest(http.MethodGet,
		"/v1/auth/google/callback?error=access_denied&error_description=nope", nil))
	assertLoginError(t, res, "google said")
}

func TestExchangeLoginCodeRejectsUnknown(t *testing.T) {
	s := googleTestServer()
	r := gin.New()
	r.POST("/v1/auth/exchange", s.exchangeLoginCode)

	res := doRequest(r, httptest.NewRequest(http.MethodPost, "/v1/auth/exchange",
		strings.NewReader(`{"login_code":"made-up"}`)))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", res.StatusCode)
	}
}

func TestLoginCodeRoundTripRedeemsOnce(t *testing.T) {
	s := googleTestServer()
	code, err := s.loginCodes().Issue("user-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if got, ok := s.loginCodes().Consume(code); !ok || got != "user-1" {
		t.Fatalf("consume: got (%q,%v)", got, ok)
	}
	if _, ok := s.loginCodes().Consume(code); ok {
		t.Fatal("expected the code to be spent")
	}
}

func assertLoginError(t *testing.T, res *http.Response, want string) {
	t.Helper()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status: got %d, want 302", res.StatusCode)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if loc.Path != "/login" {
		t.Fatalf("expected a redirect to the login page, got %q", res.Header.Get("Location"))
	}
	msg := strings.ToLower(loc.Query().Get("error"))
	if msg == "" {
		t.Fatalf("expected an error parameter, got %q", res.Header.Get("Location"))
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("expected %q in %q", want, msg)
	}
}

func queryOf(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	value := u.Query().Get(key)
	if value == "" {
		t.Fatalf("no %s in %q", key, raw)
	}
	return value
}
