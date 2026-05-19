package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/model"
	"framefleet/entry-server/internal/service"
)

type WorkerHandler struct {
	registry *service.WorkerRegistry
}

func NewWorkerHandler(registry *service.WorkerRegistry) *WorkerHandler {
	return &WorkerHandler{registry: registry}
}

func (h *WorkerHandler) Register(c *gin.Context) {
	var req model.RegisterWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.RegisterWorkerResponse{
			Status: model.RegisterWorkerStatusFailed,
		})
		return
	}

	worker, existed, err := h.registry.RegisterWorker(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.RegisterWorkerResponse{
			Status: model.RegisterWorkerStatusFailed,
		})
		return
	}

	status := model.RegisterWorkerStatusSuccess
	if existed {
		status = model.RegisterWorkerStatusExists
	}

	c.JSON(http.StatusOK, model.RegisterWorkerResponse{
		Status:   status,
		WorkerID: worker.ID,
	})
}
