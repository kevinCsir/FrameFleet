package handlers

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

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

	go h.runAssembleGIF(req, workerID)

	c.JSON(http.StatusOK, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusSuccess})
}

func (h *Handler) runAssembleGIF(req protocol.StartAssembleGIFRequest, workerID string) {
	ctx := context.Background()

	inputs := make([]engineprotocol.FileRef, len(req.Segments))
	errs := make(chan error, len(req.Segments))
	var wg sync.WaitGroup
	for i, segment := range req.Segments {
		i := i
		segment := segment
		localPath := h.spool.AssembleArtifactPath(req.JobID, segment.TaskID)
		inputs[i] = engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: localPath,
			Name: filepath.Base(localPath),
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.peers.DownloadArtifact(ctx, segment.WorkerAddress, segment.TaskID, localPath); err != nil {
				errs <- fmt.Errorf("download artifact %s failed: %w", segment.TaskID, err)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			reason := err.Error()
			h.reportAssembleFailed(ctx, req.JobID, workerID, reason, true)
			return
		}
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
