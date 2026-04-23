package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

const maxUploadSize = 100 << 20 // 100 MiB

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
		// Verify the site belongs to the authenticated org.
		if _, err := s.constructionSiteService.Get(c.Request.Context(), id, orgID); err != nil {
			s.respondWithError(c, err)
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
		// Verify the parent document belongs to the authenticated org.
		if _, err := s.documentService.Get(c.Request.Context(), id, orgID); err != nil {
			s.respondWithError(c, err)
			return
		}
		params.ParentID = pgtype.UUID{Bytes: id, Valid: true}
	}

	if req.MimeType != nil {
		params.MimeType = pgtype.Text{String: *req.MimeType, Valid: true}
	}

	if req.FileSizeBytes != nil {
		if *req.FileSizeBytes < 0 {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "file_size_bytes must be non-negative", nil))
			return
		}
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

// UploadDocument accepts a multipart/form-data request, uploads the file to
// S3/MinIO, and creates the corresponding document record in the database.
//
// Form fields:
//   - file        (required) — the file binary
//   - site_id     (optional) — UUID of the construction site
//   - parent_id   (optional) — UUID of the parent document
func (s *Server) UploadDocument(c *gin.Context) {
	if s.storageClient == nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "storage not configured", nil))
		return
	}

	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	userID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	// Limit request body size before parsing.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "file is required", err))
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "cannot open uploaded file", err))
		return
	}
	defer f.Close()

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	storagePath, err := s.storageClient.Upload(
		c.Request.Context(),
		f,
		fileHeader.Size,
		fileHeader.Filename,
		contentType,
	)
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "upload failed", err))
		return
	}

	params := repository.CreateDocumentParams{
		OrganizationID: orgID,
		UploadedBy:     userID,
		FileName:       fileHeader.Filename,
		StoragePath:    storagePath,
		MimeType:       pgtype.Text{String: contentType, Valid: true},
		FileSizeBytes:  pgtype.Int8{Int64: fileHeader.Size, Valid: true},
	}

	if siteIDStr := c.PostForm("site_id"); siteIDStr != "" {
		id, err := uuid.Parse(siteIDStr)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid site_id", err))
			return
		}
		if _, err := s.constructionSiteService.Get(c.Request.Context(), id, orgID); err != nil {
			s.respondWithError(c, err)
			return
		}
		params.SiteID = pgtype.UUID{Bytes: id, Valid: true}
	}

	if parentIDStr := c.PostForm("parent_id"); parentIDStr != "" {
		id, err := uuid.Parse(parentIDStr)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid parent_id", err))
			return
		}
		if _, err := s.documentService.Get(c.Request.Context(), id, orgID); err != nil {
			s.respondWithError(c, err)
			return
		}
		params.ParentID = pgtype.UUID{Bytes: id, Valid: true}
	}

	doc, err := s.documentService.Create(c.Request.Context(), params)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, doc)
}
