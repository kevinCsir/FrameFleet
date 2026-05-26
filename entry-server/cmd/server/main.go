package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"framefleet/entry-server/internal/logger"
	entryserver "framefleet/entry-server/internal/server"
	"framefleet/entry-server/internal/service"
	"framefleet/pkg/envfile"
	"framefleet/pkg/protocol"
)

func main() {
	if err := envfile.LoadWithOverride("ENTRY_ENV_FILE", ".env", "entry-server/.env"); err != nil {
		log.Fatalf("load entry env failed: %v", err)
	}

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

	processingPolicy := protocol.ProcessingPolicy{
		CannyLowThreshold:  intFromEnv("PROCESS_CANNY_LOW_THRESHOLD", 80),
		CannyHighThreshold: intFromEnv("PROCESS_CANNY_HIGH_THRESHOLD", 160),
		AssembleMode:       gifAssembleModeFromEnv("GIF_ASSEMBLE_MODE", protocol.GIFAssembleModeLocalPaletteConcat),
	}
	workerRegistry := service.NewWorkerRegistry(appLogger)
	server := entryserver.New(workerRegistry, entryserver.HeartbeatConfig{
		Timeout:       durationFromEnvSeconds("WORKER_HEARTBEAT_TIMEOUT_SECONDS", 30*time.Second),
		CheckInterval: durationFromEnvSeconds("WORKER_HEARTBEAT_CHECK_INTERVAL_SECONDS", 10*time.Second),
	}, protocol.SplitPolicy{
		TargetSegmentSizeBytes:  int64FromEnv("SPLIT_TARGET_SEGMENT_SIZE_BYTES", 0),
		TargetSegmentDurationMS: int64FromEnv("SPLIT_TARGET_SEGMENT_DURATION_MS", 3_000),
		MaxSegments:             intFromEnv("SPLIT_MAX_SEGMENTS", 0),
	}, processingPolicy, appLogger)

	appLogger.Info("entry server starting",
		"event", "entry_server_start",
		"addr", addr,
		"heartbeat_timeout_seconds", int(server.HeartbeatTimeout().Seconds()),
		"heartbeat_check_interval_seconds", int(server.HeartbeatCheckInterval().Seconds()),
		"split_target_segment_size_bytes", server.SplitPolicy().TargetSegmentSizeBytes,
		"split_target_segment_duration_ms", server.SplitPolicy().TargetSegmentDurationMS,
		"split_max_segments", server.SplitPolicy().MaxSegments,
		"processing_canny_low_threshold", server.ProcessingPolicy().CannyLowThreshold,
		"processing_canny_high_threshold", server.ProcessingPolicy().CannyHighThreshold,
		"processing_assemble_mode", server.ProcessingPolicy().AssembleMode,
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

func intFromEnv(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func int64FromEnv(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func gifAssembleModeFromEnv(key string, fallback protocol.GIFAssembleMode) protocol.GIFAssembleMode {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	mode := protocol.NormalizeGIFAssembleMode(protocol.GIFAssembleMode(raw))
	if mode == protocol.GIFAssembleModeLocalPaletteConcat && raw != string(protocol.GIFAssembleModeLocalPaletteConcat) {
		return fallback
	}
	return mode
}
