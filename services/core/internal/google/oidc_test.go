package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testClientID = "test-client.apps.googleusercontent.com"

type fixture struct {
	verifier  *Verifier
	keyID     string
	private   *rsa.PrivateKey
	jwksCalls atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	f := &fixture{keyID: "kid-1", private: key}
	pub := &key.PublicKey
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.jwksCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": f.keyID, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)

	v := NewVerifier(testClientID)
	v.jwksURL = srv.URL
	f.verifier = v
	return f
}

func (f *fixture) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	tok.Header["kid"] = f.keyID
	signed, err := tok.SignedString(f.private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": Issuer, "aud": testClientID, "sub": "1087171234567890",
		"email": "dev@marblejar.io", "email_verified": true,
		"name": "Dev", "picture": "https://example.com/a.png",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
	}
}

func TestVerifyAcceptsGoodToken(t *testing.T) {
	f := newFixture(t)
	id, err := f.verifier.Verify(context.Background(), f.token(t, validClaims()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Email != "dev@marblejar.io" || id.Subject != "1087171234567890" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.Domain() != "marblejar.io" {
		t.Fatalf("unexpected domain: %q", id.Domain())
	}
}

func TestVerifyRejects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cases := map[string]func(map[string]any){
		"wrong audience":   func(c map[string]any) { c["aud"] = "someone-else" },
		"wrong issuer":     func(c map[string]any) { c["iss"] = "https://evil.example" },
		"unverified email": func(c map[string]any) { c["email_verified"] = false },
		"missing email":    func(c map[string]any) { delete(c, "email") },
		"expired":          func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
	}
	for name, mutate := range cases {
		claims := validClaims()
		mutate(claims)
		if _, err := f.verifier.Verify(ctx, f.token(t, claims)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestDomainPrefersHostedDomain(t *testing.T) {
	f := newFixture(t)
	claims := validClaims()
	claims["hd"] = "Acme.COM"
	id, err := f.verifier.Verify(context.Background(), f.token(t, claims))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Domain() != "acme.com" {
		t.Fatalf("got domain %q", id.Domain())
	}
}

func TestVerifyRejectsUnknownKey(t *testing.T) {
	f := newFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(validClaims()))
	tok.Header["kid"] = "rotated-away"
	raw, err := tok.SignedString(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected a token signed by an unknown key to be rejected")
	}
}

func TestVerifyRejectsAlgConfusion(t *testing.T) {
	f := newFixture(t)
	mac := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(validClaims()))
	raw, err := mac.SignedString([]byte("guess-me"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected an HS256 token to be rejected")
	}
}

func TestVerifyFetchesJWKSOnce(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		if _, err := f.verifier.Verify(context.Background(), f.token(t, validClaims())); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if got := f.jwksCalls.Load(); got != 1 {
		t.Fatalf("expected the cached keys to be reused, got %d jwks fetches", got)
	}
}
