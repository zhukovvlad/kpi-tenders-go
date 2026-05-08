package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/pkg/errs"
)

type createExtractionKeyRequest struct {
	KeyName     string  `json:"key_name" binding:"required"`
	SourceQuery string  `json:"source_query" binding:"required"`
	DataType    string  `json:"data_type" binding:"required,oneof=string number date boolean"`
	DisplayName *string `json:"display_name"`
}

type updateExtractionKeyRequest struct {
	SourceQuery *string `json:"source_query"`
	DataType    *string `json:"data_type" binding:"omitempty,oneof=string number date boolean"`
	DisplayName *string `json:"display_name"`
}

// ListExtractionKeys handles GET /api/v1/extraction-keys.
// Returns all keys visible to the tenant (org-specific and system keys).
func (s *Server) ListExtractionKeys(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	keys, err := s.extractionKeyService.List(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, keys)
}

// CreateExtractionKey handles POST /api/v1/extraction-keys.
// Creates a new org-specific extraction key.
func (s *Server) CreateExtractionKey(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	var req createExtractionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	key, err := s.extractionKeyService.Create(c.Request.Context(), orgID, req.KeyName, req.SourceQuery, req.DataType, req.DisplayName)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, key)
}

// GetExtractionKey handles GET /api/v1/extraction-keys/:id.
func (s *Server) GetExtractionKey(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	key, err := s.extractionKeyService.Get(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, key)
}

// UpdateExtractionKey handles PATCH /api/v1/extraction-keys/:id.
// Partially updates an org-specific extraction key. System keys are read-only.
func (s *Server) UpdateExtractionKey(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	var req updateExtractionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	key, err := s.extractionKeyService.Update(c.Request.Context(), id, orgID, req.SourceQuery, req.DataType, req.DisplayName)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, key)
}

// DeleteExtractionKey handles DELETE /api/v1/extraction-keys/:id.
// Deletes an org-specific extraction key. Returns 404 for system keys.
func (s *Server) DeleteExtractionKey(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.extractionKeyService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
