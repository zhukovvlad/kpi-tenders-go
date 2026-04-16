package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/service"
)

type registerRequest struct {
	OrgName  string `json:"org_name"  binding:"required"`
	INN      string `json:"inn"`
	Email    string `json:"email"     binding:"required,email"`
	Password string `json:"password"  binding:"required,min=8"`
	FullName string `json:"full_name" binding:"required"`
}

// RegisterOrganization handles POST /api/v1/auth/register.
// Creates an organization and its first admin user, then issues auth cookies.
func (s *Server) RegisterOrganization(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	org, user, err := s.organizationService.Register(c.Request.Context(), service.RegisterParams{
		OrgName:  req.OrgName,
		INN:      req.INN,
		Email:    req.Email,
		Password: req.Password,
		FullName: req.FullName,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidINN):
			c.JSON(http.StatusBadRequest, gin.H{"error": "INN must be exactly 10 digits"})
		case errors.Is(err, service.ErrEmailTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
		case errors.Is(err, service.ErrINNTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "INN already in use"})
		default:
			s.log.Error("register: failed", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}

	accessToken, refreshToken, err := s.authService.GenerateTokens(user.ID, org.ID, user.Role)
	if err != nil {
		// Org and user are already persisted — returning 500 here would make
		// clients retry registration and hit uniqueness conflicts. Return 201
		// and ask the client to sign in manually.
		s.log.Error("register: token generation failed", "err", err)
		c.JSON(http.StatusCreated, gin.H{
			"org_id":  org.ID,
			"user_id": user.ID,
			"warning": "registered successfully, please sign in",
		})
		return
	}

	s.setAuthCookies(c, accessToken, refreshToken)
	c.JSON(http.StatusCreated, gin.H{
		"org_id":  org.ID,
		"user_id": user.ID,
	})
}

// GetOrganization handles GET /api/v1/organizations/:id.
// Users can only fetch their own organization.
func (s *Server) GetOrganization(c *gin.Context) {
	orgID, ok := orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if id != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	org, err := s.organizationService.GetByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrOrgNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}
		s.log.Error("get org: failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, org)
}

type updateOrganizationRequest struct {
	Name string  `json:"name" binding:"required"`
	INN  *string `json:"inn"`
}

// UpdateOrganization handles PATCH /api/v1/organizations/:id.
// Only admin users can update their own organization.
func (s *Server) UpdateOrganization(c *gin.Context) {
	orgID, ok := orgIDFromContext(c)
	if !ok {
		return
	}

	if role, _ := c.Get("role"); role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin role required"})
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if id != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	var req updateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	org, err := s.organizationService.Update(c.Request.Context(), id, req.Name, req.INN)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidINN):
			c.JSON(http.StatusBadRequest, gin.H{"error": "INN must be exactly 10 digits"})
		case errors.Is(err, service.ErrOrgNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		case errors.Is(err, service.ErrINNTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "INN already in use"})
		default:
			s.log.Error("update org: failed", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
		return
	}

	c.JSON(http.StatusOK, org)
}

// DeleteOrganization handles DELETE /api/v1/organizations/:id.
// Only admin users can delete their own organization (cascades to all data).
func (s *Server) DeleteOrganization(c *gin.Context) {
	orgID, ok := orgIDFromContext(c)
	if !ok {
		return
	}

	if role, _ := c.Get("role"); role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin role required"})
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if id != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	if err := s.organizationService.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, service.ErrOrgNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
			return
		}
		s.log.Error("delete org: failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	s.clearAuthCookies(c)
	c.Status(http.StatusNoContent)
}

// orgIDFromContext extracts the orgID set by AuthMiddleware, responding with
// 401 and returning false if it is missing.
func orgIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	val, exists := c.Get("orgID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.UUID{}, false
	}
	id, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return uuid.UUID{}, false
	}
	return id, true
}
