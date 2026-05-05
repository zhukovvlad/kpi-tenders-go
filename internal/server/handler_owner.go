package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

// OwnerListOrganizationUsers handles GET /api/v1/organizations/:id/users.
// Owner-only: returns all users belonging to the specified organization.
func (s *Server) OwnerListOrganizationUsers(c *gin.Context) {
	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid organization id", err))
		return
	}

	users, svcErr := s.userService.List(c.Request.Context(), orgID)
	if svcErr != nil {
		s.respondWithError(c, svcErr)
		return
	}

	c.JSON(http.StatusOK, users)
}

type ownerUpdateUserRequest struct {
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// OwnerUpdateOrganizationUser handles PATCH /api/v1/organizations/:id/users/:user_id.
// Owner-only: updates role or active status of any user in the specified org.
func (s *Server) OwnerUpdateOrganizationUser(c *gin.Context) {
	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid organization id", err))
		return
	}

	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid user id", err))
		return
	}

	var req ownerUpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	if req.Role == nil && req.IsActive == nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "nothing to update", nil))
		return
	}

	updated, svcErr := s.userService.Update(c.Request.Context(), service.UpdateUserParams{
		UserID: userID,
		OrgID:  orgID,
		Role:   req.Role,
		Active: req.IsActive,
	})
	if svcErr != nil {
		s.respondWithError(c, svcErr)
		return
	}

	c.JSON(http.StatusOK, updated)
}

// OwnerDeactivateOrganizationUser handles DELETE /api/v1/organizations/:id/users/:user_id.
// Owner-only: deactivates (soft-deletes) any user in the specified organization.
func (s *Server) OwnerDeactivateOrganizationUser(c *gin.Context) {
	orgID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid organization id", err))
		return
	}

	userID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid user id", err))
		return
	}

	if _, svcErr := s.userService.Deactivate(c.Request.Context(), userID, orgID); svcErr != nil {
		s.respondWithError(c, svcErr)
		return
	}

	c.Status(http.StatusNoContent)
}
