package server

import (
	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/service"
)

type Server struct {
	engine *gin.Engine
}

func New(registry *service.WorkerRegistry) *Server {
	engine := gin.Default()
	registerRoutes(engine, registry)

	return &Server{engine: engine}
}

func (s *Server) Run(addr string) error {
	return s.engine.Run(addr)
}
