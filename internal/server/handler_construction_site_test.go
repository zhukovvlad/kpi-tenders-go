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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/internal/service"
	storemock "go-kpi-tenders/internal/store/mock"
)

// newServerWithMockSiteServices wires a server whose constructionSiteService
// and siteAuditService are backed by the given MockQuerier.
func newServerWithMockSiteServices(t *testing.T, mq *storemock.MockQuerier) *Server {
	t.Helper()
	s := newTestServerWithJWT(t)
	s.constructionSiteService = service.NewConstructionSiteService(mq, s.log)
	s.siteAuditService = service.NewSiteAuditService(mq, s.log)
	return s
}

func sampleSite(orgID uuid.UUID) repository.ConstructionSite {
	return repository.ConstructionSite{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           "Test Site",
		Status:         "active",
		LastActivityAt: time.Now(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
}

// ── GET /api/v1/sites/root ────────────────────────────────────────────────────

func TestListRootConstructionSites_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/sites/root", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	mq.AssertNotCalled(t, "ListRootConstructionSites")
}

func TestListRootConstructionSites_Empty_ReturnsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	mq.On("ListRootConstructionSites", mock.Anything, orgID).
		Return([]repository.ConstructionSite{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/sites/root", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, len(resp))
}

func TestListRootConstructionSites_Success_ReturnsSiteListItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	site := sampleSite(orgID)

	mq.On("ListRootConstructionSites", mock.Anything, orgID).
		Return([]repository.ConstructionSite{site}, nil)
	mq.On("ListSiteStatusesBySiteIds", mock.Anything,
		repository.ListSiteStatusesBySiteIdsParams{
			OrganizationID: orgID,
			SiteIds:        []uuid.UUID{site.ID},
		}).Return([]repository.VSiteStatus{{SiteID: site.ID, Status: "ready"}}, nil)
	mq.On("ListSiteExtractedCounts", mock.Anything, mock.Anything).
		Return([]repository.ListSiteExtractedCountsRow{}, nil)
	mq.On("ListSiteContractKinds", mock.Anything, mock.Anything).
		Return([]repository.ListSiteContractKindsRow{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/sites/root", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "ready", resp[0]["aggregate_status"])
	assert.Equal(t, float64(0), resp[0]["extracted_count"])
	assert.Nil(t, resp[0]["inflation_pct"])
	assert.NotNil(t, resp[0]["breadcrumbs"])
	assert.NotNil(t, resp[0]["contract_kinds"])
}

// ── GET /api/v1/sites/:id/children ───────────────────────────────────────────

func TestListConstructionSitesByParent_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+uuid.New().String()+"/children", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListConstructionSitesByParent_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/not-a-uuid/children", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListConstructionSitesByParent_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	parentID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	child := sampleSite(orgID)

	mq.On("GetSiteAncestors", mock.Anything, mock.Anything).
		Return([]string{"Root", "Parent"}, nil)
	mq.On("ListConstructionSitesByParent", mock.Anything, mock.Anything).
		Return([]repository.ConstructionSite{child}, nil)
	mq.On("ListSiteStatusesBySiteIds", mock.Anything,
		repository.ListSiteStatusesBySiteIdsParams{
			OrganizationID: orgID,
			SiteIds:        []uuid.UUID{child.ID},
		}).Return([]repository.VSiteStatus{{SiteID: child.ID, Status: "processing"}}, nil)
	mq.On("ListSiteExtractedCounts", mock.Anything, mock.Anything).
		Return([]repository.ListSiteExtractedCountsRow{{SiteID: pgtype.UUID{Bytes: child.ID, Valid: true}, ExtractedCount: 5}}, nil)
	mq.On("ListSiteContractKinds", mock.Anything, mock.Anything).
		Return([]repository.ListSiteContractKindsRow{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+parentID.String()+"/children", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "processing", resp[0]["aggregate_status"])
	assert.Equal(t, float64(5), resp[0]["extracted_count"])
	breadcrumbs, ok := resp[0]["breadcrumbs"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(breadcrumbs))
}

// ── GET /api/v1/sites/:id/events ─────────────────────────────────────────────

func TestListSiteEvents_NoAuth_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+uuid.New().String()+"/events", nil)
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListSiteEvents_InvalidID_Returns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/not-a-uuid/events", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListSiteEvents_SiteNotFound_Returns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	siteID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	mq.On("GetConstructionSite", mock.Anything, mock.Anything).
		Return(repository.ConstructionSite{}, pgx.ErrNoRows)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+siteID.String()+"/events", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListSiteEvents_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	siteID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	site := sampleSite(orgID)
	site.ID = siteID

	mq.On("GetConstructionSite", mock.Anything, mock.Anything).
		Return(site, nil)
	mq.On("ListSiteEventsBySite", mock.Anything, mock.Anything).
		Return([]repository.ListSiteEventsBySiteRow{
			{
				ID:        uuid.New(),
				SiteID:    siteID,
				EventType: "site_created",
				Payload:   json.RawMessage(`{}`),
				CreatedAt: time.Now(),
				ActorName: "Иван Иванов",
			},
		}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+siteID.String()+"/events", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "site_created", resp[0]["kind"])
	assert.Equal(t, "Иван Иванов", resp[0]["actor_name"])
	assert.Equal(t, "Объект строительства создан", resp[0]["message"])
	assert.NotEmpty(t, resp[0]["occurred_at"])
}

func TestListSiteEvents_Empty_ReturnsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orgID := uuid.New()
	siteID := uuid.New()
	mq := new(storemock.MockQuerier)
	s := newServerWithMockSiteServices(t, mq)

	access, _, err := s.authService.GenerateTokens(uuid.New(), orgID, "member")
	require.NoError(t, err)

	site := sampleSite(orgID)
	site.ID = siteID

	mq.On("GetConstructionSite", mock.Anything, mock.Anything).
		Return(site, nil)
	mq.On("ListSiteEventsBySite", mock.Anything, mock.Anything).
		Return([]repository.ListSiteEventsBySiteRow{}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/sites/"+siteID.String()+"/events", nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: access})
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []service.SiteEvent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, len(resp))
}
