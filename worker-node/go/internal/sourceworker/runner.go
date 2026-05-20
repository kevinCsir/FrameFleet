package sourceworker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/enginepool"
	"framefleet/worker-node/go/internal/engineprotocol"
	"framefleet/worker-node/go/internal/entryclient"
	"framefleet/worker-node/go/internal/peerclient"
	"framefleet/worker-node/go/internal/spool"
	"framefleet/worker-node/go/internal/workerstate"
)

type Runner struct {
	inputDir string
	interval time.Duration
	logger   *slog.Logger
	entry    *entryclient.Client
	peers    *peerclient.Client
	engines  *enginepool.Pool
	spool    *spool.Manager
	state    *workerstate.State

	mu           sync.Mutex
	pendingSplit map[string]sourceVideoTask
	done         map[string]struct{}
}

type Config struct {
	InputDir string
	Interval time.Duration
}

type sourceVideoTask struct {
	Path           string
	VideoName      string
	TotalSizeBytes int64
}

type splitVideoTask struct {
	Path           string
	VideoName      string
	TotalSizeBytes int64
	Segments       []engineprotocol.SegmentFile
}

type internalSegmentWork struct {
	segment engineprotocol.SegmentFile
	lease   *enginepool.Lease
}

var createJobRetryDelays = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

func New(cfg Config, logger *slog.Logger, entry *entryclient.Client, peers *peerclient.Client, engines *enginepool.Pool, spoolManager *spool.Manager, state *workerstate.State) *Runner {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		inputDir: cfg.InputDir,
		interval: cfg.Interval,
		logger:   logger,
		entry:    entry,
		peers:    peers,
		engines:  engines,
		spool:    spoolManager,
		state:    state,

		pendingSplit: make(map[string]sourceVideoTask),
		done:         make(map[string]struct{}),
	}
}

func (r *Runner) Start(ctx context.Context) {
	if r.inputDir == "" {
		r.logger.Info("source worker disabled: input dir not configured", "event", "source_worker_disabled")
		return
	}

	go r.scanLoop(ctx)
	go r.processLoop(ctx)
}

func (r *Runner) scanLoop(ctx context.Context) {
	r.scan(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("source worker scanner stopped", "event", "source_worker_scanner_stopped")
			return
		case <-ticker.C:
			r.scan(ctx)
		}
	}
}

func (r *Runner) processLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.processOne(ctx)
		select {
		case <-ctx.Done():
			r.logger.Info("source worker processor stopped", "event", "source_worker_processor_stopped")
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) scan(ctx context.Context) {
	entries, err := os.ReadDir(r.inputDir)
	if err != nil {
		if os.IsNotExist(err) {
			if mkdirErr := os.MkdirAll(r.inputDir, 0755); mkdirErr != nil {
				r.logger.Warn("create source input dir failed", "event", "source_input_dir_create_failed", "dir", r.inputDir, "error", mkdirErr)
			}
			return
		}
		r.logger.Warn("scan source input dir failed", "event", "source_input_scan_failed", "dir", r.inputDir, "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(r.inputDir, entry.Name())
		r.enqueueSourceVideo(sourceVideoTask{
			Path:           path,
			VideoName:      entry.Name(),
			TotalSizeBytes: fileSize(path),
		})
	}
}

func (r *Runner) enqueueSourceVideo(task sourceVideoTask) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.done[task.Path]; ok {
		return
	}
	if _, ok := r.pendingSplit[task.Path]; ok {
		return
	}

	r.pendingSplit[task.Path] = task
	r.logger.Info("source video queued", "event", "source_video_queued", "path", task.Path)
}

func (r *Runner) processOne(ctx context.Context) {
	if r.state.BackpressureActive() {
		r.logger.Info("source worker backpressure active", "event", "source_worker_backpressure_active")
		return
	}

	if task, ok := r.popPendingSplit(); ok {
		splitTask, split := r.splitVideo(ctx, task)
		if split {
			if r.registerAndDispatch(ctx, splitTask) {
				r.markDone(task.Path)
			} else {
				r.markDone(task.Path)
			}
		} else {
			r.requeuePendingSplit(task)
		}
	}
}

func (r *Runner) popPendingSplit() (sourceVideoTask, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for path, task := range r.pendingSplit {
		delete(r.pendingSplit, path)
		return task, true
	}
	return sourceVideoTask{}, false
}

func (r *Runner) requeuePendingSplit(task sourceVideoTask) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.done[task.Path]; ok {
		return
	}
	r.pendingSplit[task.Path] = task
}

func (r *Runner) markDone(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.pendingSplit, path)
	r.done[path] = struct{}{}
}

func (r *Runner) splitVideo(ctx context.Context, task sourceVideoTask) (splitVideoTask, bool) {
	lease, err := r.engines.Acquire(ctx)
	if err != nil {
		r.logger.Warn("source split slot acquire failed", "event", "source_split_slot_acquire_failed", "path", task.Path, "error", err)
		return splitVideoTask{}, false
	}
	defer lease.Release()

	jobKey := safeJobKey(task.VideoName)
	splitResp, err := lease.Call(ctx, engineprotocol.Request{
		Operation:    engineprotocol.OpSplitVideo,
		SegmentCount: r.segmentCount(),
		Input: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: task.Path,
			Name: task.VideoName,
		},
		OutputDir: r.spool.OutgoingDir(jobKey),
	})
	if err != nil {
		r.logger.Warn("source video split failed", "event", "source_video_split_failed", "path", task.Path, "error", err)
		return splitVideoTask{}, false
	}
	if splitResp.Type == engineprotocol.ResponseTypeFailed {
		r.logger.Warn("source video split failed", "event", "source_video_split_failed", "path", task.Path, "reason", splitResp.Reason)
		return splitVideoTask{}, false
	}
	if len(splitResp.Segments) == 0 {
		r.logger.Warn("source video split returned no segments", "event", "source_video_split_empty", "path", task.Path)
		return splitVideoTask{}, false
	}

	return splitVideoTask{
		Path:           task.Path,
		VideoName:      task.VideoName,
		TotalSizeBytes: task.TotalSizeBytes,
		Segments:       splitResp.Segments,
	}, true
}

func (r *Runner) registerAndDispatch(ctx context.Context, task splitVideoTask) bool {
	workerID := r.state.WorkerID()
	if workerID == "" {
		r.logger.Warn("source video skipped before registration", "event", "source_video_skipped_unregistered", "path", task.Path)
		return false
	}

	internalWork := make(map[int]internalSegmentWork)
	jobTasks := make([]protocol.CreateJobTaskRequest, 0, len(task.Segments))
	for _, segment := range task.Segments {
		lease, err := r.engines.TryAcquire()
		mode := protocol.TaskProcessingModeExternal
		if err == nil {
			mode = protocol.TaskProcessingModeInternal
			internalWork[segment.SegmentIndex] = internalSegmentWork{segment: segment, lease: lease}
		} else if err != enginepool.ErrNoIdleEngine {
			r.logger.Warn("source segment slot acquire failed", "event", "source_segment_slot_acquire_failed", "path", task.Path, "segment_index", segment.SegmentIndex, "error", err)
		}
		jobTasks = append(jobTasks, protocol.CreateJobTaskRequest{
			SegmentIndex: segment.SegmentIndex,
			Mode:         mode,
		})
	}

	releaseInternalWork := true
	defer func() {
		if releaseInternalWork {
			for _, work := range internalWork {
				work.lease.Release()
			}
		}
	}()

	jobReq := protocol.CreateJobRequest{
		WorkerID:       workerID,
		VideoName:      task.VideoName,
		TotalSizeBytes: task.TotalSizeBytes,
		Tasks:          jobTasks,
	}
	jobResp, err := r.createJobWithRetry(ctx, task, jobReq)
	if err != nil {
		r.logger.Warn("source job abandoned after create retries",
			"event", "source_job_create_abandoned",
			"video_name", task.VideoName,
			"path", task.Path,
			"attempts", len(createJobRetryDelays)+1,
			"error", err,
		)
		return false
	}

	for _, assignment := range jobResp.Assignments {
		switch assignment.Mode {
		case protocol.TaskProcessingModeInternal:
			work, ok := internalWork[assignment.SegmentIndex]
			if !ok {
				r.logger.Warn("internal assignment missing held slot", "event", "source_internal_assignment_missing_slot", "segment_index", assignment.SegmentIndex)
				return true
			}
			r.processInternalSegment(ctx, work.lease, workerID, assignment, work.segment)
			work.lease.Release()
			delete(internalWork, assignment.SegmentIndex)
		case protocol.TaskProcessingModeExternal:
			segment, ok := segmentByIndex(task.Segments, assignment.SegmentIndex)
			if !ok {
				r.logger.Warn("external segment missing", "event", "source_external_segment_missing", "segment_index", assignment.SegmentIndex)
				return true
			}
			if assignment.Address == "" {
				r.logger.Warn("external assignment missing address", "event", "source_external_assignment_missing_address", "task_id", assignment.TaskID)
				return true
			}
			if err := r.peers.UploadSegment(ctx, assignment.Address, assignment.TaskID, segment.Path); err != nil {
				r.logger.Warn("upload segment to worker failed", "event", "source_external_segment_upload_failed", "task_id", assignment.TaskID, "address", assignment.Address, "error", err)
				return true
			}
		default:
			r.logger.Warn("unknown source assignment mode", "event", "source_assignment_unknown_mode", "mode", string(assignment.Mode), "task_id", assignment.TaskID)
			return true
		}
	}

	releaseInternalWork = false
	return true
}

func (r *Runner) createJobWithRetry(ctx context.Context, task splitVideoTask, req protocol.CreateJobRequest) (protocol.CreateJobResponse, error) {
	var lastErr error
	attempts := len(createJobRetryDelays) + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		jobResp, err := r.entry.CreateJob(ctx, req)
		if err == nil {
			if attempt > 1 {
				r.logger.Info("source job create retry succeeded",
					"event", "source_job_create_retry_succeeded",
					"video_name", task.VideoName,
					"path", task.Path,
					"attempt", attempt,
				)
			}
			return jobResp, nil
		}
		lastErr = err
		r.logger.Warn("create source job failed",
			"event", "source_job_create_failed",
			"video_name", task.VideoName,
			"path", task.Path,
			"attempt", attempt,
			"attempts", attempts,
			"error", err,
		)
		if attempt == attempts {
			break
		}
		delay := createJobRetryDelays[attempt-1]
		select {
		case <-ctx.Done():
			return protocol.CreateJobResponse{}, ctx.Err()
		case <-time.After(delay):
		}
	}
	return protocol.CreateJobResponse{}, lastErr
}

func (r *Runner) processInternalSegment(ctx context.Context, lease *enginepool.Lease, workerID string, assignment protocol.TaskAssignment, segment engineprotocol.SegmentFile) {
	artifactPath := r.spool.ArtifactPath(assignment.TaskID)
	r.state.StartTask(assignment.TaskID, protocol.TaskTypeProcessSegment)
	engineResp, err := lease.Call(ctx, engineprotocol.Request{
		Operation: engineprotocol.OpProcessSegment,
		Input:     fileRef(segment.Path, segment.Name),
		Output:    fileRef(artifactPath, filepath.Base(artifactPath)),
	})
	r.state.FinishTask(assignment.TaskID)
	if err != nil || engineResp.Type == engineprotocol.ResponseTypeFailed {
		reason := reasonFromEngine(err, engineResp)
		_, _ = r.entry.FailTask(ctx, assignment.TaskID, protocol.TaskFailedRequest{WorkerID: workerID, Reason: reason, Retryable: true})
		return
	}
	if _, err := r.entry.CompleteTask(ctx, assignment.TaskID, protocol.TaskCompletedRequest{
		WorkerID:        workerID,
		Checksum:        engineResp.Checksum,
		FrameCount:      engineResp.FrameCount,
		DurationMS:      engineResp.DurationMS,
		OutputSizeBytes: engineResp.OutputSizeBytes,
	}); err != nil {
		r.logger.Warn("complete internal segment failed", "event", "source_internal_segment_complete_failed", "task_id", assignment.TaskID, "error", err)
	}
}

func (r *Runner) segmentCount() int {
	policy := r.state.SplitPolicy()
	if policy.MaxSegments > 0 {
		return policy.MaxSegments
	}
	return 3
}

func segmentByIndex(segments []engineprotocol.SegmentFile, index int) (engineprotocol.SegmentFile, bool) {
	for _, segment := range segments {
		if segment.SegmentIndex == index {
			return segment, true
		}
	}
	return engineprotocol.SegmentFile{}, false
}

func fileRef(path string, name string) *engineprotocol.FileRef {
	return &engineprotocol.FileRef{Mode: engineprotocol.DataModeFile, Path: path, Name: name}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func safeJobKey(videoName string) string {
	return fmt.Sprintf("src_%d_%s", time.Now().UnixNano(), filepath.Base(videoName))
}

func reasonFromEngine(err error, resp engineprotocol.Response) string {
	if err != nil {
		return err.Error()
	}
	if resp.Reason != "" {
		return resp.Reason
	}
	return "engine operation failed"
}
