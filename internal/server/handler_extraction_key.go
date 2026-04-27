package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

type resolveExtractionKeyRequest struct {
	SourceQuery string `json:"source_query" binding:"required"`
}

type resolveExtractionKeyResponse struct {
	Key       any  `json:"key"`
	Duplicate bool `json:"duplicate"`
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
