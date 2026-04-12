package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware is a placeholder for JWT / session-based authentication.
func (s *Server) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		// TODO: validate JWT, extract user claims, set into context.
		c.Next()
	}
}

// ServiceBearerAuth is a placeholder for service-to-service bearer token validation.
func (s *Server) ServiceBearerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing service token"})
			return
		}

		// TODO: validate static service bearer token from config.
		c.Next()
	}
}
