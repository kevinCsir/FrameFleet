package sourceworker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

	seen map[string]struct{}
}

type Config struct {
	InputDir string
	Interval time.Duration
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
		seen:     make(map[string]struct{}),
	}
}

func (r *Runner) Start(ctx context.Context) {
	if r.inputDir == "" {
		r.logger.Info("source worker disabled: input dir not configured", "event", "source_worker_disabled")
		return
	}

	go func() {
		r.scan(ctx)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				r.logger.Info("source worker stopped", "event", "source_worker_stopped")
				return
			case <-ticker.C:
				r.scan(ctx)
			}
		}
	}()
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
		if _, ok := r.seen[path]; ok {
			continue
		}
		r.seen[path] = struct{}{}
		r.processVideo(ctx, path, entry.Name())
	}
}

func (r *Runner) processVideo(ctx context.Context, path string, videoName string) {
	workerID := r.state.WorkerID()
	if workerID == "" {
		r.logger.Warn("source video skipped before registration", "event", "source_video_skipped_unregistered", "path", path)
		return
	}

	jobKey := safeJobKey(videoName)
	splitResp, err := r.engines.Call(ctx, engineprotocol.Request{
		Operation:    engineprotocol.OpSplitVideo,
		JobID:        jobKey,
		SegmentCount: r.segmentCount(),
		Input: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: path,
			Name: videoName,
		},
		OutputDir: r.spool.OutgoingDir(jobKey),
	})
	if err != nil {
		r.logger.Warn("source video split failed", "event", "source_video_split_failed", "path", path, "error", err)
		return
	}
	if splitResp.Type == engineprotocol.ResponseTypeFailed {
		r.logger.Warn("source video split failed", "event", "source_video_split_failed", "path", path, "reason", splitResp.Reason)
		return
	}

	lease, err := r.engines.TryAcquire()
	if err == nil {
		if r.processInternal(ctx, lease, workerID, path, videoName, splitResp.Segments) {
			lease.Release()
			return
		}
		lease.Release()
	} else if err != enginepool.ErrNoIdleEngine {
		r.logger.Warn("source local slot acquire failed", "event", "source_local_slot_acquire_failed", "path", path, "error", err)
	}

	r.processExternal(ctx, workerID, path, videoName, splitResp.Segments)
}

func (r *Runner) processInternal(ctx context.Context, lease *enginepool.Lease, workerID string, path string, videoName string, segments []engineprotocol.SegmentFile) bool {
	jobResp, err := r.entry.CreateJob(ctx, protocol.CreateJobRequest{
		WorkerID:       workerID,
		VideoName:      videoName,
		SegmentCount:   len(segments),
		TotalSizeBytes: fileSize(path),
		Mode:           protocol.JobModeInternal,
	})
	if err != nil {
		r.logger.Warn("create internal job failed", "event", "source_internal_job_create_failed", "video_name", videoName, "error", err)
		return false
	}

	artifactInputs := make([]engineprotocol.FileRef, 0, len(jobResp.Assignments))
	for _, assignment := range jobResp.Assignments {
		segment, ok := segmentByIndex(segments, assignment.SegmentIndex)
		if !ok {
			r.logger.Warn("internal segment missing", "event", "source_internal_segment_missing", "segment_index", assignment.SegmentIndex)
			return true
		}
		artifactPath := r.spool.ArtifactPath(assignment.TaskID)
		r.state.StartTask(assignment.TaskID, protocol.TaskTypeProcessSegment)
		engineResp, err := lease.Call(ctx, engineprotocol.Request{
			Operation:    engineprotocol.OpProcessSegment,
			JobID:        jobResp.JobID,
			TaskID:       assignment.TaskID,
			SegmentIndex: assignment.SegmentIndex,
			Input:        fileRef(segment.Path, segment.Name),
			Output:       fileRef(artifactPath, filepath.Base(artifactPath)),
		})
		r.state.FinishTask(assignment.TaskID)
		if err != nil || engineResp.Type == engineprotocol.ResponseTypeFailed {
			reason := reasonFromEngine(err, engineResp)
			_, _ = r.entry.FailTask(ctx, assignment.TaskID, protocol.TaskFailedRequest{WorkerID: workerID, Reason: reason, Retryable: true})
			return true
		}
		if _, err := r.entry.CompleteTask(ctx, assignment.TaskID, protocol.TaskCompletedRequest{
			WorkerID:        workerID,
			Checksum:        engineResp.Checksum,
			FrameCount:      engineResp.FrameCount,
			DurationMS:      engineResp.DurationMS,
			OutputSizeBytes: engineResp.OutputSizeBytes,
		}); err != nil {
			r.logger.Warn("complete internal segment failed", "event", "source_internal_segment_complete_failed", "task_id", assignment.TaskID, "error", err)
			return true
		}
		artifactInputs = append(artifactInputs, *fileRef(artifactPath, filepath.Base(artifactPath)))
	}

	resultName := jobResp.JobID + ".gif"
	resultPath := r.spool.ResultPath(resultName)
	engineResp, err := lease.Call(ctx, engineprotocol.Request{
		Operation: engineprotocol.OpAssembleGIF,
		JobID:     jobResp.JobID,
		Inputs:    artifactInputs,
		Output:    fileRef(resultPath, resultName),
	})
	if err != nil || engineResp.Type == engineprotocol.ResponseTypeFailed {
		reason := reasonFromEngine(err, engineResp)
		_, _ = r.entry.ReportAssembled(ctx, jobResp.JobID, protocol.JobAssembledRequest{WorkerID: workerID, Status: protocol.JobResultStatusFailed, Reason: reason, Retryable: true})
		return true
	}
	_, _ = r.entry.ReportAssembled(ctx, jobResp.JobID, protocol.JobAssembledRequest{
		WorkerID:        workerID,
		Status:          protocol.JobResultStatusSuccess,
		ResultName:      resultName,
		Checksum:        engineResp.Checksum,
		DurationMS:      engineResp.DurationMS,
		OutputSizeBytes: engineResp.OutputSizeBytes,
	})
	return true
}

func (r *Runner) processExternal(ctx context.Context, workerID string, path string, videoName string, segments []engineprotocol.SegmentFile) {
	jobResp, err := r.entry.CreateJob(ctx, protocol.CreateJobRequest{
		WorkerID:       workerID,
		VideoName:      videoName,
		SegmentCount:   len(segments),
		TotalSizeBytes: fileSize(path),
		Mode:           protocol.JobModeExternal,
	})
	if err != nil {
		r.logger.Warn("create external job failed", "event", "source_external_job_create_failed", "video_name", videoName, "error", err)
		return
	}

	for _, assignment := range jobResp.Assignments {
		segment, ok := segmentByIndex(segments, assignment.SegmentIndex)
		if !ok {
			r.logger.Warn("external segment missing", "event", "source_external_segment_missing", "segment_index", assignment.SegmentIndex)
			return
		}
		if err := r.peers.UploadSegment(ctx, assignment.Address, assignment.TaskID, segment.Path); err != nil {
			r.logger.Warn("upload segment to worker failed", "event", "source_external_segment_upload_failed", "task_id", assignment.TaskID, "address", assignment.Address, "error", err)
			return
		}
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
