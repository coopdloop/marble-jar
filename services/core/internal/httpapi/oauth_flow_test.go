package httpapi

import (
	"testing"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/config"
)

func testServer() *Server {
	return &Server{cfg: &config.Config{HMACDispatchSecret: "test-secret"}}
}

func TestStateRoundTrip(t *testing.T) {
	s := testServer()
	token, err := s.signState(oauthState{OrgID: "org-1", UserID: "user-1", Provider: "slack"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	st, err := s.verifyState(token, "slack")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if st.OrgID != "org-1" || st.UserID != "user-1" || st.Provider != "slack" {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestStateRejectsTampering(t *testing.T) {
	s := testServer()
	token, err := s.signState(oauthState{OrgID: "org-1", UserID: "user-1", Provider: "slack"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := s.verifyState(token, "jira"); err == nil {
		t.Fatal("expected provider mismatch to be rejected")
	}
	if _, err := s.verifyState(token+"x", "slack"); err == nil {
		t.Fatal("expected tampered signature to be rejected")
	}
	other := &Server{cfg: &config.Config{HMACDispatchSecret: "different-secret"}}
	if _, err := other.verifyState(token, "slack"); err == nil {
		t.Fatal("expected wrong secret to be rejected")
	}
}

func TestStateRejectsExpiry(t *testing.T) {
	s := testServer()
	token, err := s.signState(oauthState{
		OrgID: "org-1", UserID: "user-1", Provider: "slack",
		Expiry: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := s.verifyState(token, "slack"); err == nil {
		t.Fatal("expected expired state to be rejected")
	}
}

func TestParseSlackToken(t *testing.T) {
	raw := []byte(`{
		"ok": true,
		"authed_user": {"id": "U123", "access_token": "xoxp-abc", "scope": "chat:write"},
		"team": {"id": "T1", "name": "Acme"}
	}`)
	tok, err := parseSlackToken(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tok.AccessToken != "xoxp-abc" {
		t.Fatalf("token: %q", tok.AccessToken)
	}
	if tok.AccountID != "Acme (T1/U123)" {
		t.Fatalf("account: %q", tok.AccountID)
	}
	if len(tok.Scopes) != 1 || tok.Scopes[0] != "chat:write" {
		t.Fatalf("scopes: %v", tok.Scopes)
	}
}

func TestParseSlackTokenError(t *testing.T) {
	if _, err := parseSlackToken([]byte(`{"ok": false, "error": "invalid_code"}`)); err == nil {
		t.Fatal("expected slack error to surface")
	}
}
