package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

type createUserRequest struct {
	Email    string `json:"email"     binding:"required,email"`
	Password string `json:"password"  binding:"required,min=8"`
	FullName string `json:"full_name" binding:"required"`
	Role     string `json:"role"      binding:"required,oneof=admin member"`
}

// CreateUser handles POST /api/v1/users.
// Admin-only: creates a new user in the caller's organization.
func (s *Server) CreateUser(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	user, err := s.userService.Create(c.Request.Context(), service.CreateUserParams{
		OrgID:    orgID,
		Email:    req.Email,
		Password: req.Password,
		FullName: req.FullName,
		Role:     req.Role,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":              user.ID,
		"organization_id": user.OrganizationID,
		"email":           user.Email,
		"full_name":       user.FullName,
		"role":            user.Role,
		"is_active":       user.IsActive,
		"created_at":      user.CreatedAt,
	})
}

// ListUsers handles GET /api/v1/users.
// Admin-only: returns all users in the caller's organization.
func (s *Server) ListUsers(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	users, err := s.userService.List(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, users)
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// UpdateUser handles PATCH /api/v1/users/:user_id.
// Admin-only: updates role or active status of a user within the same org.
func (s *Server) UpdateUser(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	callerID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid user id", err))
		return
	}

	if userID == callerID {
		s.respondWithError(c, errs.New(errs.CodeForbidden, "cannot modify your own account", nil))
		return
	}

	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	if req.Role == nil && req.IsActive == nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "nothing to update", nil))
		return
	}

	updated, err := s.userService.Update(c.Request.Context(), service.UpdateUserParams{
		UserID: userID,
		OrgID:  orgID,
		Role:   req.Role,
		Active: req.IsActive,
	})
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, updated)
}

// DeactivateUser handles DELETE /api/v1/users/:user_id.
// Admin-only: sets is_active=false (soft delete).
func (s *Server) DeactivateUser(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	callerID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid user id", err))
		return
	}

	if userID == callerID {
		s.respondWithError(c, errs.New(errs.CodeForbidden, "cannot deactivate your own account", nil))
		return
	}

	if _, err := s.userService.Deactivate(c.Request.Context(), userID, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "user deactivated"})
}
