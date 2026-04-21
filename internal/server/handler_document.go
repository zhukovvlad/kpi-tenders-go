package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type createDocumentRequest struct {
	SiteID        *string `json:"site_id"`
	ParentID      *string `json:"parent_id"`
	FileName      string  `json:"file_name"      binding:"required"`
	StoragePath   string  `json:"storage_path"   binding:"required"`
	MimeType      *string `json:"mime_type"`
	FileSizeBytes *int64  `json:"file_size_bytes"`
}

func (s *Server) CreateDocument(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	userID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	var req createDocumentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	params := repository.CreateDocumentParams{
		OrganizationID: orgID,
		UploadedBy:     userID,
		FileName:       req.FileName,
		StoragePath:    req.StoragePath,
	}

	if req.SiteID != nil {
		id, err := uuid.Parse(*req.SiteID)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid site_id", err))
			return
		}
		params.SiteID = pgtype.UUID{Bytes: id, Valid: true}
	}

	if req.ParentID != nil {
		id, err := uuid.Parse(*req.ParentID)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid parent_id", err))
			return
		}
		params.ParentID = pgtype.UUID{Bytes: id, Valid: true}
	}

	if req.MimeType != nil {
		params.MimeType = pgtype.Text{String: *req.MimeType, Valid: true}
	}

	if req.FileSizeBytes != nil {
		params.FileSizeBytes = pgtype.Int8{Int64: *req.FileSizeBytes, Valid: true}
	}

	doc, err := s.documentService.Create(c.Request.Context(), params)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, doc)
}

func (s *Server) ListDocuments(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	if siteIDStr := c.Query("site_id"); siteIDStr != "" {
		siteID, err := uuid.Parse(siteIDStr)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid site_id", err))
			return
		}
		docs, err := s.documentService.ListBySite(c.Request.Context(), orgID, siteID)
		if err != nil {
			s.respondWithError(c, err)
			return
		}
		c.JSON(http.StatusOK, docs)
		return
	}

	docs, err := s.documentService.ListByOrganization(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}
	c.JSON(http.StatusOK, docs)
}

func (s *Server) GetDocument(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	doc, err := s.documentService.Get(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, doc)
}

func (s *Server) DeleteDocument(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.documentService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func parseUUID(raw string) (uuid.UUID, error) {
	return uuid.Parse(raw)
}
