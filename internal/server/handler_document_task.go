package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type createDocumentTaskRequest struct {
	DocumentID string `json:"document_id" binding:"required"`
	ModuleName string `json:"module_name" binding:"required"`
}

func (s *Server) CreateDocumentTask(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	var req createDocumentTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	docID, err := uuid.Parse(req.DocumentID)
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document_id", err))
		return
	}

	task, err := s.documentTaskService.Create(c.Request.Context(), repository.CreateDocumentTaskParams{
		DocumentID:     docID,
		ModuleName:     req.ModuleName,
		OrganizationID: orgID,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, task)
}

func (s *Server) ListDocumentTasks(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	docIDStr, docIDPresent := c.GetQuery("document_id")
	docIDsStr, docIDsPresent := c.GetQuery("document_ids")

	if docIDPresent && docIDsPresent {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "use document_id or document_ids, not both", nil))
		return
	}

	if docIDsPresent {
		if docIDsStr == "" {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "document_ids cannot be empty", nil))
			return
		}
		parts := strings.Split(docIDsStr, ",")
		if len(parts) > 100 {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "too many document_ids (max 100)", nil))
			return
		}
		ids := make([]uuid.UUID, 0, len(parts))
		for _, p := range parts {
			id, err := uuid.Parse(strings.TrimSpace(p))
			if err != nil {
				s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document_id in document_ids", err))
				return
			}
			ids = append(ids, id)
		}
		tasks, err := s.documentTaskService.ListByDocuments(c.Request.Context(), ids, orgID)
		if err != nil {
			s.respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, tasks)
		return
	}

	if !docIDPresent {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "document_id or document_ids query param is required", nil))
		return
	}
	docID, err := uuid.Parse(docIDStr)
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document_id", err))
		return
	}
	tasks, err := s.documentTaskService.ListByDocument(c.Request.Context(), docID, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) GetDocumentTask(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	task, err := s.documentTaskService.Get(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, task)
}

type updateTaskStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

var validTaskStatuses = map[string]bool{
	"pending": true, "processing": true, "completed": true, "failed": true,
}

func (s *Server) UpdateDocumentTaskStatus(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	var req updateTaskStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	if !validTaskStatuses[req.Status] {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid status: must be pending, processing, completed or failed", nil))
		return
	}

	task, err := s.documentTaskService.UpdateStatus(c.Request.Context(), repository.UpdateDocumentTaskStatusParams{
		ID:             id,
		OrganizationID: orgID,
		Status:         req.Status,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, task)
}

func (s *Server) DeleteDocumentTask(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.documentTaskService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
