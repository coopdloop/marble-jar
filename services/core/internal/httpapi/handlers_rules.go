package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/marble-jar/marble-jar/services/core/internal/dispatch"
	"github.com/marble-jar/marble-jar/services/core/internal/rules"
	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// GET /v1/rules
func (s *Server) listRules(c *gin.Context) {
	p := mustPrincipal(c)
	items, err := s.store.ListRules(c.Request.Context(), p.OrganizationID,
		c.Query("active") == "true")
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type ruleBody struct {
	Name         string          `json:"name"`
	ProjectID    *string         `json:"project_id"`
	Condition    json.RawMessage `json:"condition"`
	TargetType   string          `json:"target_type"`
	TargetConfig json.RawMessage `json:"target_config"`
	IsActive     *bool           `json:"is_active"`
}

// validate parses the condition tree and checks the target integration.
func (b *ruleBody) validate(requireAll bool) error {
	if requireAll {
		if b.Name == "" {
			return errBadRequest("name is required")
		}
		if b.TargetType == "" {
			return errBadRequest("target_type is required")
		}
	}
	if b.TargetType != "" && !dispatch.SupportedIntegrations[b.TargetType] {
		return errBadRequest("unsupported target_type: " + b.TargetType)
	}
	if len(b.Condition) > 0 {
		cond, err := rules.Parse(b.Condition)
		if err != nil {
			return errBadRequest(err.Error())
		}
		if err := cond.Validate(); err != nil {
			return errBadRequest(err.Error())
		}
	}
	return nil
}

type badRequest struct{ msg string }

func (e badRequest) Error() string { return e.msg }
func errBadRequest(m string) error { return badRequest{m} }

// POST /v1/rules
func (s *Server) createRule(c *gin.Context) {
	if !requireScope(c, "rules:write") {
		return
	}
	p := mustPrincipal(c)

	var body ruleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := body.validate(true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	active := true
	if body.IsActive != nil {
		active = *body.IsActive
	}

	r, err := s.store.CreateRule(c.Request.Context(), store.CreateRuleParams{
		OrganizationID:  p.OrganizationID,
		ProjectID:       body.ProjectID,
		Name:            body.Name,
		Condition:       body.Condition,
		TargetType:      body.TargetType,
		TargetConfig:    body.TargetConfig,
		IsActive:        active,
		CreatedByUserID: p.ActingUserID(),
	})
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, r)
}

// GET /v1/rules/:rule_id
func (s *Server) getRule(c *gin.Context) {
	p := mustPrincipal(c)
	r, err := s.store.GetRule(c.Request.Context(), p.OrganizationID, c.Param("rule_id"))
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

// PATCH /v1/rules/:rule_id
func (s *Server) updateRule(c *gin.Context) {
	if !requireScope(c, "rules:write") {
		return
	}
	p := mustPrincipal(c)

	var body ruleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := body.validate(false); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	params := store.UpdateRuleParams{
		Condition:    body.Condition,
		TargetConfig: body.TargetConfig,
		IsActive:     body.IsActive,
		ProjectID:    body.ProjectID,
	}
	if body.Name != "" {
		params.Name = &body.Name
	}
	if body.TargetType != "" {
		params.TargetType = &body.TargetType
	}

	r, err := s.store.UpdateRule(c.Request.Context(), p.OrganizationID, c.Param("rule_id"), params)
	if err != nil {
		respondStoreErr(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

// DELETE /v1/rules/:rule_id
func (s *Server) deleteRule(c *gin.Context) {
	if !requireScope(c, "rules:write") {
		return
	}
	p := mustPrincipal(c)
	if err := s.store.DeleteRule(c.Request.Context(), p.OrganizationID, c.Param("rule_id")); err != nil {
		respondStoreErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type testRuleBody struct {
	Condition json.RawMessage `json:"condition"`
	ProjectID *string         `json:"project_id"`
	Sample    int             `json:"sample"`
}

// POST /v1/rules/test — "test against last 20 marbles" preview.
func (s *Server) testRule(c *gin.Context) {
	p := mustPrincipal(c)

	var body testRuleBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Sample <= 0 || body.Sample > 100 {
		body.Sample = 20
	}

	cond, err := rules.Parse(body.Condition)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := cond.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sample, _, err := s.store.ListMarbles(c.Request.Context(), p.OrganizationID,
		store.ListMarblesFilter{Limit: body.Sample})
	if err != nil {
		respondStoreErr(c, err)
		return
	}

	type result struct {
		Marble  store.Marble `json:"marble"`
		Matched bool         `json:"matched"`
	}
	out := make([]result, 0, len(sample))
	matched := 0
	for i := range sample {
		if body.ProjectID != nil &&
			(sample[i].ProjectID == nil || *sample[i].ProjectID != *body.ProjectID) {
			out = append(out, result{Marble: sample[i], Matched: false})
			continue
		}
		ok := cond.Matches(&sample[i])
		if ok {
			matched++
		}
		out = append(out, result{Marble: sample[i], Matched: ok})
	}

	c.JSON(http.StatusOK, gin.H{
		"sample_size":   len(sample),
		"matched_count": matched,
		"results":       out,
	})
}
