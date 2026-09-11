// Package auth implements Marble Jar's identity surface: session JWTs for the
// dashboard, hashed API keys for SDK/MCP ingestion, and the principal model
// that downstream OBO dispatch attributes actions to.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpired      = errors.New("token expired")
)

// PrincipalKind distinguishes a human session from an agent API key.
type PrincipalKind string

const (
	KindUser   PrincipalKind = "user"
	KindAPIKey PrincipalKind = "api_key"
)

// Principal is the authenticated caller attached to every request context.
type Principal struct {
	Kind           PrincipalKind
	OrganizationID string
	UserID         string
	APIKeyID       string
	Email          string
	Role           string
	Scopes         []string
	AgentIdentity  string
}

func (p *Principal) IsAdmin() bool {
	return p.Role == "admin" || p.Role == "owner" || p.HasScope("admin")
}

func (p *Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope || s == "*" {
			return true
		}
	}
	return false
}

// ActingUserID returns the user an action is attributed to, if any. API keys
// carry the creating user so OBO dispatch retains a human identity.
func (p *Principal) ActingUserID() *string {
	if p.UserID == "" {
		return nil
	}
	id := p.UserID
	return &id
}

// Manager mints and verifies tokens.
type Manager struct {
	signingKey      []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

func NewManager(signingKey string, accessTTL, refreshTTL time.Duration) *Manager {
	return &Manager{
		signingKey:      []byte(signingKey),
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
	}
}

// Claims is the Marble Jar session JWT body.
type Claims struct {
	jwt.RegisteredClaims
	OrganizationID string   `json:"org"`
	Email          string   `json:"email"`
	Role           string   `json:"role"`
	Scopes         []string `json:"scopes,omitempty"`
	TokenType      string   `json:"typ"`
}

type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (m *Manager) IssueTokens(userID, orgID, email, role string, scopes []string) (*TokenPair, error) {
	access, exp, err := m.sign(userID, orgID, email, role, scopes, "access", m.accessTokenTTL)
	if err != nil {
		return nil, err
	}
	refresh, _, err := m.sign(userID, orgID, email, role, scopes, "refresh", m.refreshTokenTTL)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    exp,
	}, nil
}

func (m *Manager) sign(userID, orgID, email, role string, scopes []string, typ string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "marble-jar",
		},
		OrganizationID: orgID,
		Email:          email,
		Role:           role,
		Scopes:         scopes,
		TokenType:      typ,
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.signingKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

func (m *Manager) Verify(token, expectedType string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.signingKey, nil
	}, jwt.WithIssuer("marble-jar"), jwt.WithLeeway(30*time.Second))

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalidToken
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if expectedType != "" && claims.TokenType != expectedType {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ---------- password hashing ----------

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// ---------- API keys ----------

const APIKeyPrefixLen = 12

// GenerateAPIKey returns (fullKey, prefix, sha256Hash). Only the prefix and
// hash are ever persisted.
func GenerateAPIKey() (full, prefix, hashed string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	body := base64.RawURLEncoding.EncodeToString(buf)
	full = "mj_" + body
	prefix = full[:APIKeyPrefixLen]
	hashed = HashAPIKey(full)
	return full, prefix, hashed, nil
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// MatchAPIKey compares hashes in constant time.
func MatchAPIKey(presented, storedHash string) bool {
	h := HashAPIKey(presented)
	return subtle.ConstantTimeCompare([]byte(h), []byte(storedHash)) == 1
}

func APIKeyPrefix(key string) string {
	if len(key) < APIKeyPrefixLen {
		return key
	}
	return key[:APIKeyPrefixLen]
}

func IsAPIKey(token string) bool {
	return strings.HasPrefix(token, "mj_")
}
