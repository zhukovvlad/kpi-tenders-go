package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthCheck returns 200 — used for liveness probes.
func (s *Server) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
