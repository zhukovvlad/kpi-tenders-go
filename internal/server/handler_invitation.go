package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"go-kpi-tenders/internal/repository"
	"go-kpi-tenders/pkg/errs"
)

// invitationResponse is the API representation of a user invitation.
// token_hash is intentionally omitted — it is an internal implementation detail
// and must never be exposed over the API.
type invitationResponse struct {
	ID             uuid.UUID          `json:"id"`
	OrganizationID uuid.UUID          `json:"organization_id"`
	Email          string             `json:"email"`
	Role           string             `json:"role"`
	InvitedBy      pgtype.UUID        `json:"invited_by"`
	ExpiresAt      time.Time          `json:"expires_at"`
	AcceptedAt     pgtype.Timestamptz `json:"accepted_at"`
	CreatedAt      time.Time          `json:"created_at"`
}

func toInvitationResponse(inv repository.UserInvitation) invitationResponse {
	return invitationResponse{
		ID:             inv.ID,
		OrganizationID: inv.OrganizationID,
		Email:          inv.Email,
		Role:           inv.Role,
		InvitedBy:      inv.InvitedBy,
		ExpiresAt:      inv.ExpiresAt,
		AcceptedAt:     inv.AcceptedAt,
		CreatedAt:      inv.CreatedAt,
	}
}

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

	dtos := make([]invitationResponse, len(invitations))
	for i, inv := range invitations {
		dtos[i] = toInvitationResponse(inv)
	}
	c.JSON(http.StatusOK, dtos)
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
			"invitation": toInvitationResponse(inv),
			"token":      rawToken,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"invitation": toInvitationResponse(inv),
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
