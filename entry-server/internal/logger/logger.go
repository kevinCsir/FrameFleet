package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	OutputStdout  = "stdout"
	OutputFile    = "file"
	OutputBoth    = "both"
	OutputDiscard = "discard"
)

type Config struct {
	Level  string
	Output string
	File   string
}

type Logger struct {
	*slog.Logger
}

func NewFromEnv() (*Logger, func() error, error) {
	return New(Config{
		Level:  envOrDefault("LOG_LEVEL", "info"),
		Output: envOrDefault("LOG_OUTPUT", OutputStdout),
		File:   envOrDefault("LOG_FILE", "logs/entry-server.log"),
	})
}

func New(config Config) (*Logger, func() error, error) {
	writer, closeFn, err := writerFromConfig(config)
	if err != nil {
		return nil, nil, err
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: parseLevel(config.Level),
	})

	return &Logger{Logger: slog.New(handler)}, closeFn, nil
}

func writerFromConfig(config Config) (io.Writer, func() error, error) {
	switch strings.ToLower(config.Output) {
	case OutputStdout, "":
		return os.Stdout, func() error { return nil }, nil
	case OutputDiscard:
		return io.Discard, func() error { return nil }, nil
	case OutputFile:
		file, err := openLogFile(config.File)
		if err != nil {
			return nil, nil, err
		}
		return file, file.Close, nil
	case OutputBoth:
		file, err := openLogFile(config.File)
		if err != nil {
			return nil, nil, err
		}
		return io.MultiWriter(os.Stdout, file), file.Close, nil
	default:
		return os.Stdout, func() error { return nil }, nil
	}
}

func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
