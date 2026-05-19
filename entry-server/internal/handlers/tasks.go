package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

type TaskHandler struct {
	jobs   *service.JobManager
	logger *logger.Logger
}

func NewTaskHandler(jobs *service.JobManager, appLogger *logger.Logger) *TaskHandler {
	return &TaskHandler{jobs: jobs, logger: appLogger}
}

func (h *TaskHandler) Accepted(c *gin.Context) {
	taskID := c.Param("task_id")

	var req protocol.TaskAcceptedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("task accepted bad request", "event", "task_accepted_bad_request", "task_id", taskID, "error", err)
		c.JSON(http.StatusBadRequest, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusFailed})
		return
	}

	if err := h.jobs.AcceptSegmentTask(taskID, req); err != nil {
		h.respondTaskError(c, "task_accepted_failed", taskID, req.WorkerID, err)
		return
	}

	c.JSON(http.StatusOK, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusSuccess})
}

func (h *TaskHandler) Completed(c *gin.Context) {
	taskID := c.Param("task_id")

	var req protocol.TaskCompletedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("task completed bad request", "event", "task_completed_bad_request", "task_id", taskID, "error", err)
		c.JSON(http.StatusBadRequest, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusFailed})
		return
	}

	if err := h.jobs.CompleteSegmentTask(taskID, req); err != nil {
		h.respondTaskError(c, "task_completed_failed", taskID, req.WorkerID, err)
		return
	}

	c.JSON(http.StatusOK, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusSuccess})
}

func (h *TaskHandler) Failed(c *gin.Context) {
	taskID := c.Param("task_id")

	var req protocol.TaskFailedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("task failed bad request", "event", "task_failed_bad_request", "task_id", taskID, "error", err)
		c.JSON(http.StatusBadRequest, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusFailed})
		return
	}

	if err := h.jobs.FailSegmentTask(taskID, req); err != nil {
		h.respondTaskError(c, "task_failed_failed", taskID, req.WorkerID, err)
		return
	}

	c.JSON(http.StatusOK, protocol.TaskUpdateResponse{Status: protocol.TaskUpdateStatusSuccess})
}

func (h *TaskHandler) respondTaskError(c *gin.Context, event string, taskID string, workerID string, err error) {
	status := protocol.TaskUpdateStatusFailed
	code := http.StatusBadRequest

	switch {
	case errors.Is(err, service.ErrTaskNotFound):
		status = protocol.TaskUpdateStatusNotFound
		code = http.StatusNotFound
	case errors.Is(err, service.ErrTaskWorkerMismatch):
		status = protocol.TaskUpdateStatusWorkerMismatch
		code = http.StatusConflict
	case errors.Is(err, service.ErrTaskInvalidState):
		status = protocol.TaskUpdateStatusInvalidState
		code = http.StatusConflict
	}

	h.logger.Warn("task update failed",
		"event", event,
		"task_id", taskID,
		"worker_id", workerID,
		"status", string(status),
		"error", err,
	)

	c.JSON(code, protocol.TaskUpdateResponse{Status: status})
}
