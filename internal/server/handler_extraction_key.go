package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

// resolveExtractionKeyRequest is the JSON body for resolving a natural-language
// question into a normalized extraction key.
type resolveExtractionKeyRequest struct {
	SourceQuery string `json:"source_query" binding:"required"`
}

// resolveExtractionKeyResponse returns the resolved key plus whether it was
// reused from an existing source query/key_name.
type resolveExtractionKeyResponse struct {
	Key       repository.ExtractionKey `json:"key"`
	Duplicate bool                     `json:"duplicate"`
}

// ResolveExtractionKey converts a user's natural-language question into a
// tenant-scoped extraction key, returning an existing key when it is a duplicate.
func (s *Server) ResolveExtractionKey(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	var req resolveExtractionKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	key, duplicate, err := s.extractionKeyService.Resolve(c.Request.Context(), service.ResolveExtractionKeyParams{
		OrganizationID: orgID,
		SourceQuery:    req.SourceQuery,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	c.JSON(status, resolveExtractionKeyResponse{Key: key, Duplicate: duplicate})
}
