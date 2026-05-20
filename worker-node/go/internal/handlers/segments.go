package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/engineprotocol"
)

func (h *Handler) UploadSegment(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		c.JSON(http.StatusBadRequest, protocol.SegmentUploadResponse{
			Status: protocol.SegmentUploadStatusFailed,
			Reason: "missing task_id",
		})
		return
	}

	workerID := h.state.WorkerID()
	if workerID == "" {
		c.JSON(http.StatusServiceUnavailable, protocol.SegmentUploadResponse{
			Status: protocol.SegmentUploadStatusFailed,
			Reason: "worker is not registered",
		})
		return
	}

	inputPath := h.spool.UploadPath(taskID)
	tmpPath := h.spool.TempUploadPath(taskID)
	artifactPath := h.spool.ArtifactPath(taskID)

	if err := writeRequestBody(tmpPath, c); err != nil {
		h.respondSegmentFailure(c, http.StatusInternalServerError, taskID, workerID, fmt.Sprintf("store upload failed: %v", err), false)
		return
	}
	if err := os.Rename(tmpPath, inputPath); err != nil {
		h.respondSegmentFailure(c, http.StatusInternalServerError, taskID, workerID, fmt.Sprintf("finalize upload failed: %v", err), false)
		return
	}

	if _, err := h.entry.AcceptTask(c.Request.Context(), taskID, protocol.TaskAcceptedRequest{WorkerID: workerID}); err != nil {
		h.respondSegmentFailure(c, http.StatusBadRequest, taskID, workerID, fmt.Sprintf("entry accept task failed: %v", err), false)
		return
	}

	go h.processUploadedSegment(taskID, workerID, inputPath, artifactPath)

	c.JSON(http.StatusOK, protocol.SegmentUploadResponse{Status: protocol.SegmentUploadStatusSuccess})
}

func (h *Handler) processUploadedSegment(taskID string, workerID string, inputPath string, artifactPath string) {
	ctx := context.Background()

	lease, err := h.engines.Acquire(ctx)
	if err != nil {
		h.reportSegmentFailure(ctx, taskID, workerID, fmt.Sprintf("engine slot acquire failed: %v", err), true)
		return
	}
	defer lease.Release()

	h.state.StartTask(taskID, protocol.TaskTypeProcessSegment)
	defer h.state.FinishTask(taskID)

	engineResp, err := lease.Call(ctx, engineprotocol.Request{
		Operation: engineprotocol.OpProcessSegment,
		Input: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: inputPath,
			Name: filepath.Base(inputPath),
		},
		Output: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: artifactPath,
			Name: filepath.Base(artifactPath),
		},
	})
	if err != nil {
		h.reportSegmentFailure(ctx, taskID, workerID, fmt.Sprintf("engine call failed: %v", err), true)
		return
	}
	if engineResp.Type == engineprotocol.ResponseTypeFailed {
		reason := engineResp.Reason
		if reason == "" {
			reason = "engine process_segment failed"
		}
		h.reportSegmentFailure(ctx, taskID, workerID, reason, engineResp.Retryable)
		return
	}

	if _, err := h.entry.CompleteTask(ctx, taskID, protocol.TaskCompletedRequest{
		WorkerID:        workerID,
		Checksum:        engineResp.Checksum,
		FrameCount:      engineResp.FrameCount,
		DurationMS:      engineResp.DurationMS,
		OutputSizeBytes: engineResp.OutputSizeBytes,
	}); err != nil {
		h.logger.Warn("report segment task completion failed",
			"event", "segment_task_completion_report_failed",
			"task_id", taskID,
			"worker_id", workerID,
			"error", err,
		)
	}
}

func (h *Handler) respondSegmentFailure(c *gin.Context, code int, taskID string, workerID string, reason string, retryable bool) {
	if taskID != "" && workerID != "" && h.entry != nil {
		h.reportSegmentFailure(c.Request.Context(), taskID, workerID, reason, retryable)
	}

	c.JSON(code, protocol.SegmentUploadResponse{
		Status: protocol.SegmentUploadStatusFailed,
		Reason: reason,
	})
}

func (h *Handler) reportSegmentFailure(ctx context.Context, taskID string, workerID string, reason string, retryable bool) {
	if taskID == "" || workerID == "" || h.entry == nil {
		return
	}
	if _, err := h.entry.FailTask(ctx, taskID, protocol.TaskFailedRequest{
		WorkerID:  workerID,
		Reason:    reason,
		Retryable: retryable,
	}); err != nil {
		h.logger.Warn("report segment task failure failed",
			"event", "segment_task_failure_report_failed",
			"task_id", taskID,
			"worker_id", workerID,
			"error", err,
		)
	}
}

func writeRequestBody(path string, c *gin.Context) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.ReadFrom(c.Request.Body)
	return err
}
