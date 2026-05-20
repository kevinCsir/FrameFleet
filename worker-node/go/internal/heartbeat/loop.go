package heartbeat

import (
	"context"
	"log/slog"
	"time"

	"framefleet/worker-node/go/internal/entryclient"
	"framefleet/worker-node/go/internal/workerstate"
)

type Loop struct {
	client   *entryclient.Client
	state    *workerstate.State
	interval time.Duration
	logger   *slog.Logger
}

func New(client *entryclient.Client, state *workerstate.State, interval time.Duration, logger *slog.Logger) *Loop {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Loop{
		client:   client,
		state:    state,
		interval: interval,
		logger:   logger,
	}
}

func (l *Loop) Start(ctx context.Context) {
	go func() {
		l.send(ctx)

		ticker := time.NewTicker(l.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				l.logger.Info("worker heartbeat loop stopped", "event", "worker_heartbeat_loop_stopped")
				return
			case <-ticker.C:
				l.send(ctx)
			}
		}
	}()
}

func (l *Loop) send(ctx context.Context) {
	req := l.state.HeartbeatRequest()
	if req.WorkerID == "" {
		l.logger.Warn("skip heartbeat before worker registration", "event", "worker_heartbeat_skipped")
		return
	}

	resp, err := l.client.HeartbeatWorker(ctx, req)
	if err != nil {
		l.logger.Warn("worker heartbeat failed", "event", "worker_heartbeat_failed", "worker_id", req.WorkerID, "error", err)
		return
	}
	l.state.SetBackpressure(resp.GlobalBackpressure)

	l.logger.Info("worker heartbeat sent",
		"event", "worker_heartbeat_sent",
		"worker_id", req.WorkerID,
		"backpressure_active", resp.GlobalBackpressure.Active,
	)
}
