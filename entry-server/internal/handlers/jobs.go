package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

type JobHandler struct {
	jobs   *service.JobManager
	logger *logger.Logger
}

func NewJobHandler(jobs *service.JobManager, appLogger *logger.Logger) *JobHandler {
	return &JobHandler{jobs: jobs, logger: appLogger}
}

func (h *JobHandler) Create(c *gin.Context) {
	var req protocol.CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("job create bad request", "event", "job_create_bad_request", "error", err)
		c.JSON(http.StatusBadRequest, protocol.CreateJobResponse{
			Status: protocol.CreateJobStatusFailed,
		})
		return
	}

	result, err := h.jobs.CreateJob(req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrWorkerNotFound):
			h.logger.Warn("job create source worker not found", "event", "job_create_worker_not_found", "worker_id", req.WorkerID)
			c.JSON(http.StatusNotFound, protocol.CreateJobResponse{
				Status:        protocol.CreateJobStatusNotFound,
				RequiredSlots: req.SegmentCount,
			})
		case errors.Is(err, service.ErrInsufficientResources):
			h.logger.Warn("job create insufficient resources",
				"event", "job_create_insufficient_resources",
				"worker_id", req.WorkerID,
				"required_slots", result.RequiredSlots,
				"available_slots", result.AvailableSlots,
			)
			c.JSON(http.StatusOK, protocol.CreateJobResponse{
				Status:         protocol.CreateJobStatusInsufficientResources,
				RequiredSlots:  result.RequiredSlots,
				AvailableSlots: result.AvailableSlots,
				Assignments:    result.Assignments,
			})
		default:
			h.logger.Error("job create failed", "event", "job_create_failed", "worker_id", req.WorkerID, "error", err)
			c.JSON(http.StatusInternalServerError, protocol.CreateJobResponse{
				Status:        protocol.CreateJobStatusFailed,
				RequiredSlots: req.SegmentCount,
			})
		}
		return
	}

	status := protocol.CreateJobStatusSuccess
	if result.AlreadyExists {
		status = protocol.CreateJobStatusAlreadyExists
	}

	c.JSON(http.StatusOK, protocol.CreateJobResponse{
		Status:         status,
		JobID:          result.JobID,
		JobStatus:      result.JobStatus,
		AlreadyExists:  result.AlreadyExists,
		RequiredSlots:  result.RequiredSlots,
		AvailableSlots: result.AvailableSlots,
		Assignments:    result.Assignments,
	})
}

func (h *JobHandler) Result(c *gin.Context) {
	address := c.Query("address")
	videoName := c.Query("video_name")
	if address == "" || videoName == "" {
		h.logger.Warn("job result bad request", "event", "job_result_bad_request", "address", address, "video_name", videoName)
		c.JSON(http.StatusBadRequest, protocol.QueryJobResultResponse{
			Status: protocol.QueryJobResultStatusFailed,
		})
		return
	}

	response, ok := h.jobs.GetJobResultByIdentity(address, videoName)
	if !ok {
		c.JSON(http.StatusNotFound, response)
		return
	}

	c.JSON(http.StatusOK, response)
}
