package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"framefleet/pkg/protocol"
)

func (h *Handler) JobResultNotification(c *gin.Context) {
	var req protocol.JobResultNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, protocol.JobResultNotificationResponse{Status: protocol.JobResultNotifyStatusFailed})
		return
	}

	args := []any{
		"event", "job_result_notification_received",
		"job_id", req.JobID,
		"video_name", req.VideoName,
		"status", req.Status,
		"result_worker_id", req.ResultWorkerID,
		"result_worker_address", req.ResultWorkerAddress,
		"result_name", req.ResultName,
		"result_uri", req.ResultURI,
		"checksum", req.Checksum,
		"output_size_bytes", req.OutputSizeBytes,
		"reason", req.Reason,
		"retryable", req.Retryable,
	}
	if req.Status == protocol.JobResultStatusSuccess && req.ResultWorkerAddress != "" && req.ResultName != "" {
		args = append(args, "download_url", "http://"+req.ResultWorkerAddress+"/results/"+req.ResultName)
	}

	h.logger.Info("job result notification received", args...)
	c.JSON(http.StatusOK, protocol.JobResultNotificationResponse{Status: protocol.JobResultNotifyStatusSuccess})
}
