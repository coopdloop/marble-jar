// Package google verifies Google OpenID Connect ID tokens, which is how the
// dashboard signs humans in without ever handling a password.
package google

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Endpoints from Google's OpenID discovery document
// (https://accounts.google.com/.well-known/openid-configuration).
const (
	AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL     = "https://oauth2.googleapis.com/token"
	JWKSURL      = "https://www.googleapis.com/oauth2/v3/certs"
	Issuer       = "accounts.google.com"
	// Google has issued both spellings over time; accept either.
	IssuerAlt    = "https://accounts.google.com"
	OIDCScope    = "openid email profile"
	cacheTTL     = time.Hour
	maxJWKSBytes = 1 << 16
)

// Identity is the verified subset of the Google ID token we act on.
type Identity struct {
	Subject       string // stable Google account id
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	HostedDomain  string // Workspace domain, empty for consumer @gmail.com
}

// Domain returns the email domain, which the allowlist is matched against.
func (i *Identity) Domain() string {
	if i.HostedDomain != "" {
		return strings.ToLower(i.HostedDomain)
	}
	if at := strings.LastIndex(i.Email, "@"); at >= 0 {
		return strings.ToLower(i.Email[at+1:])
	}
	return ""
}

type idClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	HostedDomain  string `json:"hd"`
	jwt.RegisteredClaims
}

// Verifier checks ID token signatures against Google's JWKS, caching the keys.
// A zero value is not usable; call NewVerifier.
type Verifier struct {
	clientID string
	http     *http.Client
	jwksURL  string

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func NewVerifier(clientID string) *Verifier {
	return &Verifier{
		clientID: clientID,
		http:     &http.Client{Timeout: 10 * time.Second},
		jwksURL:  JWKSURL,
		keys:     map[string]*rsa.PublicKey{},
	}
}

// Verify returns the identity only if the token is a current, correctly issued
// and correctly audenced Google ID token for this client.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Identity, error) {
	kid, err := tokenKeyID(rawToken)
	if err != nil {
		return nil, err
	}

	key, err := v.publicKey(ctx, kid)
	if err != nil {
		return nil, fmt.Errorf("google id_token: %w", err)
	}

	claims := &idClaims{}
	token, err := jwt.NewParser(jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithAudience(v.clientID), jwt.WithExpirationRequired()).
		ParseWithClaims(rawToken, claims, func(*jwt.Token) (any, error) {
			return key, nil
		})
	if err != nil {
		return nil, fmt.Errorf("google id_token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("google id_token: invalid token")
	}
	if claims.Issuer != Issuer && claims.Issuer != IssuerAlt {
		return nil, fmt.Errorf("google id_token: unexpected issuer %q", claims.Issuer)
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("google id_token: missing sub")
	}
	// Google always sends email/email_verified for the openid+email scopes,
	// but a missing claim must not be read as "verified".
	if claims.Email == "" || !claims.EmailVerified {
		return nil, fmt.Errorf("google id_token: email not verified")
	}

	return &Identity{
		Subject: claims.Subject, Email: claims.Email, EmailVerified: true,
		Name: claims.Name, Picture: claims.Picture, HostedDomain: claims.HostedDomain,
	}, nil
}

func tokenKeyID(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("google id_token: malformed")
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	rawJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("google id_token: bad header: %w", err)
	}
	if err := json.Unmarshal(rawJSON, &header); err != nil {
		return "", fmt.Errorf("google id_token: bad header: %w", err)
	}
	if header.KID == "" {
		return "", fmt.Errorf("google id_token: missing kid")
	}
	return header.KID, nil
}

// publicKey serves a cached key, refetching JWKS when the kid is unknown or the
// cache has aged out. Google rotates keys without notice, so an unknown kid is
// always a refresh rather than an error.
func (v *Verifier) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if key, ok := v.keys[kid]; ok && time.Since(v.fetched) < cacheTTL {
		return key, nil
	}
	if err := v.refreshLocked(ctx); err != nil {
		return nil, err
	}
	key, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("no Google key with kid %q", kid)
	}
	return key, nil
}

func (v *Verifier) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return fmt.Errorf("fetch jwks: read: %w", err)
	}
	var set struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("fetch jwks: decode: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.KTY != "RSA" || jwk.KID == "" {
			continue
		}
		key, err := rsaPublicKey(jwk.N, jwk.E)
		if err != nil {
			continue // skip a key we cannot parse, keep the rest usable
		}
		keys[jwk.KID] = key
	}
	if len(keys) == 0 {
		return fmt.Errorf("fetch jwks: no usable RSA keys")
	}
	v.keys = keys
	v.fetched = time.Now()
	return nil
}

func rsaPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() > math.MaxInt32 {
		return nil, fmt.Errorf("unsupported RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}
