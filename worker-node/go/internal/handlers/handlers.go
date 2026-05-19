package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/worker-node/go/internal/enginepool"
	"framefleet/worker-node/go/internal/entryclient"
	"framefleet/worker-node/go/internal/peerclient"
	"framefleet/worker-node/go/internal/spool"
	"framefleet/worker-node/go/internal/workerstate"
)

type Handler struct {
	logger  *slog.Logger
	spool   *spool.Manager
	engines *enginepool.Pool
	entry   *entryclient.Client
	state   *workerstate.State
	peers   *peerclient.Client
}

func New(logger *slog.Logger, spoolManager *spool.Manager, engines *enginepool.Pool, entry *entryclient.Client, state *workerstate.State, peers *peerclient.Client) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		logger:  logger,
		spool:   spoolManager,
		engines: engines,
		entry:   entry,
		state:   state,
		peers:   peers,
	}
}

func (h *Handler) RegisterRoutes(router *gin.Engine) {
	router.POST("/segments/:task_id/upload", h.UploadSegment)
	router.POST("/tasks/assemble_gif", h.StartAssembleGIF)
	router.GET("/artifacts/:task_id", h.GetArtifact)
	router.GET("/results/:result_name", h.GetResult)
	router.POST("/jobs/result", h.JobResultNotification)
}

func notImplemented(c *gin.Context, operation string) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"status":    "not_implemented",
		"operation": operation,
	})
}
