package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

type loginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// Login handles POST /api/v1/auth/login.
// On success it issues access_token and refresh_token as HttpOnly cookies.
func (s *Server) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		s.log.Debug("login: invalid request payload", "err", err)
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request payload", err))
		return
	}

	accessToken, refreshToken, err := s.authService.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	s.setAuthCookies(c, accessToken, refreshToken)
	c.JSON(http.StatusOK, gin.H{"message": "logged in"})
}

// RefreshTokens handles POST /api/v1/auth/refresh.
// It validates the refresh_token cookie, fetches the current user record,
// and issues a fresh token pair.
func (s *Server) RefreshTokens(c *gin.Context) {
	refreshToken, err := c.Cookie("refresh_token")
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeUnauthorized, "missing refresh token", err))
		return
	}

	claims, err := s.authService.ValidateRefreshToken(refreshToken)
	if err != nil {
		s.clearAuthCookies(c)
		s.respondWithError(c, err)
		return
	}

	// Re-fetch the user to pick up any role / org changes since the token was issued.
	user, err := s.authService.ResolveUserForRefresh(c.Request.Context(), claims.UserID)
	if err != nil {
		s.clearAuthCookies(c)
		s.respondWithError(c, err)
		return
	}

	newAccess, newRefresh, err := s.authService.GenerateTokens(user.ID, user.OrganizationID, user.Role)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	s.setAuthCookies(c, newAccess, newRefresh)
	c.JSON(http.StatusOK, gin.H{"message": "tokens refreshed"})
}

// Logout handles POST /api/v1/auth/logout.
// It clears both auth cookies by setting them to expired.
func (s *Server) Logout(c *gin.Context) {
	s.clearAuthCookies(c)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// setAuthCookies writes access_token and refresh_token as HttpOnly cookies.
// The refresh_token path is scoped to /api/v1/auth to minimise exposure.
func (s *Server) setAuthCookies(c *gin.Context, accessToken, refreshToken string) {
	secure := s.cfg.AppEnv != "local"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		"access_token", accessToken,
		int(service.AccessTokenTTL.Seconds()),
		"/", "", secure, true,
	)
	c.SetCookie(
		"refresh_token", refreshToken,
		int(service.RefreshTokenTTL.Seconds()),
		"/api/v1/auth", "", secure, true,
	)
}

// clearAuthCookies expires both auth cookies immediately.
func (s *Server) clearAuthCookies(c *gin.Context) {
	secure := s.cfg.AppEnv != "local"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("access_token", "", -1, "/", "", secure, true)
	c.SetCookie("refresh_token", "", -1, "/api/v1/auth", "", secure, true)
}
