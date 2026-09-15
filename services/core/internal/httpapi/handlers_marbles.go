package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/marbles"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

const maxIngestBody = 1 << 20 // 1 MiB

// POST /v1/marbles — the ingestion hot path.
func (s *Server) logMarble(c *gin.Context) {
	if !requireScope(c, "marbles:write") {
		return
	}
	p := mustPrincipal(c)

	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxIngestBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unable to read body"})
		return
	}

	var req marbles.LogMarbleRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Header form of the idempotency key wins when both are present.
	if hk := c.GetHeader("Idempotency-Key"); hk != "" {
		req.IdempotencyKey = hk
	}

	res, err := s.ingest.Ingest(c.Request.Context(), p, &req, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	status := http.StatusCreated
	if !res.Created {
		status = http.StatusOK // idempotent replay
	}
	c.JSON(status, gin.H{
		"marble_id":   res.Marble.ID,
		"accepted_at": res.Marble.CreatedAt,
		"marble":      res.Marble,
		"created":     res.Created,
	})
}

// GET /v1/ws/queue — live jar feed.
func (s *Server) serveQueueWS(c *gin.Context) {
	p := mustPrincipal(c)
	if err := s.hub.ServeWS(c.Writer, c.Request, p.OrganizationID); err != nil {
		s.log.Warn("ws upgrade failed", "error", err)
	}
}

// GET /v1/marbles
func (s *Server) listMarbles(c *gin.Context) {
	p := mustPrincipal(c)

	f := store.ListMarblesFilter{
		Project:     c.Query("project"),
		ObjectiveID: c.Query("objective_id"),
		Model:       c.Query("model"),
		AgentID:     c.Query("agent_id"),
		Status:      c.Query("status"),
		Search:      c.Query("q"),
		Unassigned:  c.Query("unassigned") == "true",
		Cursor:      c.Query("cursor"),
		Limit:       queryInt(c, "limit", 50),
	}
	if from, ok := queryTime(c, "from"); ok {
		f.From = &from
	}
	if to, ok := queryTime(c, "to"); ok {
		f.To = &to
	}

	items, next, err := s.store.ListMarbles(c.Request.Context(), p.OrganizationID, f)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

// GET /v1/marbles/:marble_id
func (s *Server) getMarble(c *gin.Context) {
	p := mustPrincipal(c)
	m, err := s.store.GetMarble(c.Request.Context(), p.OrganizationID, c.Param("marble_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	// Attach the dispatch history so the detail sheet renders in one round trip.
	dispatches, _, err := s.store.ListDispatches(c.Request.Context(), p.OrganizationID,
		store.ListDispatchesFilter{MarbleID: m.ID, Limit: 50})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"marble": m, "dispatches": dispatches})
}

type updateMarbleBody struct {
	Summary        *string         `json:"summary"`
	Status         *string         `json:"status"`
	ObjectiveID    *string         `json:"objective_id"`
	ClearObjective bool            `json:"clear_objective"`
	Metadata       json.RawMessage `json:"metadata"`
}

// PATCH /v1/marbles/:marble_id
func (s *Server) updateMarble(c *gin.Context) {
	if !requireScope(c, "marbles:write") {
		return
	}
	p := mustPrincipal(c)

	var body updateMarbleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Metadata != nil && !json.Valid(body.Metadata) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata must be valid JSON"})
		return
	}

	m, err := s.store.UpdateMarble(c.Request.Context(), p.OrganizationID, c.Param("marble_id"),
		store.UpdateMarbleParams{
			Summary:        body.Summary,
			Status:         body.Status,
			ObjectiveID:    body.ObjectiveID,
			ClearObjective: body.ClearObjective,
			Metadata:       body.Metadata,
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "marble.updated", p.OrganizationID, m)
	c.JSON(http.StatusOK, m)
}

// DELETE /v1/marbles/:marble_id (admin/correction only)
func (s *Server) deleteMarble(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)
	id := c.Param("marble_id")

	if err := s.store.DeleteMarble(c.Request.Context(), p.OrganizationID, id); err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "marble.deleted", p.OrganizationID, gin.H{"id": id})
	c.Status(http.StatusNoContent)
}

type rollupBody struct {
	TokensIn        *int            `json:"tokens_in"`
	TokensOut       *int            `json:"tokens_out"`
	CostUSD         *float64        `json:"cost_usd"`
	DurationMS      *int            `json:"duration_ms"`
	TraceID         *string         `json:"trace_id"`
	PhoenixTraceURL *string         `json:"phoenix_trace_url"`
	PhoenixProject  *string         `json:"phoenix_project_name"`
	SpanID          *string         `json:"span_id"`
	Metrics         json.RawMessage `json:"rollup_metrics"`
}

// POST /v1/marbles/:marble_id/rollups — internal, used by phoenix_bridge.
func (s *Server) writeRollup(c *gin.Context) {
	if !requireScope(c, "rollups:write") {
		return
	}
	p := mustPrincipal(c)

	var body rollupBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Derive the Phoenix deep link when the bridge only supplies a trace id.
	if body.TraceID != nil && body.PhoenixTraceURL == nil && s.cfg.PhoenixBaseURL != "" {
		url := s.cfg.PhoenixBaseURL + "/v1/traces/" + *body.TraceID
		body.PhoenixTraceURL = &url
	}

	m, err := s.store.WriteMarbleRollup(c.Request.Context(), p.OrganizationID,
		c.Param("marble_id"), store.RollupParams{
			TokensIn:        body.TokensIn,
			TokensOut:       body.TokensOut,
			CostUSD:         body.CostUSD,
			DurationMS:      body.DurationMS,
			TraceID:         body.TraceID,
			PhoenixTraceURL: body.PhoenixTraceURL,
			PhoenixProject:  body.PhoenixProject,
			SpanID:          body.SpanID,
			Metrics:         body.Metrics,
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "marble.updated", p.OrganizationID, m)
	c.JSON(http.StatusOK, m)
}

type dispatchMarbleBody struct {
	IntegrationType string          `json:"integration_type" binding:"required"`
	Action          string          `json:"action"`
	TargetConfig    json.RawMessage `json:"target_config"`
	Payload         json.RawMessage `json:"payload"`
}

// POST /v1/marbles/:marble_id/dispatch — human-in-the-loop dispatch.
func (s *Server) dispatchMarble(c *gin.Context) {
	if !requireScope(c, "dispatch:write") {
		return
	}
	p := mustPrincipal(c)
	marbleID := c.Param("marble_id")

	var body dispatchMarbleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	m, err := s.store.GetMarble(c.Request.Context(), p.OrganizationID, marbleID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	payload := body.Payload
	if len(payload) == 0 {
		payload = dispatch.MarblePayload(m)
	}

	d, err := s.dispatcher.Create(c.Request.Context(), p, dispatch.Request{
		IntegrationType: body.IntegrationType,
		Action:          body.Action,
		MarbleID:        &marbleID,
		TargetConfig:    body.TargetConfig,
		Payload:         payload,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.broadcast(c, "dispatch.queued", p.OrganizationID, d)
	c.JSON(http.StatusAccepted, d)
}

// ---- shared query helpers ----

func queryInt(c *gin.Context, key string, fallback int) int {
	if v, err := strconv.Atoi(c.Query(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// queryCSV reads a multi-value filter, accepting both comma-separated and
// repeated params (?status=a,b and ?status=a&status=b), and drops blanks.
func queryCSV(c *gin.Context, key string) []string {
	var out []string
	for _, raw := range c.QueryArray(key) {
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func queryTime(c *gin.Context, key string) (time.Time, bool) {
	raw := c.Query(key)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
