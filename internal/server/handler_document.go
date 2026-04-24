package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

const (
	// maxFileSize is the intended maximum size for the uploaded file itself.
	maxFileSize = 100 << 20 // 100 MiB

	// maxRequestBodySize is the limit applied to the entire multipart request body,
	// which includes MIME boundaries, part headers, and other form fields in
	// addition to the file data. 1 MiB of headroom is sufficient for any
	// realistic multipart envelope.
	maxRequestBodySize = maxFileSize + 1<<20 // 101 MiB

	// maxFileSizeMiB is maxFileSize expressed in MiB for human-readable error messages.
	maxFileSizeMiB = maxFileSize >> 20 // 100
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

// GetDocumentPresignedURL generates a short-lived presigned GET URL for the
// document identified by :id.
//
// Query parameters:
//
//	download=true  — sets Content-Disposition: attachment so the browser
//	                 downloads the file instead of opening it inline.
//	download=false (default) — Content-Disposition is omitted, so browser
//	                            behavior depends on the content type and client.
func (s *Server) GetDocumentPresignedURL(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	download := c.Query("download") == "true"

	presignedURL, err := s.documentService.GetPresignedURL(c.Request.Context(), id, orgID, download)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": presignedURL})
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
		s.respondWithError(c, errs.New(errs.CodeInternalError, "storage unavailable", nil))
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

	// Limit request body size before parsing. maxRequestBodySize adds headroom
	// above maxFileSize to accommodate multipart boundaries and part headers.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, http.ErrMissingFile):
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "file is required", err))
		// errors.As may not propagate through all multipart parsing layers;
		// the string check is a belt-and-suspenders fallback for Go versions
		// where MaxBytesError is wrapped before reaching FormFile.
		case errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large"):
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, fmt.Sprintf("file too large (max %d MiB)", maxFileSizeMiB), err))
		default:
			s.respondWithError(c, errs.New(errs.CodeInternalError, "cannot parse upload", err))
		}
		return
	}
	// net/http may have spilled multipart parts to temporary disk files during
	// FormFile. Clean them up when the handler returns regardless of outcome.
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll() //nolint:errcheck
	}

	// Validate optional fields BEFORE uploading to avoid orphaned S3 objects.
	// Normalize filename: strip leading path components (including Windows-style
	// backslash paths like "C:\fakepath\file.pdf") before storing.
	fileName := strings.TrimSpace(filepath.Base(strings.ReplaceAll(fileHeader.Filename, "\\", "/")))
	if fileName == "" || fileName == "." || fileName == ".." {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid file name", nil))
		return
	}
	// Enforce the per-file size limit explicitly. MaxBytesReader caps the entire
	// request body (file + multipart overhead), so a file just over 100 MiB could
	// still slip through if its overhead keeps the total under maxRequestBodySize.
	if fileHeader.Size > maxFileSize {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, fmt.Sprintf("file too large (max %d MiB)", maxFileSizeMiB), nil))
		return
	}

	params := repository.CreateDocumentParams{
		OrganizationID: orgID,
		UploadedBy:     userID,
		FileName:       fileName,
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

	f, err := fileHeader.Open()
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "cannot open uploaded file", err))
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			s.log.Error("upload: failed to close file", "err", err)
		}
	}()

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	params.MimeType = pgtype.Text{String: contentType, Valid: true}

	storagePath, err := s.storageClient.Upload(
		c.Request.Context(),
		f,
		fileHeader.Size,
		fileName,
		contentType,
	)
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "upload failed", err))
		return
	}
	params.StoragePath = storagePath

	doc, err := s.documentService.Create(c.Request.Context(), params)
	if err != nil {
		// Best-effort cleanup: remove the uploaded object to avoid orphaned S3 objects.
		// Use a fresh background context with a timeout: c.Request.Context() may
		// already be cancelled (e.g. client disconnect) which would prevent Delete
		// from running — exactly the scenario the cleanup exists to handle.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if delErr := s.storageClient.Delete(cleanupCtx, storagePath); delErr != nil {
			s.log.Error("upload: failed to delete orphaned object", "path", storagePath, "err", delErr)
		}
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, doc)
}
