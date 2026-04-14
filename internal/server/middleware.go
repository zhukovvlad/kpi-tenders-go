package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware validates the access_token HttpOnly cookie and populates
// userID, orgID, and role into the Gin context for downstream handlers.
func (s *Server) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, err := c.Cookie("access_token")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing access token"})
			return
		}

		claims, err := s.authService.ValidateAccessToken(tokenStr)
		if err != nil {
			s.log.Warn("auth middleware: invalid token", slog.String("err", err.Error()))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("userID", claims.UserID)
		c.Set("orgID", claims.OrgID)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// ServiceBearerAuth validates a static bearer token for service-to-service
// requests. Uses constant-time comparison to prevent timing attacks.
func (s *Server) ServiceBearerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed service token"})
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.ServiceToken)) != 1 {
			s.log.Warn("service auth: invalid token attempt",
				slog.String("remote_addr", c.ClientIP()),
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid service token"})
			return
		}

		c.Next()
	}
}
