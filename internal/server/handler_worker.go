package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-kpi-tenders/internal/service"
	"go-kpi-tenders/pkg/errs"
)

// WorkerUpdateTaskStatus handles PATCH /internal/worker/tasks/:id/status.
// Protected by ServiceBearerAuth middleware — callers must supply a valid
// SERVICE_TOKEN.
func (s *Server) WorkerUpdateTaskStatus(c *gin.Context) {
	if s.workerService == nil {
		s.respondWithError(c, errs.New(errs.CodeInternalError, "worker service not configured", nil))
		return
	}

	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid task id", err))
		return
	}

	var req service.WorkerStatusUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid request body", err))
		return
	}

	// "pending" is intentionally excluded: Go sets that status when creating a task.
	// Workers only report transitions they drive (processing, completed, failed).
	validStatuses := map[string]bool{"processing": true, "completed": true, "failed": true}
	if !validStatuses[req.Status] {
		s.respondWithError(c, errs.New(errs.CodeValidationFailed, "invalid status", nil))
		return
	}

	task, err := s.workerService.HandleStatusUpdate(c.Request.Context(), taskID, req)
	if err != nil {
		s.respondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, task)
}
