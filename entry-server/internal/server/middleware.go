package server

import (
	"time"

	"github.com/gin-gonic/gin"

	"framefleet/entry-server/internal/logger"
)

func requestLogger(appLogger *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()

		c.Next()

		latency := time.Since(startedAt)
		status := c.Writer.Status()
		level := "Info"
		if status >= 500 {
			level = "Error"
		} else if status >= 400 {
			level = "Warn"
		}

		args := []any{
			"event", "http_request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
		}

		switch level {
		case "Error":
			appLogger.Error("http request completed", args...)
		case "Warn":
			appLogger.Warn("http request completed", args...)
		default:
			appLogger.Info("http request completed", args...)
		}
	}
}
