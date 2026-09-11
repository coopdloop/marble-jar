package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/objectives"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// GET /v1/objectives
func (s *Server) listObjectives(c *gin.Context) {
	p := mustPrincipal(c)
	items, next, err := s.store.ListObjectives(c.Request.Context(), p.OrganizationID,
		store.ListObjectivesFilter{
			Status:  c.Query("status"),
			Project: c.Query("project"),
			Limit:   queryInt(c, "limit", 50),
			Cursor:  c.Query("cursor"),
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

type createObjectiveBody struct {
	Title         string   `json:"title" binding:"required"`
	Description   *string  `json:"description"`
	ProjectID     *string  `json:"project_id"`
	BudgetTokens  *int64   `json:"budget_tokens"`
	BudgetCostUSD *float64 `json:"budget_cost_usd"`
	Status        string   `json:"status"`
}

// POST /v1/objectives
func (s *Server) createObjective(c *gin.Context) {
	if !requireScope(c, "objectives:write") {
		return
	}
	p := mustPrincipal(c)

	var body createObjectiveBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	o, err := s.store.CreateObjective(c.Request.Context(), store.CreateObjectiveParams{
		OrganizationID:  p.OrganizationID,
		ProjectID:       body.ProjectID,
		Title:           body.Title,
		Description:     body.Description,
		Status:          body.Status,
		CreatedByUserID: p.ActingUserID(),
		BudgetTokens:    body.BudgetTokens,
		BudgetCostUSD:   body.BudgetCostUSD,
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "objective.created", p.OrganizationID, o)
	c.JSON(http.StatusCreated, o)
}

// GET /v1/objectives/:objective_id
func (s *Server) getObjective(c *gin.Context) {
	p := mustPrincipal(c)
	id := c.Param("objective_id")

	o, err := s.store.GetObjective(c.Request.Context(), p.OrganizationID, id)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	trend, err := s.store.ObjectiveTrend(c.Request.Context(), p.OrganizationID, id,
		c.DefaultQuery("interval", "day"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	summary, err := s.objectives.BuildSummary(c.Request.Context(), p.OrganizationID, id)
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"objective":       o,
		"rollup":          o.Rollup,
		"trend":           trend,
		"summary_preview": summary,
	})
}

type updateObjectiveBody struct {
	Title         *string  `json:"title"`
	Description   *string  `json:"description"`
	Status        *string  `json:"status"`
	ProjectID     *string  `json:"project_id"`
	BudgetTokens  *int64   `json:"budget_tokens"`
	BudgetCostUSD *float64 `json:"budget_cost_usd"`
}

// PATCH /v1/objectives/:objective_id
func (s *Server) updateObjective(c *gin.Context) {
	if !requireScope(c, "objectives:write") {
		return
	}
	p := mustPrincipal(c)

	var body updateObjectiveBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	o, err := s.store.UpdateObjective(c.Request.Context(), p.OrganizationID,
		c.Param("objective_id"), store.UpdateObjectiveParams{
			Title:         body.Title,
			Description:   body.Description,
			Status:        body.Status,
			ProjectID:     body.ProjectID,
			BudgetTokens:  body.BudgetTokens,
			BudgetCostUSD: body.BudgetCostUSD,
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "objective.updated", p.OrganizationID, o)
	c.JSON(http.StatusOK, o)
}

// DELETE /v1/objectives/:objective_id
func (s *Server) deleteObjective(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	p := mustPrincipal(c)
	id := c.Param("objective_id")

	if err := s.store.DeleteObjective(c.Request.Context(), p.OrganizationID, id); err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "objective.deleted", p.OrganizationID, gin.H{"id": id})
	c.Status(http.StatusNoContent)
}

// GET /v1/objectives/:objective_id/marbles
func (s *Server) listObjectiveMarbles(c *gin.Context) {
	p := mustPrincipal(c)
	items, next, err := s.store.ListMarbles(c.Request.Context(), p.OrganizationID,
		store.ListMarblesFilter{
			ObjectiveID: c.Param("objective_id"),
			Limit:       queryInt(c, "limit", 50),
			Cursor:      c.Query("cursor"),
		})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": next})
}

// POST /v1/objectives/:objective_id/marbles/:marble_id — drag-and-drop bucketing.
func (s *Server) addMarbleToObjective(c *gin.Context) {
	if !requireScope(c, "objectives:write") {
		return
	}
	p := mustPrincipal(c)

	m, err := s.store.AddMarbleToObjective(c.Request.Context(), p.OrganizationID,
		c.Param("objective_id"), c.Param("marble_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "marble.updated", p.OrganizationID, m)
	c.JSON(http.StatusOK, m)
}

// DELETE /v1/objectives/:objective_id/marbles/:marble_id
func (s *Server) removeMarbleFromObjective(c *gin.Context) {
	if !requireScope(c, "objectives:write") {
		return
	}
	p := mustPrincipal(c)

	m, err := s.store.RemoveMarbleFromObjective(c.Request.Context(), p.OrganizationID,
		c.Param("objective_id"), c.Param("marble_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	s.broadcast(c, "marble.updated", p.OrganizationID, m)
	c.JSON(http.StatusOK, m)
}

// POST /v1/objectives/:objective_id/ship — the Ship It flow.
func (s *Server) shipObjective(c *gin.Context) {
	if !requireScope(c, "dispatch:write") {
		return
	}
	p := mustPrincipal(c)

	var body objectives.ShipRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.IntegrationType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "integration_type is required"})
		return
	}

	res, err := s.objectives.Ship(c.Request.Context(), p, c.Param("objective_id"), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.broadcast(c, "objective.shipped", p.OrganizationID, res.Objective)
	c.JSON(http.StatusAccepted, res)
}

// GET /v1/trends/cost
func (s *Server) costTrends(c *gin.Context) { s.trends(c) }

// GET /v1/trends/tokens
func (s *Server) tokenTrends(c *gin.Context) { s.trends(c) }

func (s *Server) trends(c *gin.Context) {
	p := mustPrincipal(c)
	points, err := s.store.Trends(c.Request.Context(), p.OrganizationID, store.TrendFilter{
		Interval:    c.DefaultQuery("interval", "day"),
		Days:        queryInt(c, "days", 30),
		ProjectID:   c.Query("project_id"),
		ObjectiveID: c.Query("objective_id"),
		Model:       c.Query("model"),
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"points": points})
}

// GET /v1/jar/status — backs the monitor MCP's get_jar_status.
func (s *Server) jarStatus(c *gin.Context) {
	p := mustPrincipal(c)
	status, err := s.store.JarStatus(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

// GET /v1/projects
func (s *Server) listProjects(c *gin.Context) {
	p := mustPrincipal(c)
	items, err := s.store.ListProjects(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GET /v1/agents
func (s *Server) listAgents(c *gin.Context) {
	p := mustPrincipal(c)
	items, err := s.store.ListAgents(c.Request.Context(), p.OrganizationID)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
