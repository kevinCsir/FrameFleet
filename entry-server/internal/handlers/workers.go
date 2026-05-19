package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

type WorkerHandler struct {
	registry    *service.WorkerRegistry
	splitPolicy protocol.SplitPolicy
	logger      *logger.Logger
}

func NewWorkerHandler(registry *service.WorkerRegistry, splitPolicy protocol.SplitPolicy, appLogger *logger.Logger) *WorkerHandler {
	return &WorkerHandler{registry: registry, splitPolicy: splitPolicy, logger: appLogger}
}

func (h *WorkerHandler) Register(c *gin.Context) {
	var req protocol.RegisterWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("worker register bad request", "event", "worker_register_bad_request", "error", err)
		c.JSON(http.StatusBadRequest, protocol.RegisterWorkerResponse{
			Status: protocol.RegisterWorkerStatusFailed,
		})
		return
	}

	worker, existed, err := h.registry.RegisterWorker(req)
	if err != nil {
		h.logger.Warn("worker register failed", "event", "worker_register_failed", "address", req.Address, "error", err)
		c.JSON(http.StatusBadRequest, protocol.RegisterWorkerResponse{
			Status: protocol.RegisterWorkerStatusFailed,
		})
		return
	}

	status := protocol.RegisterWorkerStatusSuccess
	if existed {
		status = protocol.RegisterWorkerStatusExists
	}

	h.logger.Info("worker registered",
		"event", "worker_registered",
		"status", string(status),
		"worker_id", worker.ID,
		"address", worker.Address,
		"total_slots", worker.TotalSlots,
		"free_slots", worker.FreeSlots,
		"disk_total_bytes", worker.DiskTotalBytes,
		"disk_free_bytes", worker.DiskFreeBytes,
	)

	c.JSON(http.StatusOK, protocol.RegisterWorkerResponse{
		Status:      status,
		WorkerID:    worker.ID,
		SplitPolicy: h.splitPolicy,
	})
}

func (h *WorkerHandler) Heartbeat(c *gin.Context) {
	var req protocol.HeartbeatWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.logger.Warn("worker heartbeat bad request", "event", "worker_heartbeat_bad_request", "error", err)
		c.JSON(http.StatusBadRequest, protocol.HeartbeatWorkerResponse{
			Status: protocol.HeartbeatWorkerStatusFailed,
		})
		return
	}

	if err := h.registry.HeartbeatWorker(req); err != nil {
		status := protocol.HeartbeatWorkerStatusFailed
		code := http.StatusBadRequest
		if errors.Is(err, service.ErrWorkerNotFound) {
			status = protocol.HeartbeatWorkerStatusNotFound
			code = http.StatusNotFound
			h.logger.Warn("worker heartbeat not found", "event", "worker_heartbeat_not_found", "worker_id", req.WorkerID)
		} else {
			h.logger.Warn("worker heartbeat failed", "event", "worker_heartbeat_failed", "worker_id", req.WorkerID, "error", err)
		}

		c.JSON(code, protocol.HeartbeatWorkerResponse{
			Status: status,
		})
		return
	}

	h.logger.Info("worker heartbeat",
		"event", "worker_heartbeat",
		"worker_id", req.WorkerID,
		"total_slots", req.TotalSlots,
		"running_process_segment", req.RunningProcessSegment,
		"running_assemble_gif", req.RunningAssembleGIF,
		"reported_running_tasks", len(req.RunningTasks),
		"disk_total_bytes", req.DiskTotalBytes,
		"disk_free_bytes", req.DiskFreeBytes,
	)

	c.JSON(http.StatusOK, protocol.HeartbeatWorkerResponse{
		Status: protocol.HeartbeatWorkerStatusSuccess,
	})
}
