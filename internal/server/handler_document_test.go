package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/config"
	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/internal/storage"
	storemock "go-kpi-tenders/internal/store/mock"
	"go-kpi-tenders/pkg/errs"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// mockStorageClient is a testify-mock implementation of the storageClient
// interface, allowing Upload/Delete/PresignedURL to be stubbed in unit tests.
type mockStorageClient struct {
	mock.Mock
}

func (m *mockStorageClient) Upload(ctx context.Context, r io.Reader, size int64, originalFilename, contentType string) (string, error) {
	args := m.Called(ctx, r, size, originalFilename, contentType)
	return args.String(0), args.Error(1)
}

func (m *mockStorageClient) PresignedURL(ctx context.Context, storagePath string, ttl time.Duration) (string, error) {
	args := m.Called(ctx, storagePath, ttl)
	return args.String(0), args.Error(1)
}

func (m *mockStorageClient) Delete(ctx context.Context, storagePath string) error {
	args := m.Called(ctx, storagePath)
	return args.Error(0)
}

// newServerWithMockDocumentService creates a JWT-capable server whose
// documentService is backed by the supplied MockQuerier.
func newServerWithMockDocumentService(mq *storemock.MockQuerier) *Server {
	s := newTestServerWithJWT()
	s.documentService = service.NewDocumentService(mq, s.log)
	return s
}

// newTestStorageClient creates a storage.Client pointing to a non-existent
// MinIO endpoint. minio-go does not connect at construction time, so the
// client is safe to use in tests that exercise logic executed before any
// actual S3 operation (e.g. form parsing, field validation).
func newTestStorageClient(t *testing.T) *storage.Client {
	t.Helper()
	cfg := &config.Config{
		S3Endpoint:  "localhost:19999",
		S3AccessKey: "minioadmin",
		S3SecretKey: "minioadmin",
		S3Bucket:    "tenders",
		S3Region:    "us-east-1",
	}
	sc, err := storage.New(cfg)
	require.NoError(t, err)
	return sc
}

// emptyMultipartRequest builds a multipart/form-data request with no parts.
func emptyMultipartRequest(t *testing.T, url, accessToken string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	require.NoError(t, mw.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if accessToken != "" {
		req.AddCookie(&http.Cookie{Name: "access_token", Value: accessToken})
	}
	return req
}

// multipartRequestWithFile builds a multipart/form-data POST request that
// includes a "file" field with the given filename and body content.
func multipartRequestWithFile(t *testing.T, url, accessToken, filename, content string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = io.WriteString(part, content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, url, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if accessToken != "" {
		req.AddCookie(&http.Cookie{Name: "access_token", Value: accessToken})
	}
	return req
}

// sampleDocument returns a minimal repository.Document for use in mock setup.
func sampleDocument(id, orgID uuid.UUID) repository.Document {
	return repository.Document{
		ID:             id,
		OrganizationID: orgID,
		UploadedBy:     uuid.New(),
		FileName:       "tender.pdf",
		StoragePath:    "tenders/some-uuid.pdf",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

// ── POST /api/v1/documents ────────────────────────────────────────────────────

func TestCreateDocument_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID := uuid.New()
	orgID := uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("CreateDocument", mock.Anything, mock.MatchedBy(func(p repository.CreateDocumentParams) bool {
		return p.FileName == "tender.pdf" && p.StoragePath == "tenders/abc.pdf"
	})).Return(sampleDocument(docID, orgID), nil)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := `{"file_name":"tender.pdf","storage_path":"tenders/abc.pdf"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/documents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, docID.String(), got["id"])
	mq.AssertExpectations(t)
}

func TestCreateDocument_MissingFields_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// file_name and storage_path are required — empty body should fail binding.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/documents", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateDocument_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	body := `{"file_name":"f.pdf","storage_path":"tenders/f.pdf"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/documents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateDocument_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("CreateDocument", mock.Anything, mock.Anything).
		Return(repository.Document{}, errors.New("db error"))

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	body := `{"file_name":"f.pdf","storage_path":"tenders/f.pdf"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/documents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}

// ── GET /api/v1/documents ─────────────────────────────────────────────────────

func TestListDocuments_ByOrg_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("ListDocumentsByOrganization", mock.Anything, orgID).
		Return([]repository.Document{sampleDocument(uuid.New(), orgID)}, nil)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 1)
	mq.AssertExpectations(t)
}

func TestListDocuments_InvalidSiteID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()
	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents?site_id=not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListDocuments_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents", nil)

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── GET /api/v1/documents/:id ─────────────────────────────────────────────────

func TestGetDocument_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, repository.GetDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(sampleDocument(docID, orgID), nil)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents/"+docID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, docID.String(), got["id"])
	mq.AssertExpectations(t)
}

func TestGetDocument_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("GetDocument", mock.Anything, mock.Anything).
		Return(repository.Document{}, pgx.ErrNoRows)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents/"+docID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, string(errs.CodeNotFound), errObj["code"])
	mq.AssertExpectations(t)
}

func TestGetDocument_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()
	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents/not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetDocument_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/documents/"+uuid.New().String(), nil)

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── DELETE /api/v1/documents/:id ─────────────────────────────────────────────

func TestDeleteDocument_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("DeleteDocument", mock.Anything, repository.DeleteDocumentParams{
		ID:             docID,
		OrganizationID: orgID,
	}).Return(int64(1), nil)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/documents/"+docID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	mq.AssertExpectations(t)
}

func TestDeleteDocument_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID, orgID := uuid.New(), uuid.New()

	mq := new(storemock.MockQuerier)
	mq.On("DeleteDocument", mock.Anything, mock.Anything).Return(int64(0), nil)

	s := newServerWithMockDocumentService(mq)
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/documents/"+docID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestDeleteDocument_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()
	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/documents/bad-id", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteDocument_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newServerWithMockDocumentService(new(storemock.MockQuerier))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/documents/"+uuid.New().String(), nil)

	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── POST /api/v1/documents/upload ────────────────────────────────────────────

// TestUploadDocument_NoAuth verifies the route is protected: a request without
// a valid JWT cookie must be rejected by AuthMiddleware with 401.
func TestUploadDocument_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newTestServerWithJWT()

	req := emptyMultipartRequest(t, "/api/v1/documents/upload", "")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestUploadDocument_StorageNotConfigured verifies that when storageClient is
// nil (no S3 credentials configured), the handler returns 500 immediately,
// before attempting any S3 or DB operation.
func TestUploadDocument_StorageNotConfigured_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// newTestServerWithJWT leaves storageClient nil (S3AccessKey is empty).
	s := newTestServerWithJWT()
	assert.Nil(t, s.storageClient, "pre-condition: storageClient must be nil")

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := emptyMultipartRequest(t, "/api/v1/documents/upload", access)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestUploadDocument_MissingFile verifies that a multipart request without a
// "file" part is rejected with 400 before any S3 or DB operation is attempted.
func TestUploadDocument_MissingFile_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := newTestServerWithJWT()
	s.storageClient = newTestStorageClient(t)

	userID := uuid.New()
	orgID := uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// Multipart body has no "file" field.
	req := emptyMultipartRequest(t, "/api/v1/documents/upload", access)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadDocument_HappyPath_Returns201 verifies the full success flow:
// storage.Upload is called, documentService.Create succeeds, response is 201.
func TestUploadDocument_HappyPath_Returns201(t *testing.T) {
	gin.SetMode(gin.TestMode)

	docID := uuid.New()
	userID, orgID := uuid.New(), uuid.New()
	const storagePath = "tenders/some-uuid.pdf"

	mq := new(storemock.MockQuerier)
	mq.On("CreateDocument", mock.Anything, mock.Anything).
		Return(sampleDocument(docID, orgID), nil)

	msc := new(mockStorageClient)
	msc.On("Upload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(storagePath, nil)

	s := newTestServerWithJWT()
	s.storageClient = msc
	s.documentService = service.NewDocumentService(mq, s.log)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := multipartRequestWithFile(t, "/api/v1/documents/upload", access, "tender.pdf", "pdf-content")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, docID.String(), got["id"])
	mq.AssertExpectations(t)
	msc.AssertExpectations(t)
}

// TestUploadDocument_DBError_CleansUpS3Object verifies that when
// documentService.Create fails after a successful upload, the handler calls
// storageClient.Delete to remove the orphaned S3 object, then returns 500.
func TestUploadDocument_DBError_CleansUpS3Object(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userID, orgID := uuid.New(), uuid.New()
	const storagePath = "tenders/some-uuid.pdf"

	mq := new(storemock.MockQuerier)
	mq.On("CreateDocument", mock.Anything, mock.Anything).
		Return(repository.Document{}, errors.New("db error"))

	msc := new(mockStorageClient)
	msc.On("Upload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(storagePath, nil)
	msc.On("Delete", mock.Anything, storagePath).Return(nil)

	s := newTestServerWithJWT()
	s.storageClient = msc
	s.documentService = service.NewDocumentService(mq, s.log)

	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	req := multipartRequestWithFile(t, "/api/v1/documents/upload", access, "tender.pdf", "pdf-content")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
	// Delete must have been called to clean up the orphaned object.
	msc.AssertExpectations(t)
}

// TestUploadDocument_InvalidFileName_Returns400 verifies that a filename that
// normalises to "." (empty path component) is rejected before any S3 call.
func TestUploadDocument_InvalidFileName_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	msc := new(mockStorageClient) // Upload must NOT be called.

	s := newTestServerWithJWT()
	s.storageClient = msc

	userID, orgID := uuid.New(), uuid.New()
	access, _, err := s.authService.GenerateTokens(userID, orgID, "admin")
	require.NoError(t, err)

	// Empty filename: filepath.Base("") == ".", which is rejected.
	req := multipartRequestWithFile(t, "/api/v1/documents/upload", access, "", "content")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	msc.AssertNotCalled(t, "Upload")
}
