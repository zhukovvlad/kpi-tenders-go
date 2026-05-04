package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/pkg/errs"
)

type createComparisonSessionRequest struct {
	Name           *string `json:"name"`
	ContractKindID *string `json:"contract_kind_id"`
}

type addDocumentToSessionRequest struct {
	DocumentID string `json:"document_id" binding:"required"`
	Position   *int16 `json:"position"`
}

func (s *Server) ListComparisonSessions(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	sessions, err := s.comparisonSessionService.ListByOrg(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, sessions)
}

func (s *Server) CreateComparisonSession(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	userID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	var req createComparisonSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	var contractKindID *uuid.UUID
	if req.ContractKindID != nil {
		id, err := parseUUID(*req.ContractKindID)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid contract_kind_id", err))
			return
		}
		contractKindID = &id
	}

	session, err := s.comparisonSessionService.Create(c.Request.Context(), orgID, userID, req.Name, contractKindID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, session)
}

func (s *Server) GetComparisonSession(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	session, err := s.comparisonSessionService.Get(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	docs, err := s.comparisonSessionService.ListDocuments(c.Request.Context(), id)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session":   session,
		"documents": docs,
	})
}

func (s *Server) DeleteComparisonSession(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.comparisonSessionService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) AddDocumentToSession(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	sessionID, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid session id", err))
		return
	}

	// Verify session belongs to org.
	if _, err := s.comparisonSessionService.Get(c.Request.Context(), sessionID, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	var req addDocumentToSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	documentID, err := parseUUID(req.DocumentID)
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document_id", err))
		return
	}

	position := int16(0)
	if req.Position != nil {
		position = *req.Position
	}

	doc, err := s.comparisonSessionService.AddDocument(c.Request.Context(), sessionID, documentID, orgID, position)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, doc)
}

func (s *Server) RemoveDocumentFromSession(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	sessionID, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid session id", err))
		return
	}

	// Verify session belongs to org.
	if _, err := s.comparisonSessionService.Get(c.Request.Context(), sessionID, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	documentID, err := parseUUID(c.Param("doc_id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid document_id", err))
		return
	}

	if err := s.comparisonSessionService.RemoveDocument(c.Request.Context(), sessionID, documentID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
