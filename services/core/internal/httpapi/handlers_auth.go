package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

type registerBody struct {
	Email            string `json:"email" binding:"required,email"`
	Password         string `json:"password" binding:"required,min=12"`
	DisplayName      string `json:"display_name"`
	OrganizationName string `json:"organization_name"`
	// OrganizationID joins an existing workspace instead of creating one.
	OrganizationID string `json:"organization_id"`
}

// POST /v1/register
func (s *Server) register(c *gin.Context) {
	var body registerBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	if _, err := s.store.GetUserByEmail(ctx, body.Email); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		respondStoreErr(c, err)
		return
	}

	orgID := body.OrganizationID
	role := "member"
	if orgID == "" {
		name := body.OrganizationName
		if name == "" {
			name = strings.SplitN(body.Email, "@", 2)[0] + "'s jar"
		}
		org, err := s.store.CreateOrganization(ctx, name, orgSlug(name))
		if err != nil {
			respondStoreErr(c, err)
			return
		}
		orgID = org.ID
		role = "owner" // first user in a new workspace owns it
	} else if _, err := s.store.GetOrganization(ctx, orgID); err != nil {
		respondStoreErr(c, err)
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	u, err := s.store.CreateUser(ctx, orgID, body.Email, body.DisplayName, role, hash)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	tokens, err := s.auth.IssueTokens(u.ID, u.OrganizationID, u.Email, u.Role, nil)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"user": u, "tokens": tokens})
}

type loginBody struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// POST /v1/login
func (s *Server) login(c *gin.Context) {
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	u, err := s.store.GetUserByEmail(c.Request.Context(), body.Email)
	// Uniform error text so the endpoint does not leak account existence.
	if err != nil || u.PasswordHash == nil ||
		!auth.CheckPassword(*u.PasswordHash, body.Password) || !u.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	tokens, err := s.auth.IssueTokens(u.ID, u.OrganizationID, u.Email, u.Role, nil)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	org, err := s.store.GetOrganization(c.Request.Context(), u.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u, "organization": org, "tokens": tokens})
}

type refreshBody struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// POST /v1/token/refresh
func (s *Server) refreshToken(c *gin.Context) {
	var body refreshBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := s.auth.Verify(body.RefreshToken, "refresh")
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	u, err := s.store.GetUser(c.Request.Context(), claims.Subject)
	if err != nil || !u.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found or inactive"})
		return
	}

	tokens, err := s.auth.IssueTokens(u.ID, u.OrganizationID, u.Email, u.Role, nil)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, tokens)
}

// POST /v1/token/introspect
func (s *Server) introspect(c *gin.Context) {
	p := mustPrincipal(c)
	c.JSON(http.StatusOK, gin.H{
		"active":          true,
		"kind":            p.Kind,
		"organization_id": p.OrganizationID,
		"user_id":         p.UserID,
		"api_key_id":      p.APIKeyID,
		"scopes":          p.Scopes,
		"role":            p.Role,
	})
}

// GET /v1/me
func (s *Server) me(c *gin.Context) {
	p := mustPrincipal(c)

	org, err := s.store.GetOrganization(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	resp := gin.H{"organization": org, "principal": gin.H{
		"kind": p.Kind, "role": p.Role, "scopes": p.Scopes,
		"agent_identity": p.AgentIdentity,
	}}
	if p.UserID != "" {
		if u, err := s.store.GetUser(c.Request.Context(), p.UserID); err == nil {
			resp["user"] = u
		}
	}
	c.JSON(http.StatusOK, resp)
}

// ---- API keys ----

// GET /v1/api-keys
func (s *Server) listAPIKeys(c *gin.Context) {
	p := mustPrincipal(c)
	keys, err := s.store.ListAPIKeys(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": keys})
}

type createAPIKeyBody struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes"`
}

// Default scopes let an agent log marbles and read jar status, nothing more.
var defaultAPIKeyScopes = []string{"marbles:write", "marbles:read", "jar:read"}

// POST /v1/api-keys
func (s *Server) createAPIKey(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)

	var body createAPIKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	scopes := body.Scopes
	if len(scopes) == 0 {
		scopes = defaultAPIKeyScopes
	}

	full, prefix, hashed, err := auth.GenerateAPIKey()
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	k, err := s.store.CreateAPIKey(c.Request.Context(), p.OrganizationID,
		p.ActingUserID(), body.Name, prefix, hashed, scopes)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	// The plaintext key is returned exactly once.
	c.JSON(http.StatusCreated, gin.H{"api_key": k, "key": full})
}

// DELETE /v1/api-keys/:key_id
func (s *Server) revokeAPIKey(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)
	if err := s.store.RevokeAPIKey(c.Request.Context(), p.OrganizationID, c.Param("key_id")); err != nil {
		respondStoreErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- integrations (OBO connections) ----

var supportedProviders = map[string]bool{
	"jira": true, "slack": true, "teams": true,
}

// GET /v1/integrations
func (s *Server) listIntegrations(c *gin.Context) {
	p := mustPrincipal(c)
	if p.UserID == "" {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
		return
	}

	conns, err := s.store.ListOAuthConnections(c.Request.Context(), p.OrganizationID, p.UserID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	byProvider := map[string]*store.OAuthConnection{}
	for i := range conns {
		byProvider[conns[i].Provider] = &conns[i]
	}

	items := []gin.H{}
	for provider := range supportedProviders {
		item := gin.H{"provider": provider, "connected": false}
		if conn, ok := byProvider[provider]; ok {
			item["connected"] = true
			item["scopes"] = conn.Scopes
			item["expires_at"] = conn.ExpiresAt
			item["connected_at"] = conn.CreatedAt
			item["account_id"] = conn.ProviderAccountID
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GET /v1/integrations/:integration/connection
func (s *Server) getIntegrationConnection(c *gin.Context) {
	p := mustPrincipal(c)
	provider := c.Param("integration")
	if !supportedProviders[provider] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported integration"})
		return
	}

	conn, err := s.store.GetOAuthConnection(c.Request.Context(), p.OrganizationID, p.UserID, provider)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusOK, gin.H{"provider": provider, "connected": false})
		return
	}
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"provider": provider, "connected": true,
		"scopes": conn.Scopes, "expires_at": conn.ExpiresAt,
		"account_id": conn.ProviderAccountID, "connected_at": conn.CreatedAt,
	})
}

// POST /v1/integrations/:integration/connect
//
// Returns the provider's own authorize URL (Slack/Atlassian/Entra), with the
// signed state token carrying the connecting user's identity through the
// redirect. Without the provider app credentials this reports what is missing
// rather than pretending the connection succeeded.
func (s *Server) connectIntegration(c *gin.Context) {
	p := mustPrincipal(c)
	provider := c.Param("integration")
	if !supportedProviders[provider] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported integration"})
		return
	}

	app, err := s.appFor(provider)
	if err != nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":    "integration not configured",
			"provider": provider,
			"hint":     err.Error(),
		})
		return
	}

	state, err := s.signState(oauthState{
		OrgID: p.OrganizationID, UserID: p.UserID, Provider: provider,
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	if err := s.store.RecordAudit(c.Request.Context(), store.AuditParams{
		OrganizationID: p.OrganizationID,
		ActingUserID:   p.ActingUserID(),
		Provider:       provider,
		Action:         "integration.connect_initiated",
	}); err != nil {
		s.log.Warn("audit connect failed", "error", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"provider":      provider,
		"authorize_url": s.authorizeURL(provider, app, state),
		"state":         state,
		"scopes":        app.scopes,
	})
}

// DELETE /v1/integrations/:integration/connection
func (s *Server) disconnectIntegration(c *gin.Context) {
	p := mustPrincipal(c)
	provider := c.Param("integration")

	if err := s.store.DeleteOAuthConnection(c.Request.Context(),
		p.OrganizationID, p.UserID, provider); err != nil {
		respondStoreErr(c, err)
		return
	}

	if err := s.store.RecordAudit(c.Request.Context(), store.AuditParams{
		OrganizationID: p.OrganizationID,
		ActingUserID:   p.ActingUserID(),
		Provider:       provider,
		Action:         "integration.disconnected",
	}); err != nil {
		s.log.Warn("audit disconnect failed", "error", err)
	}
	c.Status(http.StatusNoContent)
}

func providerScopes(provider string) []string {
	switch provider {
	case "jira":
		return []string{"read:jira-work", "write:jira-work", "offline_access"}
	case "slack":
		return []string{"chat:write", "channels:read"}
	case "teams":
		return []string{"ChannelMessage.Send", "offline_access"}
	}
	return nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func orgSlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "workspace"
	}
	// Suffix keeps slugs unique without a retry loop.
	return out + "-" + time.Now().UTC().Format("20060102150405")
}
