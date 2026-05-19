package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

type JobResultHandler struct {
	jobs   *service.JobManager
	logger *logger.Logger
}

func NewJobResultHandler(jobs *service.JobManager, appLogger *logger.Logger) *JobResultHandler {
	return &JobResultHandler{jobs: jobs, logger: appLogger}
}

func (h *JobResultHandler) Assembled(c *gin.Context) {
	jobID := c.Param("job_id")

	var req protocol.JobAssembledRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("job assembled bad request", "event", "job_assembled_bad_request", "job_id", jobID, "error", err)
		c.JSON(http.StatusBadRequest, protocol.JobAssembledResponse{Status: protocol.JobResultUpdateStatusFailed})
		return
	}

	if err := h.jobs.AssembleJob(jobID, req); err != nil {
		h.respondJobResultError(c, jobID, req.WorkerID, err)
		return
	}

	c.JSON(http.StatusOK, protocol.JobAssembledResponse{Status: protocol.JobResultUpdateStatusSuccess})
}

func (h *JobResultHandler) respondJobResultError(c *gin.Context, jobID string, workerID string, err error) {
	status := protocol.JobResultUpdateStatusFailed
	code := http.StatusBadRequest

	switch {
	case errors.Is(err, service.ErrTaskNotFound):
		status = protocol.JobResultUpdateStatusNotFound
		code = http.StatusNotFound
	case errors.Is(err, service.ErrTaskWorkerMismatch):
		status = protocol.JobResultUpdateStatusWorkerMismatch
		code = http.StatusConflict
	case errors.Is(err, service.ErrTaskInvalidState):
		status = protocol.JobResultUpdateStatusInvalidState
		code = http.StatusConflict
	}

	h.logger.Warn("job result update failed",
		"event", "job_result_update_failed",
		"job_id", jobID,
		"worker_id", workerID,
		"status", string(status),
		"error", err,
	)

	c.JSON(code, protocol.JobAssembledResponse{Status: status})
}
