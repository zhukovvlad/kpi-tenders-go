package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/pkg/errs"
)

// initiateExtractionRequest is the JSON body of POST /api/v1/documents/:id/extract.
// Anonymize is a pointer so the omitted-field default (true) can be distinguished
// from an explicit `false` in the request body.
type initiateExtractionRequest struct {
	Questions []string `json:"questions" binding:"required,min=1"`
	Anonymize *bool    `json:"anonymize,omitempty"`
}

// InitiateExtraction handles POST /api/v1/documents/:id/extract.
// It creates an extraction_request and starts the appropriate pipeline
// (convert → anonymize → resolve_keys → extract, skipping anonymize when the
// caller opts out). Returns the extraction_request id so the client can poll.
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

	anonymize := true
	if req.Anonymize != nil {
		anonymize = *req.Anonymize
	}

	created, err := s.extractionService.Initiate(c.Request.Context(), docID, orgID, req.Questions, anonymize)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"extraction_request_id": created.ID,
		"status":                created.Status,
	})
}
