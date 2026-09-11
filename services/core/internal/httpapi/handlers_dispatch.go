package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// GET /v1/dispatches
func (s *Server) listDispatches(c *gin.Context) {
	p := mustPrincipal(c)
	items, next, err := s.store.ListDispatches(c.Request.Context(), p.OrganizationID,
		store.ListDispatchesFilter{
			Status:          c.Query("status"),
			IntegrationType: c.Query("integration_type"),
			MarbleID:        c.Query("marble_id"),
			ObjectiveID:     c.Query("objective_id"),
			Limit:           queryInt(c, "limit", 50),
			Cursor:          c.Query("cursor"),
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

// GET /v1/dispatches/:dispatch_id
func (s *Server) getDispatch(c *gin.Context) {
	p := mustPrincipal(c)
	d, err := s.store.GetDispatch(c.Request.Context(), p.OrganizationID, c.Param("dispatch_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

type dispatchResultBody struct {
	Status          string          `json:"status" binding:"required"`
	ExternalRef     *string         `json:"external_ref"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	ErrorMessage    *string         `json:"error_message"`
	AttemptCount    *int            `json:"attempt_count"`
	Detail          map[string]any  `json:"detail"`
}

var validDispatchStatuses = map[string]bool{
	"pending": true, "running": true, "succeeded": true,
	"failed": true, "retrying": true, "dead_lettered": true,
}

// POST /v1/dispatches/:dispatch_id/result — worker callback.
func (s *Server) recordDispatchResult(c *gin.Context) {
	if !requireScope(c, "dispatch:write") {
		return
	}
	p := mustPrincipal(c)

	var body dispatchResultBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !validDispatchStatuses[body.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status: " + body.Status})
		return
	}

	d, err := s.dispatcher.RecordResult(c.Request.Context(), p.OrganizationID,
		c.Param("dispatch_id"), store.DispatchResultParams{
			Status:          body.Status,
			ExternalRef:     body.ExternalRef,
			ResponsePayload: body.ResponsePayload,
			ErrorMessage:    body.ErrorMessage,
			AttemptCount:    body.AttemptCount,
		}, body.Detail)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "dispatch.updated", p.OrganizationID, d)
	c.JSON(http.StatusOK, d)
}

// POST /v1/dispatches/:dispatch_id/replay — requeue a stuck dispatch.
func (s *Server) replayDispatch(c *gin.Context) {
	if !requireScope(c, "dispatch:write") {
		return
	}
	p := mustPrincipal(c)

	d, err := s.dispatcher.Replay(c.Request.Context(), p, c.Param("dispatch_id"))
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	s.broadcast(c, "dispatch.updated", p.OrganizationID, d)
	c.JSON(http.StatusAccepted, d)
}

// GET /v1/audit-log
func (s *Server) listAudit(c *gin.Context) {
	p := mustPrincipal(c)
	items, next, err := s.store.ListAudit(c.Request.Context(), p.OrganizationID,
		store.ListAuditFilter{
			Provider:   c.Query("provider"),
			Action:     c.Query("action"),
			MarbleID:   c.Query("marble_id"),
			DispatchID: c.Query("dispatch_id"),
			Limit:      queryInt(c, "limit", 50),
			Cursor:     c.Query("cursor"),
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

// GET /v1/audit-log/:entry_id
func (s *Server) getAuditEntry(c *gin.Context) {
	p := mustPrincipal(c)
	e, err := s.store.GetAuditEntry(c.Request.Context(), p.OrganizationID, c.Param("entry_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

// GET /v1/webhook-endpoints
func (s *Server) listWebhookEndpoints(c *gin.Context) {
	p := mustPrincipal(c)
	items, err := s.store.ListWebhookEndpoints(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createWebhookBody struct {
	Name string `json:"name" binding:"required"`
	URL  string `json:"url" binding:"required,url"`
}

// POST /v1/webhook-endpoints
func (s *Server) createWebhookEndpoint(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)

	var body createWebhookBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Each endpoint gets its own signing secret for HMAC-signed deliveries.
	secret, err := randomSecret()
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	w, err := s.store.CreateWebhookEndpoint(c.Request.Context(), p.OrganizationID,
		body.Name, body.URL, secret)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	// The secret is shown exactly once, at creation.
	c.JSON(http.StatusCreated, gin.H{"endpoint": w, "secret": secret})
}

// DELETE /v1/webhook-endpoints/:endpoint_id
func (s *Server) deleteWebhookEndpoint(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)
	if err := s.store.DeleteWebhookEndpoint(c.Request.Context(), p.OrganizationID,
		c.Param("endpoint_id")); err != nil {
		respondStoreErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
