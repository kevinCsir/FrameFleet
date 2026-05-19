package taskmonitor

import (
	"context"
	"log/slog"
	"time"
)

type Monitor struct {
	interval time.Duration
	logger   *slog.Logger
}

func New(interval time.Duration, logger *slog.Logger) *Monitor {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Monitor{
		interval: interval,
		logger:   logger,
	}
}

func (m *Monitor) Start(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.logger.Info("worker task monitor stopped", "event", "worker_task_monitor_stopped")
				return
			case <-ticker.C:
				m.logger.Debug("worker task monitor tick", "event", "worker_task_monitor_tick")
			}
		}
	}()
}
