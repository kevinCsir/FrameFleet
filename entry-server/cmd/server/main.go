package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"framefleet/entry-server/internal/logger"
	entryserver "framefleet/entry-server/internal/server"
	"framefleet/entry-server/internal/service"
)

func main() {
	addr := os.Getenv("ENTRY_SERVER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	appLogger, closeLogger, err := logger.NewFromEnv()
	if err != nil {
		log.Fatalf("initialize logger failed: %v", err)
	}
	defer func() {
		if err := closeLogger(); err != nil {
			log.Printf("close logger failed: %v", err)
		}
	}()

	workerRegistry := service.NewWorkerRegistry(appLogger)
	server := entryserver.New(workerRegistry, entryserver.HeartbeatConfig{
		Timeout:       durationFromEnvSeconds("WORKER_HEARTBEAT_TIMEOUT_SECONDS", 30*time.Second),
		CheckInterval: durationFromEnvSeconds("WORKER_HEARTBEAT_CHECK_INTERVAL_SECONDS", 10*time.Second),
	}, appLogger)

	appLogger.Info("entry server starting",
		"event", "entry_server_start",
		"addr", addr,
		"heartbeat_timeout_seconds", int(server.HeartbeatTimeout().Seconds()),
		"heartbeat_check_interval_seconds", int(server.HeartbeatCheckInterval().Seconds()),
	)

	if err := server.Run(addr); err != nil {
		appLogger.Error("entry server stopped", "event", "entry_server_stopped", "error", err)
		log.Fatalf("entry server stopped: %v", err)
	}
}

func durationFromEnvSeconds(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}

	return time.Duration(seconds) * time.Second
}
