package server

import (
	"context"
	"os"
	"time"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/protocol"
)

type Server struct {
	engine          *gin.Engine
	workerRegistry  *service.WorkerRegistry
	jobManager      *service.JobManager
	heartbeatConfig HeartbeatConfig
	splitPolicy     protocol.SplitPolicy
	logger          *logger.Logger
}

type HeartbeatConfig struct {
	Timeout       time.Duration
	CheckInterval time.Duration
}

func New(registry *service.WorkerRegistry, heartbeatConfig HeartbeatConfig, splitPolicy protocol.SplitPolicy, appLogger *logger.Logger) *Server {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	jobManager := service.NewJobManager(registry, appLogger)
	engine := gin.New()
	engine.Use(requestLogger(appLogger), gin.Recovery())
	registerRoutes(engine, registry, jobManager, splitPolicy, appLogger)

	return &Server{
		engine:          engine,
		workerRegistry:  registry,
		jobManager:      jobManager,
		heartbeatConfig: heartbeatConfig,
		splitPolicy:     splitPolicy,
		logger:          appLogger,
	}
}

func (s *Server) Run(addr string) error {
	s.startWorkerExpiryChecker(context.Background())
	s.startBackpressureRefresher(context.Background())
	return s.engine.Run(addr)
}

func (s *Server) HeartbeatTimeout() time.Duration {
	return s.heartbeatConfig.Timeout
}

func (s *Server) HeartbeatCheckInterval() time.Duration {
	return s.heartbeatConfig.CheckInterval
}

func (s *Server) SplitPolicy() protocol.SplitPolicy {
	return s.splitPolicy
}

func (s *Server) startWorkerExpiryChecker(ctx context.Context) {
	if s.heartbeatConfig.Timeout <= 0 || s.heartbeatConfig.CheckInterval <= 0 {
		return
	}

	ticker := time.NewTicker(s.heartbeatConfig.CheckInterval)
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.workerRegistry.MarkExpiredWorkers(s.heartbeatConfig.Timeout)
			}
		}
	}()
}

func (s *Server) startBackpressureRefresher(ctx context.Context) {
	if s.heartbeatConfig.CheckInterval <= 0 {
		return
	}

	s.workerRegistry.RefreshBackpressureStatus()
	ticker := time.NewTicker(s.heartbeatConfig.CheckInterval)
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.workerRegistry.RefreshBackpressureStatus()
			}
		}
	}()
}
