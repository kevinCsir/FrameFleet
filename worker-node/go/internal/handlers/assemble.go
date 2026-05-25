package handlers

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/engineprotocol"
)

func (h *Handler) StartAssembleGIF(c *gin.Context) {
	var req protocol.StartAssembleGIFRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusInvalidRequest})
		return
	}

	workerID := h.state.WorkerID()
	if workerID == "" {
		c.JSON(http.StatusServiceUnavailable, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusFailed})
		return
	}

	requiredDiskBytes := estimateAssembleDiskBytes(req.Video.TotalSizeBytes, req.Segments)
	disk := h.state.DiskUsage()
	if disk.FreeBytes < requiredDiskBytes {
		c.JSON(http.StatusOK, protocol.StartAssembleGIFResponse{
			Status:        protocol.StartAssembleGIFStatusInsufficientStorage,
			DiskFreeBytes: disk.FreeBytes,
		})
		return
	}

	go h.runAssembleGIF(req, workerID)

	c.JSON(http.StatusOK, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusSuccess})
}

func estimateAssembleDiskBytes(totalSizeBytes int64, segments []protocol.AssembleSegmentRef) int64 {
	totalOutputSize := int64(0)
	allOutputSizesKnown := len(segments) > 0
	for _, segment := range segments {
		if segment.OutputSizeBytes <= 0 {
			allOutputSizesKnown = false
			break
		}
		totalOutputSize += segment.OutputSizeBytes
	}

	base := totalSizeBytes
	if allOutputSizesKnown && totalOutputSize > 0 {
		base = totalOutputSize * 2
	}
	if base < 1 {
		base = 1
	}
	return base * 12 / 10
}

func (h *Handler) runAssembleGIF(req protocol.StartAssembleGIFRequest, workerID string) {
	ctx := context.Background()

	inputs := make([]engineprotocol.FileRef, len(req.Segments))
	for i, segment := range req.Segments {
		localPath := h.spool.AssembleArtifactPath(req.JobID, segment.TaskID)
		inputs[i] = engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: localPath,
			Name: filepath.Base(localPath),
		}

		h.logger.Info("assemble artifact download started",
			"event", "assemble_artifact_download_started",
			"job_id", req.JobID,
			"task_id", segment.TaskID,
			"segment_index", segment.SegmentIndex,
			"worker_address", segment.WorkerAddress,
			"local_path", localPath,
		)
		if err := h.peers.DownloadArtifact(ctx, segment.WorkerAddress, segment.TaskID, localPath); err != nil {
			reason := fmt.Sprintf("download artifact %s failed: %v", segment.TaskID, err)
			h.reportAssembleFailed(ctx, req.JobID, workerID, reason, true)
			return
		}
		h.logger.Info("assemble artifact download completed",
			"event", "assemble_artifact_download_completed",
			"job_id", req.JobID,
			"task_id", segment.TaskID,
			"segment_index", segment.SegmentIndex,
			"worker_address", segment.WorkerAddress,
			"local_path", localPath,
		)
	}

	resultName := req.JobID + ".gif"
	outputPath := h.spool.ResultPath(resultName)

	lease, err := h.engines.Acquire(ctx)
	if err != nil {
		reason := fmt.Sprintf("engine slot acquire failed: %v", err)
		h.reportAssembleFailed(ctx, req.JobID, workerID, reason, true)
		return
	}
	defer lease.Release()

	h.state.StartTask(req.AssembleTaskID, protocol.TaskTypeAssembleGIF)
	defer h.state.FinishTask(req.AssembleTaskID)

	engineResp, err := lease.Call(ctx, engineprotocol.Request{
		Operation: engineprotocol.OpAssembleGIF,
		Inputs:    inputs,
		Output: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: outputPath,
			Name: resultName,
		},
	})
	if err != nil {
		reason := fmt.Sprintf("engine assemble_gif failed: %v", err)
		h.reportAssembleFailed(ctx, req.JobID, workerID, reason, true)
		return
	}
	if engineResp.Type == engineprotocol.ResponseTypeFailed {
		reason := engineResp.Reason
		if reason == "" {
			reason = "engine assemble_gif failed"
		}
		h.reportAssembleFailed(ctx, req.JobID, workerID, reason, engineResp.Retryable)
		return
	}

	if _, err := h.entry.ReportAssembled(ctx, req.JobID, protocol.JobAssembledRequest{
		WorkerID:        workerID,
		Status:          protocol.JobResultStatusSuccess,
		ResultName:      resultName,
		Checksum:        engineResp.Checksum,
		DurationMS:      engineResp.DurationMS,
		OutputSizeBytes: engineResp.OutputSizeBytes,
	}); err != nil {
		h.logger.Warn("report assembled success failed", "event", "report_assembled_success_failed", "job_id", req.JobID, "error", err)
		return
	}
}

func (h *Handler) reportAssembleFailed(ctx context.Context, jobID string, workerID string, reason string, retryable bool) {
	if jobID == "" || workerID == "" || h.entry == nil {
		return
	}
	if _, err := h.entry.ReportAssembled(ctx, jobID, protocol.JobAssembledRequest{
		WorkerID:  workerID,
		Status:    protocol.JobResultStatusFailed,
		Reason:    reason,
		Retryable: retryable,
	}); err != nil {
		h.logger.Warn("report assembled failure failed", "event", "report_assembled_failure_failed", "job_id", jobID, "error", err)
	}
}
