package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/pkg/errs"
)

// parseSiteAuditPagination parses ?limit and ?offset from the request query.
// Default: limit=50, max=200, offset=0. Returns a non-nil error on invalid input.
func parseSiteAuditPagination(c *gin.Context) (limit, offset int32, appErr error) {
	limit = 50
	offset = 0
	if l := c.Query("limit"); l != "" {
		v, err := strconv.ParseInt(l, 10, 32)
		if err != nil || v < 1 || v > 200 {
			return 0, 0, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 200", nil)
		}
		limit = int32(v)
	}
	if o := c.Query("offset"); o != "" {
		v, err := strconv.ParseInt(o, 10, 32)
		if err != nil || v < 0 {
			return 0, 0, errs.New(errs.CodeValidationFailed, "offset must be non-negative", nil)
		}
		offset = int32(v)
	}
	return limit, offset, nil
}

func (s *Server) ListSiteAuditLog(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	siteID, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid site id", err))
		return
	}

	// Verify site belongs to org before returning log.
	if _, err := s.constructionSiteService.Get(c.Request.Context(), siteID, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	limit, offset, paginationErr := parseSiteAuditPagination(c)
	if paginationErr != nil {
		s.respondWithError(c, paginationErr)
		return
	}

	events, err := s.siteAuditService.ListBySite(c.Request.Context(), siteID, orgID, limit, offset)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, events)
}

func (s *Server) ListSiteEvents(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	siteID, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid site id", err))
		return
	}

	// Verify site belongs to org before returning events.
	if _, err := s.constructionSiteService.Get(c.Request.Context(), siteID, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	limit, offset, paginationErr := parseSiteAuditPagination(c)
	if paginationErr != nil {
		s.respondWithError(c, paginationErr)
		return
	}

	events, err := s.siteAuditService.ListEventsBySite(c.Request.Context(), siteID, orgID, limit, offset)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, events)
}
