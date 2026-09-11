package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

const principalKey = "marblejar.principal"

// authenticate accepts either a session JWT (dashboard) or an `mj_` API key
// (SDK/MCP/webhook) and attaches the resolved Principal to the request.
func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)
		if token == "" {
			abortErr(c, http.StatusUnauthorized, "missing credentials")
			return
		}

		var p *auth.Principal
		var err error
		if auth.IsAPIKey(token) {
			p, err = s.principalFromAPIKey(c, token)
		} else {
			p, err = s.principalFromJWT(token)
		}
		if err != nil {
			abortErr(c, http.StatusUnauthorized, err.Error())
			return
		}

		c.Set(principalKey, p)
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); h != "" {
		if parts := strings.SplitN(h, " ", 2); len(parts) == 2 &&
			strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	if k := c.GetHeader("X-API-Key"); k != "" {
		return strings.TrimSpace(k)
	}
	// Browsers cannot set headers on a WebSocket handshake, so the queue feed
	// also accepts the token as a query parameter.
	if c.FullPath() == "/v1/ws/queue" {
		if t := c.Query("token"); t != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

func (s *Server) principalFromJWT(token string) (*auth.Principal, error) {
	claims, err := s.auth.Verify(token, "access")
	if err != nil {
		if errors.Is(err, auth.ErrExpired) {
			return nil, errors.New("token expired")
		}
		return nil, errors.New("invalid token")
	}
	return &auth.Principal{
		Kind:           auth.KindUser,
		OrganizationID: claims.OrganizationID,
		UserID:         claims.Subject,
		Email:          claims.Email,
		Role:           claims.Role,
		Scopes:         claims.Scopes,
	}, nil
}

func (s *Server) principalFromAPIKey(c *gin.Context, key string) (*auth.Principal, error) {
	candidates, err := s.store.APIKeysByPrefix(c.Request.Context(), auth.APIKeyPrefix(key))
	if err != nil {
		return nil, errors.New("invalid api key")
	}
	for i := range candidates {
		k := candidates[i]
		if !auth.MatchAPIKey(key, k.HashedKey) {
			continue
		}
		go s.store.TouchAPIKey(c.Copy().Request.Context(), k.ID)

		p := &auth.Principal{
			Kind:           auth.KindAPIKey,
			OrganizationID: k.OrganizationID,
			APIKeyID:       k.ID,
			Scopes:         k.Scopes,
			Role:           "agent",
			AgentIdentity:  k.Name,
		}
		// API keys inherit the creating user so dispatches stay OBO-attributed.
		if k.CreatedByUserID != nil {
			p.UserID = *k.CreatedByUserID
		}
		return p, nil
	}
	return nil, errors.New("invalid api key")
}

// mustPrincipal returns the authenticated caller; handlers run behind
// authenticate() so this never fails there.
func mustPrincipal(c *gin.Context) *auth.Principal {
	v, ok := c.Get(principalKey)
	if !ok {
		return &auth.Principal{}
	}
	p, _ := v.(*auth.Principal)
	return p
}

// requireScope enforces a scope on API-key principals; user sessions are
// governed by role instead.
func requireScope(c *gin.Context, scope string) bool {
	p := mustPrincipal(c)
	if p.Kind == auth.KindUser {
		return true
	}
	if p.HasScope(scope) {
		return true
	}
	abortErr(c, http.StatusForbidden, "api key missing required scope: "+scope)
	return false
}

func requireAdmin(c *gin.Context) bool {
	if mustPrincipal(c).IsAdmin() {
		return true
	}
	abortErr(c, http.StatusForbidden, "admin role required")
	return false
}

func abortErr(c *gin.Context, code int, msg string) {
	c.AbortWithStatusJSON(code, gin.H{"error": msg})
}

// respondStoreErr maps store sentinel errors onto HTTP status codes.
func respondStoreErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, store.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
	case errors.Is(err, store.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
