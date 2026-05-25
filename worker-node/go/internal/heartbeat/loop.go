package heartbeat

import (
	"context"
	"log/slog"
	"time"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/entryclient"
	"framefleet/worker-node/go/internal/workerstate"
)

type Loop struct {
	client           *entryclient.Client
	state            *workerstate.State
	interval         time.Duration
	logger           *slog.Logger
	snapshotProvider RuntimeSnapshotProvider
}

type RuntimeSnapshotProvider func() RuntimeSnapshot

type RuntimeSnapshot struct {
	Source    any
	Slots     any
	SlotsIdle int
	SlotsBusy int
}

func New(client *entryclient.Client, state *workerstate.State, interval time.Duration, logger *slog.Logger, snapshotProvider RuntimeSnapshotProvider) *Loop {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Loop{
		client:           client,
		state:            state,
		interval:         interval,
		logger:           logger,
		snapshotProvider: snapshotProvider,
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
		l.logRuntimeSnapshot(req, "failed", err, false)
		return
	}
	l.state.SetBackpressure(resp.GlobalBackpressure)

	l.logger.Info("worker heartbeat sent",
		"event", "worker_heartbeat_sent",
		"worker_id", req.WorkerID,
		"backpressure_active", resp.GlobalBackpressure.Active,
	)
	l.logRuntimeSnapshot(req, "success", nil, resp.GlobalBackpressure.Active)
}

func (l *Loop) logRuntimeSnapshot(req protocol.HeartbeatWorkerRequest, heartbeatStatus string, heartbeatErr error, backpressureActive bool) {
	snapshot := RuntimeSnapshot{}
	if l.snapshotProvider != nil {
		snapshot = l.snapshotProvider()
	}

	args := []any{
		"event", "worker_runtime_snapshot",
		"worker_id", req.WorkerID,
		"heartbeat_status", heartbeatStatus,
		"backpressure_active", backpressureActive,
		"total_slots", req.TotalSlots,
		"running_process_segment", req.RunningProcessSegment,
		"running_assemble_gif", req.RunningAssembleGIF,
		"running_tasks_count", len(req.RunningTasks),
		"disk_total_bytes", req.DiskTotalBytes,
		"disk_free_bytes", req.DiskFreeBytes,
		"slots_idle", snapshot.SlotsIdle,
		"slots_busy", snapshot.SlotsBusy,
		"slots", snapshot.Slots,
		"source", snapshot.Source,
	}
	if heartbeatErr != nil {
		args = append(args, "heartbeat_error", heartbeatErr.Error())
	}
	l.logger.Info("worker runtime snapshot", args...)
}
