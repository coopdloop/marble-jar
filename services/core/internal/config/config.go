package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config carries every knob marble_jar_core and the dispatch worker need.
// Field names mirror the environment variable contract in the product spec.
type Config struct {
	Port               string
	DatabaseURL        string
	RedisURL           string
	RedpandaBrokers    []string
	JWTSigningKey      string
	HMACDispatchSecret string
	PhoenixBaseURL     string
	OAuthIssuerURL     string
	OAuthClientID      string
	OAuthClientSecret  string
	// Google sign-in for the dashboard (OpenID Connect authorization code).
	// These are the *client* credentials from Google Cloud, not the OBO issuer.
	GoogleClientID     string
	GoogleClientSecret string
	// GoogleAllowedDomains restricts sign-in to these Workspace domains.
	// Empty means any Google account with a verified email may sign in.
	GoogleAllowedDomains []string
	// Per-provider OAuth apps used by the integration connect flow.
	SlackClientID         string
	SlackClientSecret     string
	AtlassianClientID     string
	AtlassianClientSecret string
	EntraTenantID         string
	EntraClientID         string
	EntraClientSecret     string
	// PublicURL is where browsers can reach this API (OAuth redirect_uri base).
	PublicURL string
	// WebAppURL is where the OAuth callback redirects the user afterwards.
	WebAppURL       string
	LogLevel        string
	CORSOrigins     []string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	DispatchWorkers int
	// DevMode relaxes secret-strength checks and enables the in-memory event bus
	// when Redpanda/Redis are not configured.
	DevMode bool
}

func Load() (*Config, error) {
	c := &Config{
		Port:                  env("PORT", "8080"),
		PublicURL:             strings.TrimRight(env("PUBLIC_API_BASE_URL", ""), "/"),
		WebAppURL:             strings.TrimRight(env("WEB_APP_URL", ""), "/"),
		DatabaseURL:           env("DATABASE_URL", env("POSTGRES_DSN", "")),
		RedisURL:              env("REDIS_URL", ""),
		RedpandaBrokers:       splitList(env("REDPANDA_BROKERS", "")),
		JWTSigningKey:         env("JWT_SIGNING_KEY", ""),
		HMACDispatchSecret:    env("HMAC_DISPATCH_SECRET", ""),
		PhoenixBaseURL:        strings.TrimRight(env("PHOENIX_BASE_URL", ""), "/"),
		OAuthIssuerURL:        env("OAUTH_ISSUER_URL", env("HYDRA_ISSUER_URL", "")),
		OAuthClientID:         env("OAUTH_CLIENT_ID", env("HYDRA_CLIENT_ID", "")),
		OAuthClientSecret:     env("OAUTH_CLIENT_SECRET", env("HYDRA_CLIENT_SECRET", "")),
		GoogleClientID:        env("GOOGLE_OAUTH_CLIENT_ID", ""),
		GoogleClientSecret:    env("GOOGLE_OAUTH_CLIENT_SECRET", ""),
		GoogleAllowedDomains:  splitList(env("GOOGLE_OAUTH_ALLOWED_DOMAINS", "")),
		SlackClientID:         env("SLACK_OAUTH_CLIENT_ID", ""),
		SlackClientSecret:     env("SLACK_OAUTH_CLIENT_SECRET", ""),
		AtlassianClientID:     env("ATLASSIAN_OAUTH_CLIENT_ID", ""),
		AtlassianClientSecret: env("ATLASSIAN_OAUTH_CLIENT_SECRET", ""),
		EntraTenantID:         env("ENTRA_ID_TENANT_ID", ""),
		EntraClientID:         env("ENTRA_ID_CLIENT_ID", ""),
		EntraClientSecret:     env("ENTRA_ID_CLIENT_SECRET", ""),
		LogLevel:              env("LOG_LEVEL", "info"),
		CORSOrigins:           splitList(env("CORS_ORIGINS", "http://localhost:5173")),
		AccessTokenTTL:        envDuration("ACCESS_TOKEN_TTL", time.Hour),
		RefreshTokenTTL:       envDuration("REFRESH_TOKEN_TTL", 720*time.Hour),
		DispatchWorkers:       envInt("DISPATCH_WORKER_CONCURRENCY", 4),
		DevMode:               envBool("DEV_MODE", false),
	}

	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:" + c.Port
	}
	if c.WebAppURL == "" {
		c.WebAppURL = "http://localhost:5173"
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL (or POSTGRES_DSN) is required")
	}
	if c.JWTSigningKey == "" {
		if !c.DevMode {
			return nil, fmt.Errorf("JWT_SIGNING_KEY is required")
		}
		c.JWTSigningKey = "dev-insecure-signing-key-change-me"
	}
	if !c.DevMode && len(c.JWTSigningKey) < 32 {
		return nil, fmt.Errorf("JWT_SIGNING_KEY must be at least 32 bytes")
	}
	if c.HMACDispatchSecret == "" {
		if !c.DevMode {
			return nil, fmt.Errorf("HMAC_DISPATCH_SECRET is required")
		}
		c.HMACDispatchSecret = "dev-insecure-dispatch-secret"
	}
	return c, nil
}

// GoogleOAuthConfigured reports whether the dashboard can offer Google sign-in.
func (c *Config) GoogleOAuthConfigured() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// AllowsGoogleDomain applies the optional Workspace-domain allowlist.
func (c *Config) AllowsGoogleDomain(domain string) bool {
	if len(c.GoogleAllowedDomains) == 0 {
		return true
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, allowed := range c.GoogleAllowedDomains {
		if strings.ToLower(strings.TrimSpace(allowed)) == domain {
			return true
		}
	}
	return false
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitList(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil && v > 0 {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil && v > 0 {
		return v
	}
	return fallback
}
