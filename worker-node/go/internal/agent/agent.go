package agent

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/config"
	"framefleet/worker-node/go/internal/enginepool"
	"framefleet/worker-node/go/internal/entryclient"
	"framefleet/worker-node/go/internal/heartbeat"
	"framefleet/worker-node/go/internal/peerclient"
	workerserver "framefleet/worker-node/go/internal/server"
	"framefleet/worker-node/go/internal/sourceworker"
	"framefleet/worker-node/go/internal/spool"
	"framefleet/worker-node/go/internal/taskmonitor"
	"framefleet/worker-node/go/internal/workerlog"
	"framefleet/worker-node/go/internal/workerstate"
)

type Agent struct {
	cfg        config.Config
	logger     *slog.Logger
	enginePool *enginepool.Pool
	spool      *spool.Manager
	monitor    *taskmonitor.Monitor
	entry      *entryclient.Client
	state      *workerstate.State
	heartbeat  *heartbeat.Loop
	peers      *peerclient.Client
	source     *sourceworker.Runner
	closeLog   func() error
}

func New(cfg config.Config) (*Agent, error) {
	logger, closeLog, err := workerlog.New(workerlog.Config{
		Level:  cfg.LogLevel,
		Output: cfg.LogOutput,
		File:   cfg.LogFile,
	})
	if err != nil {
		return nil, err
	}

	spoolManager := spool.New(cfg.DataDir)
	for _, dir := range spoolManager.Dirs() {
		if err := os.MkdirAll(dir, 0755); err != nil {
			_ = closeLog()
			return nil, err
		}
	}

	state := workerstate.New(workerstate.Config{
		TotalSlots:     cfg.TotalSlots,
		DiskTotalBytes: cfg.DiskTotalBytes,
		DiskFreeBytes:  cfg.DiskFreeBytes,
	})
	entry := entryclient.New(cfg.EntryBaseURL)
	peers := peerclient.New()

	pool, err := enginepool.New(enginepool.Config{
		Slots:      cfg.TotalSlots,
		BinaryPath: cfg.EngineBinaryPath,
		DataDir:    cfg.DataDir,
	}, logger)
	if err != nil {
		_ = closeLog()
		return nil, err
	}

	return &Agent{
		cfg:        cfg,
		logger:     logger,
		enginePool: pool,
		spool:      spoolManager,
		monitor:    taskmonitor.New(cfg.HeartbeatInterval, logger),
		entry:      entry,
		state:      state,
		heartbeat:  heartbeat.New(entry, state, cfg.HeartbeatInterval, logger),
		peers:      peers,
		source: sourceworker.New(sourceworker.Config{
			InputDir: cfg.InputDir,
			Interval: cfg.SourceScanInterval,
		}, logger, entry, peers, pool, spoolManager, state),
		closeLog: closeLog,
	}, nil
}

func (a *Agent) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.enginePool.Start(ctx); err != nil {
		return err
	}

	registerResp, err := a.entry.RegisterWorker(ctx, protocol.RegisterWorkerRequest{
		Address:        a.cfg.AdvertisedAddress,
		TotalSlots:     a.cfg.TotalSlots,
		SupportedTasks: []protocol.TaskType{protocol.TaskTypeProcessSegment, protocol.TaskTypeAssembleGIF},
		DiskTotalBytes: a.cfg.DiskTotalBytes,
		DiskFreeBytes:  a.cfg.DiskFreeBytes,
	})
	if err != nil {
		return err
	}
	a.state.SetRegistration(registerResp.WorkerID, registerResp.SplitPolicy)
	a.logger.Info("worker registered with entry",
		"event", "worker_entry_registered",
		"worker_id", registerResp.WorkerID,
		"split_target_segment_duration_ms", registerResp.SplitPolicy.TargetSegmentDurationMS,
		"split_target_segment_size_bytes", registerResp.SplitPolicy.TargetSegmentSizeBytes,
		"split_max_segments", registerResp.SplitPolicy.MaxSegments,
	)
	defer func() {
		if err := a.enginePool.Stop(context.Background()); err != nil {
			a.logger.Error("engine pool stop failed", "event", "engine_pool_stop_failed", "error", err)
		}
		if a.closeLog != nil {
			if err := a.closeLog(); err != nil {
				a.logger.Error("worker logger close failed", "event", "worker_logger_close_failed", "error", err)
			}
		}
	}()

	a.heartbeat.Start(ctx)
	a.monitor.Start(ctx)

	router := workerserver.NewRouter(workerserver.Config{
		WorkerAddress: a.cfg.AdvertisedAddress,
		EngineSlots:   a.enginePool.Slots(),
	}, workerserver.Dependencies{
		Logger:  a.logger,
		Spool:   a.spool,
		Engines: a.enginePool,
		Entry:   a.entry,
		State:   a.state,
		Peers:   a.peers,
	})

	a.logger.Info("worker agent starting",
		"event", "worker_agent_start",
		"listen_addr", a.cfg.ListenAddr,
		"entry_base_url", a.cfg.EntryBaseURL,
		"advertised_address", a.cfg.AdvertisedAddress,
		"total_slots", a.cfg.TotalSlots,
		"data_dir", a.cfg.DataDir,
		"spool_root", a.spool.Root(),
		"engine_binary_path", a.cfg.EngineBinaryPath,
		"worker_id", a.state.WorkerID(),
		"input_dir", a.cfg.InputDir,
	)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+a.cfg.AdvertisedAddress+"/healthz", nil)
			if err == nil {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						cancel()
						a.source.Start(ctx)
						return
					}
				}
			}
			cancel()
			time.Sleep(100 * time.Millisecond)
		}
	}()

	return router.Run(a.cfg.ListenAddr)
}
