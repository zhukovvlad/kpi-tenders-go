package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	storemock "go-kpi-tenders/internal/store/mock"
)

func extractionRequestURL(id uuid.UUID) string {
	return "/api/v1/extraction-requests/" + id.String()
}

// ── GET /api/v1/extraction-requests/:id ───────────────────────────────────────

func TestGetExtractionRequest_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionRequestURL(uuid.New()), nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "GetExtractionRequest")
}

func TestGetExtractionRequest_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockExtractionService(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), uuid.New(), "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/extraction-requests/not-a-uuid", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	mq.AssertNotCalled(t, "GetExtractionRequest")
}

func TestGetExtractionRequest_NotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetExtractionRequest", mock.Anything, mock.Anything).
		Return(repository.ExtractionRequest{}, pgx.ErrNoRows)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionRequestURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	mq.AssertExpectations(t)
}

func TestGetExtractionRequest_DBError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)

	mq.On("GetExtractionRequest", mock.Anything, mock.Anything).
		Return(repository.ExtractionRequest{}, pgx.ErrTxClosed)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionRequestURL(uuid.New()), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	mq.AssertExpectations(t)
}

// Success: no resolved_schema → GetAnswers returns empty without DB call.
func TestGetExtractionRequest_Success_PendingStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	reqID := uuid.New()
	mq := new(storemock.MockQuerier)

	questions, _ := json.Marshal([]string{"Какова стоимость?", "Срок действия?"})
	row := repository.ExtractionRequest{
		ID:             reqID,
		DocumentID:     uuid.New(),
		OrganizationID: orgID,
		Questions:      questions,
		Anonymize:      true,
		Status:         "pending",
		ResolvedSchema: nil,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	mq.On("GetExtractionRequest", mock.Anything, repository.GetExtractionRequestParams{
		ID:             reqID,
		OrganizationID: orgID,
	}).Return(row, nil)

	s := newServerWithMockExtractionService(t, mq)
	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		extractionRequestURL(reqID), nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "id")
	assert.Contains(t, resp, "status")
	assert.Contains(t, resp, "questions")

	// GetAnswers must NOT call ListExtractedDataForKeys when schema is empty.
	mq.AssertNotCalled(t, "ListExtractedDataForKeys")
	mq.AssertExpectations(t)
}
