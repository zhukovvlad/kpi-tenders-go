package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

type createConstructionSiteRequest struct {
	ParentID *string `json:"parent_id"`
	Name     string  `json:"name"   binding:"required"`
	Status   string  `json:"status"`
}

func (s *Server) CreateConstructionSite(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	userID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	var req createConstructionSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	status := req.Status
	if status == "" {
		status = "active"
	}
	switch status {
	case "active", "completed", "archived":
	default:
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid status: must be active, completed or archived", nil))
		return
	}

	params := repository.CreateConstructionSiteParams{
		OrganizationID: orgID,
		Name:           req.Name,
		Status:         status,
		CreatedBy:      pgtype.UUID{Bytes: userID, Valid: true},
	}

	if req.ParentID != nil {
		parentID, err := uuid.Parse(*req.ParentID)
		if err != nil {
			s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid parent_id", err))
			return
		}
		// Verify the parent site belongs to the same organization.
		if _, err := s.constructionSiteService.Get(c.Request.Context(), parentID, orgID); err != nil {
			s.respondWithError(c, err)
			return
		}
		params.ParentID = pgtype.UUID{Bytes: parentID, Valid: true}
	}

	site, err := s.constructionSiteService.Create(c.Request.Context(), params)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, site)
}

func (s *Server) ListConstructionSites(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	sites, err := s.constructionSiteService.ListByOrganization(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, sites)
}

func (s *Server) GetConstructionSite(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	site, err := s.constructionSiteService.Get(c.Request.Context(), id, orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, site)
}

type updateConstructionSiteRequest struct {
	Name   string `json:"name"   binding:"required"`
	Status string `json:"status" binding:"required"`
}

func (s *Server) UpdateConstructionSite(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	var req updateConstructionSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	switch req.Status {
	case "active", "completed", "archived":
	default:
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid status: must be active, completed or archived", nil))
		return
	}

	site, err := s.constructionSiteService.Update(c.Request.Context(), repository.UpdateConstructionSiteParams{
		ID:             id,
		OrganizationID: orgID,
		Name:           req.Name,
		Status:         req.Status,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, site)
}

func (s *Server) DeleteConstructionSite(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.constructionSiteService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
