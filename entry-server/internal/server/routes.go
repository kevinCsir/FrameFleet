package server

import (
	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/handlers"
	"framefleet/entry-server/internal/service"
)

func registerRoutes(router *gin.Engine, registry *service.WorkerRegistry) {
	workerHandler := handlers.NewWorkerHandler(registry)

	router.POST("/workers/register", workerHandler.Register)
}
