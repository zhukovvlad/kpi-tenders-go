package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/pkg/errs"
)

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

	limit := int32(50)
	offset := int32(0)
	if l := c.Query("limit"); l != "" {
		v, err := strconv.ParseInt(l, 10, 32)
		if err != nil || v < 1 || v > 200 {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "limit must be between 1 and 200", nil))
			return
		}
		limit = int32(v)
	}
	if o := c.Query("offset"); o != "" {
		v, err := strconv.ParseInt(o, 10, 32)
		if err != nil || v < 0 {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "offset must be non-negative", nil))
			return
		}
		offset = int32(v)
	}

	events, err := s.siteAuditService.ListBySite(c.Request.Context(), siteID, orgID, limit, offset)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, events)
}
