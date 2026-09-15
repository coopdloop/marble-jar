package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newFormResponder(t *testing.T, body string) (srv *httptest.Server, got func() url.Values) {
	t.Helper()
	var captured url.Values
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		captured = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() url.Values { return captured }
}

func TestRefreshSlackRotatesToken(t *testing.T) {
	srv, form := newFormResponder(t, `{"ok":true,"access_token":"xoxb-fresh","refresh_token":"xoxe-fresh","expires_in":1200}`)
	r := &Refresher{BaseURLs: map[string]string{"slack": srv.URL}}

	res, err := r.Refresh(context.Background(), "slack", "cid", "csecret", "xoxe-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.AccessToken != "xoxb-fresh" || res.RefreshToken != "xoxe-fresh" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.ExpiresIn != 20*time.Minute {
		t.Fatalf("expires_in = %v, want 20m", res.ExpiresIn)
	}

	got := form()
	if got.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q", got.Get("grant_type"))
	}
	if got.Get("refresh_token") != "xoxe-old" || got.Get("client_id") != "cid" ||
		got.Get("client_secret") != "csecret" {
		t.Fatalf("unexpected slack form: %v", got)
	}
}

// Slack answers 200 even when the grant is dead, so a status check alone would
// silently hand the executor a bogus token.
func TestRefreshSlackOkFalseIsPermanent(t *testing.T) {
	srv, _ := newFormResponder(t, `{"ok":false,"error":"invalid_grant"}`)
	r := &Refresher{BaseURLs: map[string]string{"slack": srv.URL}}

	_, err := r.Refresh(context.Background(), "slack", "cid", "csecret", "xoxe-stale")
	if err == nil {
		t.Fatal("ok:false must be an error")
	}
	var perm PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("stale grant should be permanent, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("provider error dropped: %v", err)
	}
	if !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("operator needs to know a human must re-authorise: %v", err)
	}
}

func TestRefreshJiraUsesJSONBodyAndBasicAuth(t *testing.T) {
	var body map[string]string
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = io.WriteString(w, `{"access_token":"jira-fresh","expires_in":3600}`)
	}))
	defer srv.Close()

	r := &Refresher{BaseURLs: map[string]string{"jira": srv.URL}}
	res, err := r.Refresh(context.Background(), "jira", "cid", "csecret", "atlas-old")
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "jira-fresh" {
		t.Fatalf("access token = %q", res.AccessToken)
	}
	if body["grant_type"] != "refresh_token" || body["refresh_token"] != "atlas-old" {
		t.Fatalf("unexpected json body: %v", body)
	}
	if body["client_id"] != "" || body["client_secret"] != "" {
		t.Fatalf("atlassian credentials belong in Basic auth, not the body: %v", body)
	}

	encoded, ok := strings.CutPrefix(auth, "Basic ")
	if !ok {
		t.Fatalf("atlassian needs Basic client auth, got %q", auth)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		t.Fatalf("malformed Basic auth: %v", err)
	}
	if string(raw) != "cid:csecret" {
		t.Fatalf("Basic credentials = %q", raw)
	}
}

func TestRefreshTeamsSendsScope(t *testing.T) {
	srv, form := newFormResponder(t, `{"access_token":"graph-fresh","expires_in":4000}`)
	r := &Refresher{BaseURLs: map[string]string{"teams": srv.URL}}

	res, err := r.Refresh(context.Background(), "teams", "cid", "csecret", "graph-old")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpiresIn != 4000*time.Second {
		t.Fatalf("expires = %v", res.ExpiresIn)
	}
	got := form()
	if !strings.Contains(got.Get("scope"), "graph.microsoft.com") ||
		!strings.Contains(got.Get("scope"), "offline_access") {
		t.Fatalf("scope = %q", got.Get("scope"))
	}
}

// The distinction drives the worker's retry loop: a blip is retried, a dead
// grant goes straight to the dead-letter queue.
func TestRefreshRetryability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantReable bool
	}{
		{"server error", http.StatusInternalServerError, `{"error":"server_error"}`, true},
		{"rate limited", http.StatusTooManyRequests, `{"error":"rate_limited"}`, true},
		{"dead grant", http.StatusBadRequest, `{"error":"invalid_grant"}`, false},
		{"bad client", http.StatusUnauthorized, `{"error":"invalid_client"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			r := &Refresher{BaseURLs: map[string]string{"jira": srv.URL}}

			_, err := r.Refresh(context.Background(), "jira", "cid", "csecret", "tok")
			if err == nil {
				t.Fatal("want an error")
			}
			var retry RetryableError
			if got := errors.As(err, &retry); got != tc.wantReable {
				t.Fatalf("retryable = %v, want %v (%v)", got, tc.wantReable, err)
			}
		})
	}
}

// A refresh token is a credential: it must never be echoed back through an
// error string into logs or dispatch history.
func TestRefreshErrorNeverEchoesSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","detail":"rejected refresh=SECRETTOKENHERE secret=CLIENTSECRETHERE"}`)
	}))
	defer srv.Close()

	r := &Refresher{BaseURLs: map[string]string{"slack": srv.URL}}
	_, err := r.Refresh(context.Background(), "slack", "CLIENTSECRETHERE", "CLIENTSECRETHERE", "SECRETTOKENHERE")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if strings.Contains(msg, "SECRETTOKENHERE") || strings.Contains(msg, "CLIENTSECRETHERE") {
		t.Fatalf("secrets leaked into error: %s", msg)
	}
	if !strings.Contains(msg, "invalid_grant") {
		t.Fatalf("useful detail lost: %s", msg)
	}
}

func TestRefreshUnconfiguredInputs(t *testing.T) {
	r := NewRefresher()
	if r.Supported("webhook") || !r.Supported("Slack") || !r.Supported("jira") {
		t.Fatal("Supported() mis-classified providers")
	}
	if _, err := r.Refresh(context.Background(), "webhook", "a", "b", "c"); err == nil {
		t.Fatal("webhook has no credential to refresh")
	}
	// Missing app credentials must not raise "invalid client" on every dispatch.
	for _, args := range [][3]string{{"", "s", "r"}, {"c", "", "r"}, {"c", "s", ""}} {
		_, err := r.Refresh(context.Background(), "slack", args[0], args[1], args[2])
		if err == nil {
			t.Fatalf("want an error for %+v", args)
		}
		var perm PermanentError
		if !errors.As(err, &perm) {
			t.Fatalf("%+v should be permanent, got %T", args, err)
		}
	}
}

// A 200 with no token is not a refresh: storing "" would poison the connection.
func TestRefreshRejectsEmptyToken(t *testing.T) {
	srv, _ := newFormResponder(t, `{"ok":true,"access_token":"","expires_in":60}`)
	r := &Refresher{BaseURLs: map[string]string{"slack": srv.URL}}

	_, err := r.Refresh(context.Background(), "slack", "c", "s", "r")
	var perm PermanentError
	if err == nil || !errors.As(err, &perm) {
		t.Fatalf("empty token should be a permanent failure, got %v", err)
	}
}

// Expired contexts must not hang a dispatch slot for the client timeout.
func TestRefreshHonoursContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := &Refresher{BaseURLs: map[string]string{"jira": srv.URL}}

	start := time.Now()
	if _, err := r.Refresh(ctx, "jira", "c", "s", "r"); err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("ignored cancellation for %v", time.Since(start))
	}
}

func TestSanitizeExcerptClips(t *testing.T) {
	long := strings.Repeat("a", 500)
	if got := sanitizeExcerpt([]byte(long), ""); len(got) > 300 {
		t.Fatalf("excerpt not clipped: %d bytes", len(got))
	}
}
