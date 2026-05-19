package server

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/worker-node/go/internal/enginepool"
	"framefleet/worker-node/go/internal/entryclient"
	workerhandlers "framefleet/worker-node/go/internal/handlers"
	"framefleet/worker-node/go/internal/peerclient"
	"framefleet/worker-node/go/internal/spool"
	"framefleet/worker-node/go/internal/workerstate"
)

type Config struct {
	WorkerAddress string
	EngineSlots   int
}

type Dependencies struct {
	Logger  *slog.Logger
	Spool   *spool.Manager
	Engines *enginepool.Pool
	Entry   *entryclient.Client
	State   *workerstate.State
	Peers   *peerclient.Client
}

func NewRouter(cfg Config, deps Dependencies) *gin.Engine {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger(logger))

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":         "ok",
			"worker_address": cfg.WorkerAddress,
			"engine_slots":   cfg.EngineSlots,
		})
	})

	workerhandlers.New(logger, deps.Spool, deps.Engines, deps.Entry, deps.State, deps.Peers).RegisterRoutes(router)

	return router
}

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		logger.Info("worker request",
			"event", "worker_http_request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"client_ip", c.ClientIP(),
		)
	}
}
