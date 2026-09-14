package config

import "testing"

func TestAllowsGoogleDomain(t *testing.T) {
	open := &Config{}
	if !open.AllowsGoogleDomain("gmail.com") {
		t.Fatal("an unset allowlist must accept every verified domain")
	}

	restricted := &Config{GoogleAllowedDomains: []string{"Acme.COM"}}
	if !restricted.AllowsGoogleDomain("acme.com") {
		t.Fatal("expected the allowlist to be case-insensitive")
	}
	if restricted.AllowsGoogleDomain("gmail.com") {
		t.Fatal("expected gmail.com to be rejected by the allowlist")
	}
	if restricted.AllowsGoogleDomain("") {
		t.Fatal("expected a missing domain to be rejected")
	}
}

func TestGoogleOAuthConfigured(t *testing.T) {
	if (&Config{GoogleClientID: "id"}).GoogleOAuthConfigured() {
		t.Fatal("a client id without a secret cannot complete the code exchange")
	}
	if !(&Config{GoogleClientID: "id", GoogleClientSecret: "secret"}).GoogleOAuthConfigured() {
		t.Fatal("expected both credentials to enable google sign-in")
	}
}
