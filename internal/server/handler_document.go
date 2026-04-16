package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/pkg/errs"
)

func (s *Server) CreateDocument(c *gin.Context) {
	// TODO: implement via s.documentService
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) ListDocuments(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) GetDocument(c *gin.Context) {
	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	doc, err := s.documentService.Get(c.Request.Context(), id)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, doc)
}

func (s *Server) UpdateDocumentStatus(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) DeleteDocument(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) CreateDocumentTask(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) ListDocumentTasks(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) GetDocumentTask(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) UpdateDocumentTaskStatus(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func (s *Server) DeleteDocumentTask(c *gin.Context) {
	s.respondWithError(c, errs.New(errs.CodeNotImplemented, "not implemented", nil))
}

func parseUUID(raw string) (uuid.UUID, error) {
	return uuid.Parse(raw)
}
