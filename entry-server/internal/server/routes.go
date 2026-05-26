package server

import (
	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/handlers"
	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

func registerRoutes(router *gin.Engine, registry *service.WorkerRegistry, jobs *service.JobManager, splitPolicy protocol.SplitPolicy, processingPolicy protocol.ProcessingPolicy, appLogger *logger.Logger) {
	workerHandler := handlers.NewWorkerHandler(registry, splitPolicy, processingPolicy, appLogger)
	jobHandler := handlers.NewJobHandler(jobs, appLogger)
	jobResultHandler := handlers.NewJobResultHandler(jobs, appLogger)
	taskHandler := handlers.NewTaskHandler(jobs, appLogger)

	router.POST("/workers/register", workerHandler.Register)
	router.POST("/workers/heartbeat", workerHandler.Heartbeat)
	router.POST("/jobs", jobHandler.Create)
	router.GET("/jobs/result", jobHandler.Result)
	router.POST("/jobs/:job_id/assembled", jobResultHandler.Assembled)
	router.POST("/tasks/:task_id/accepted", taskHandler.Accepted)
	router.POST("/tasks/:task_id/completed", taskHandler.Completed)
	router.POST("/tasks/:task_id/failed", taskHandler.Failed)
}
