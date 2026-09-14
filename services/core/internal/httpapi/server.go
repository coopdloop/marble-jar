// Package httpapi wires every REST route, the WebSocket upgrade endpoint, and
// the middleware chain for marble_jar_core.
package httpapi

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/auth"
	"github.com/marble-jar/marble-jar/services/core/internal/config"
	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/eventbus"
	"github.com/marble-jar/marble-jar/services/core/internal/google"
	"github.com/marble-jar/marble-jar/services/core/internal/marbles"
	"github.com/marble-jar/marble-jar/services/core/internal/objectives"
	"github.com/marble-jar/marble-jar/services/core/internal/realtime"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
	"github.com/marble-jar/marble-jar/services/core/internal/telemetry"
)

// Server holds every dependency the handlers need.
type Server struct {
	cfg        *config.Config
	store      *store.Store
	auth       *auth.Manager
	hub        *realtime.Hub
	ingest     *marbles.Ingestor
	objectives *objectives.Service
	dispatcher *dispatch.Service
	metrics    *telemetry.Metrics
	log        *slog.Logger

	// Lazy sign-in collaborators (see googleVerifier/loginCodes).
	lazyMu  sync.Mutex
	googleV *google.Verifier
	codes   *auth.LoginCodeStore
}

func NewServer(
	cfg *config.Config,
	st *store.Store,
	am *auth.Manager,
	hub *realtime.Hub,
	ingest *marbles.Ingestor,
	objSvc *objectives.Service,
	disp *dispatch.Service,
	metrics *telemetry.Metrics,
	log *slog.Logger,
) *Server {
	return &Server{
		cfg: cfg, store: st, auth: am, hub: hub, ingest: ingest,
		objectives: objSvc, dispatcher: disp, metrics: metrics, log: log,
	}
}

// Router builds the full route table described in the product spec. The
// ingestion, core, and auth surfaces are mounted on one modular monolith.
func (s *Server) Router() *gin.Engine {
	if s.cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.requestLogger())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     s.cfg.CORSOrigins,
		AllowMethods:     []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-Key", "Idempotency-Key"},
		ExposeHeaders:    []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// ---- unauthenticated ----
	r.GET("/health", s.health)
	r.GET("/metrics", gin.WrapH(s.metrics.Handler()))

	v1 := r.Group("/v1")
	{
		// Google sign-in: browser redirects, so no bearer token is involved.
		v1.GET("/auth/config", s.authConfig)
		v1.GET("/auth/google/start", s.googleStart)
		v1.GET("/auth/google/callback", s.googleCallback)
		// The dashboard redeems the callback's one-time code for a session.
		v1.POST("/auth/exchange", s.exchangeLoginCode)
		v1.POST("/token/refresh", s.refreshToken)
		// OAuth provider redirect target; identity rides in the signed state.
		v1.GET("/integrations/:integration/callback", s.integrationCallback)
	}

	// ---- authenticated ----
	api := r.Group("/v1")
	api.Use(s.authenticate())
	{
		api.GET("/me", s.me)
		api.POST("/token/introspect", s.introspect)
		// Join links are admin-minted: a stranger cannot choose which workspace
		// a sign-in provisions them into.
		api.POST("/auth/google/invite", s.googleInvite)

		// Ingestion surface (SDK / MCP / webhook senders).
		api.POST("/marbles", s.logMarble)
		api.GET("/ws/queue", s.serveQueueWS)

		// Marbles.
		api.GET("/marbles", s.listMarbles)
		api.GET("/marbles/:marble_id", s.getMarble)
		api.PATCH("/marbles/:marble_id", s.updateMarble)
		api.DELETE("/marbles/:marble_id", s.deleteMarble)
		api.POST("/marbles/:marble_id/rollups", s.writeRollup)
		api.POST("/marbles/:marble_id/dispatch", s.dispatchMarble)

		// Objectives.
		api.GET("/objectives", s.listObjectives)
		api.POST("/objectives", s.createObjective)
		api.GET("/objectives/:objective_id", s.getObjective)
		api.PATCH("/objectives/:objective_id", s.updateObjective)
		api.DELETE("/objectives/:objective_id", s.deleteObjective)
		api.GET("/objectives/:objective_id/marbles", s.listObjectiveMarbles)
		api.POST("/objectives/:objective_id/marbles/:marble_id", s.addMarbleToObjective)
		api.DELETE("/objectives/:objective_id/marbles/:marble_id", s.removeMarbleFromObjective)
		api.POST("/objectives/:objective_id/ship", s.shipObjective)

		// Rules.
		api.GET("/rules", s.listRules)
		api.POST("/rules", s.createRule)
		api.GET("/rules/:rule_id", s.getRule)
		api.PATCH("/rules/:rule_id", s.updateRule)
		api.DELETE("/rules/:rule_id", s.deleteRule)
		api.POST("/rules/test", s.testRule)

		// Dispatches.
		api.GET("/dispatches", s.listDispatches)
		api.GET("/dispatches/:dispatch_id", s.getDispatch)
		api.POST("/dispatches/:dispatch_id/result", s.recordDispatchResult)
		api.POST("/dispatches/:dispatch_id/replay", s.replayDispatch)

		// Audit + trends.
		api.GET("/audit-log", s.listAudit)
		api.GET("/audit-log/:entry_id", s.getAuditEntry)
		api.GET("/trends/cost", s.costTrends)
		api.GET("/trends/tokens", s.tokenTrends)

		// Workspace resources.
		api.GET("/projects", s.listProjects)
		api.GET("/agents", s.listAgents)
		api.GET("/jar/status", s.jarStatus)

		// API keys.
		api.GET("/api-keys", s.listAPIKeys)
		api.POST("/api-keys", s.createAPIKey)
		api.DELETE("/api-keys/:key_id", s.revokeAPIKey)

		// Integrations (OBO connections).
		api.GET("/integrations", s.listIntegrations)
		api.GET("/integrations/:integration/connection", s.getIntegrationConnection)
		api.POST("/integrations/:integration/connect", s.connectIntegration)
		api.DELETE("/integrations/:integration/connection", s.disconnectIntegration)

		// Webhook endpoints.
		api.GET("/webhook-endpoints", s.listWebhookEndpoints)
		api.POST("/webhook-endpoints", s.createWebhookEndpoint)
		api.DELETE("/webhook-endpoints/:endpoint_id", s.deleteWebhookEndpoint)
	}

	return r
}

func (s *Server) health(c *gin.Context) {
	status := "ok"
	dbOK := s.store.Ping(c.Request.Context()) == nil
	if !dbOK {
		status = "degraded"
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": status, "database": "down",
			"ws_connections": s.hub.Connections(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":         status,
		"database":       "up",
		"ws_connections": s.hub.Connections(),
		"time":           time.Now().UTC(),
	})
}

func (s *Server) requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		dur := time.Since(start)

		// Route template (not raw path) keeps metric cardinality bounded.
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		s.metrics.ObserveRequest(c.Request.Method, route, c.Writer.Status(), dur)

		if c.Writer.Status() >= 500 {
			s.log.Error("request failed",
				"method", c.Request.Method, "path", c.Request.URL.Path,
				"status", c.Writer.Status(), "duration_ms", dur.Milliseconds(),
				"errors", c.Errors.String())
		} else if s.cfg.LogLevel == "debug" {
			s.log.Debug("request",
				"method", c.Request.Method, "path", c.Request.URL.Path,
				"status", c.Writer.Status(), "duration_ms", dur.Milliseconds())
		}
	}
}

// Broadcast pushes a realtime event to connected dashboards.
func (s *Server) broadcast(c *gin.Context, typ, orgID string, payload any) {
	ev, err := eventbus.NewEvent(typ, orgID, payload, "")
	if err != nil {
		return
	}
	if err := s.ingest.Broadcaster().Broadcast(c.Request.Context(), ev); err != nil {
		s.log.Warn("broadcast failed", "type", typ, "error", err)
	}
}
