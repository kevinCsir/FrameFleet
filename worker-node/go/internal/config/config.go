package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr         string
	EntryBaseURL       string
	AdvertisedAddress  string
	TotalSlots         int
	DataDir            string
	EngineBinaryPath   string
	HeartbeatInterval  time.Duration
	DiskTotalBytes     int64
	DiskFreeBytes      int64
	InputDir           string
	SourceScanInterval time.Duration
}

func FromEnv() Config {
	return Config{
		ListenAddr:         stringFromEnv("WORKER_LISTEN_ADDR", ":9001"),
		EntryBaseURL:       stringFromEnv("ENTRY_BASE_URL", "http://127.0.0.1:8080"),
		AdvertisedAddress:  stringFromEnv("WORKER_ADVERTISED_ADDRESS", "127.0.0.1:9001"),
		TotalSlots:         intFromEnv("WORKER_TOTAL_SLOTS", 4),
		DataDir:            stringFromEnv("WORKER_DATA_DIR", "worker-node/data"),
		EngineBinaryPath:   stringFromEnv("WORKER_ENGINE_BINARY", "worker-node/cpp/build/framefleet-engine"),
		HeartbeatInterval:  durationSecondsFromEnv("WORKER_HEARTBEAT_INTERVAL_SECONDS", 10*time.Second),
		DiskTotalBytes:     int64FromEnv("WORKER_DISK_TOTAL_BYTES", 1000*1000*1000),
		DiskFreeBytes:      int64FromEnv("WORKER_DISK_FREE_BYTES", 800*1000*1000),
		InputDir:           stringFromEnv("WORKER_INPUT_DIR", "worker-node/data/input"),
		SourceScanInterval: durationSecondsFromEnv("WORKER_SOURCE_SCAN_INTERVAL_SECONDS", 10*time.Second),
	}
}

func stringFromEnv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func intFromEnv(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func durationSecondsFromEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func int64FromEnv(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
