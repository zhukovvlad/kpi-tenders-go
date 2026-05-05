package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/pkg/errs"
)

type createInvitationRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role"`
}

func (s *Server) ListInvitations(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	invitations, err := s.invitationService.ListByOrg(c.Request.Context(), orgID)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, invitations)
}

func (s *Server) CreateInvitation(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}
	userID, ok := s.userIDFromContext(c)
	if !ok {
		return
	}

	var req createInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request", err))
		return
	}

	role := req.Role
	if role == "" {
		role = "member"
	}
	switch role {
	case "admin", "member":
	default:
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid role: must be admin or member", nil))
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "failed to generate token", err))
		return
	}
	rawToken := hex.EncodeToString(tokenBytes)
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().UTC().Add(72 * time.Hour)

	inv, err := s.invitationService.Create(c.Request.Context(), orgID, req.Email, role, userID, tokenHash, expiresAt)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	// In local development the raw token is returned in the response body so
	// that the API can be exercised without an email provider. In all other
	// environments the token must be delivered out-of-band (e.g. via email) and
	// is never included in the API response.
	if s.cfg.AppEnv == "local" {
		c.JSON(http.StatusCreated, gin.H{
			"invitation": inv,
			"token":      rawToken,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"invitation": inv,
	})
}

func (s *Server) DeleteInvitation(c *gin.Context) {
	orgID, ok := s.orgIDFromContext(c)
	if !ok {
		return
	}

	id, err := parseUUID(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid id", err))
		return
	}

	if err := s.invitationService.Delete(c.Request.Context(), id, orgID); err != nil {
		s.respondWithError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
