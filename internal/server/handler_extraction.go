package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/pkg/errs"
)

type initiateExtractionRequest struct {
	Questions []string `json:"questions" binding:"required,min=1"`
}

// InitiateExtraction handles POST /api/v1/documents/:id/extract.
// It starts the resolve_keys → extract pipeline for the given document and
// returns the task_id so the client can poll for progress.
func (s *Server) InitiateExtraction(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	docID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document id", err))
		return
	}

	var req initiateExtractionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request body", err))
		return
	}

	task, err := s.extractionService.Initiate(c.Request.Context(), docID, orgID, req.Questions)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"task_id": task.ID})
}
