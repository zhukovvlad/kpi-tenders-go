package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

// extractionRequestResponse is the JSON shape returned by GET /extraction-requests/:id.
// Answers is empty until the extract task completes; resolved_schema is empty
// until resolve_keys completes.
type extractionRequestResponse struct {
	ID              uuid.UUID                  `json:"id"`
	DocumentID      uuid.UUID                  `json:"document_id"`
	Status          string                     `json:"status"`
	Anonymize       bool                       `json:"anonymize"`
	Questions       []string                   `json:"questions"`
	ResolvedSchema  []resolvedSchemaItem       `json:"resolved_schema,omitempty"`
	Answers         []service.ExtractionAnswer `json:"answers,omitempty"`
	ErrorMessage    *string                    `json:"error_message,omitempty"`
	CreatedAt       time.Time                  `json:"created_at"`
	UpdatedAt       time.Time                  `json:"updated_at"`
}

type resolvedSchemaItem struct {
	KeyName  string `json:"key_name"`
	DataType string `json:"data_type"`
}

// GetExtractionRequest handles GET /api/v1/extraction-requests/:id.
// Tenant-scoped: only the owning organization can read its requests.
func (s *Server) GetExtractionRequest(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid extraction request id", err))
		return
	}

	req, err := s.extractionService.GetRequest(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	answers, err := s.extractionService.GetAnswers(c.Request.Context(), req)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, buildExtractionRequestResponse(req, answers))
}

// buildExtractionRequestResponse converts the repository row plus answers
// into the JSON shape returned to clients. Marshal/unmarshal of JSONB columns
// happens here so the handler stays thin.
func buildExtractionRequestResponse(
	req repository.ExtractionRequest,
	answers []service.ExtractionAnswer,
) extractionRequestResponse {
	resp := extractionRequestResponse{
		ID:         req.ID,
		DocumentID: req.DocumentID,
		Status:     req.Status,
		Anonymize:  req.Anonymize,
		Answers:    answers,
		CreatedAt:  req.CreatedAt,
		UpdatedAt:  req.UpdatedAt,
	}

	var questions []string
	if err := json.Unmarshal(req.Questions, &questions); err == nil {
		resp.Questions = questions
	}

	if len(req.ResolvedSchema) > 0 {
		var schema []resolvedSchemaItem
		if err := json.Unmarshal(req.ResolvedSchema, &schema); err == nil {
			resp.ResolvedSchema = schema
		}
	}

	if req.ErrorMessage.Valid {
		msg := req.ErrorMessage.String
		resp.ErrorMessage = &msg
	}

	return resp
}
