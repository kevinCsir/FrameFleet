package handlers

import (
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

	inputs := make([]engineprotocol.FileRef, 0, len(req.Segments))
	for _, segment := range req.Segments {
		localPath := h.spool.AssembleArtifactPath(req.JobID, segment.TaskID)
		if err := h.peers.DownloadArtifact(c.Request.Context(), segment.WorkerAddress, segment.TaskID, localPath); err != nil {
			reason := fmt.Sprintf("download artifact %s failed: %v", segment.TaskID, err)
			h.reportAssembleFailed(c, req.JobID, workerID, reason, true)
			c.JSON(http.StatusInternalServerError, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusFailed})
			return
		}
		inputs = append(inputs, engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: localPath,
			Name: filepath.Base(localPath),
		})
	}

	resultName := req.JobID + ".gif"
	outputPath := h.spool.ResultPath(resultName)

	h.state.StartTask(req.AssembleTaskID, protocol.TaskTypeAssembleGIF)
	defer h.state.FinishTask(req.AssembleTaskID)

	engineResp, err := h.engines.Call(c.Request.Context(), engineprotocol.Request{
		Operation: engineprotocol.OpAssembleGIF,
		JobID:     req.JobID,
		TaskID:    req.AssembleTaskID,
		Inputs:    inputs,
		Output: &engineprotocol.FileRef{
			Mode: engineprotocol.DataModeFile,
			Path: outputPath,
			Name: resultName,
		},
	})
	if err != nil {
		reason := fmt.Sprintf("engine assemble_gif failed: %v", err)
		h.reportAssembleFailed(c, req.JobID, workerID, reason, true)
		c.JSON(http.StatusInternalServerError, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusFailed})
		return
	}
	if engineResp.Type == engineprotocol.ResponseTypeFailed {
		reason := engineResp.Reason
		if reason == "" {
			reason = "engine assemble_gif failed"
		}
		h.reportAssembleFailed(c, req.JobID, workerID, reason, engineResp.Retryable)
		c.JSON(http.StatusInternalServerError, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusFailed})
		return
	}

	if _, err := h.entry.ReportAssembled(c.Request.Context(), req.JobID, protocol.JobAssembledRequest{
		WorkerID:        workerID,
		Status:          protocol.JobResultStatusSuccess,
		ResultName:      resultName,
		Checksum:        engineResp.Checksum,
		DurationMS:      engineResp.DurationMS,
		OutputSizeBytes: engineResp.OutputSizeBytes,
	}); err != nil {
		h.logger.Warn("report assembled success failed", "event", "report_assembled_success_failed", "job_id", req.JobID, "error", err)
		c.JSON(http.StatusInternalServerError, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusFailed})
		return
	}

	c.JSON(http.StatusOK, protocol.StartAssembleGIFResponse{Status: protocol.StartAssembleGIFStatusSuccess})
}

func (h *Handler) reportAssembleFailed(c *gin.Context, jobID string, workerID string, reason string, retryable bool) {
	if jobID == "" || workerID == "" || h.entry == nil {
		return
	}
	if _, err := h.entry.ReportAssembled(c.Request.Context(), jobID, protocol.JobAssembledRequest{
		WorkerID:  workerID,
		Status:    protocol.JobResultStatusFailed,
		Reason:    reason,
		Retryable: retryable,
	}); err != nil {
		h.logger.Warn("report assembled failure failed", "event", "report_assembled_failure_failed", "job_id", jobID, "error", err)
	}
}
