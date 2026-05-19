package protocol

type TaskType string

const (
	TaskTypeProcessSegment TaskType = "process_segment"
	TaskTypeAssembleGIF    TaskType = "assemble_gif"
)

func IsSupportedTaskType(taskType TaskType) bool {
	switch taskType {
	case TaskTypeProcessSegment, TaskTypeAssembleGIF:
		return true
	default:
		return false
	}
}

type TaskUpdateStatus string

const (
	TaskUpdateStatusSuccess        TaskUpdateStatus = "success"
	TaskUpdateStatusFailed         TaskUpdateStatus = "failed"
	TaskUpdateStatusNotFound       TaskUpdateStatus = "not_found"
	TaskUpdateStatusWorkerMismatch TaskUpdateStatus = "worker_mismatch"
	TaskUpdateStatusInvalidState   TaskUpdateStatus = "invalid_state"
)

type RunningTask struct {
	TaskID   string   `json:"task_id" binding:"required"`
	TaskType TaskType `json:"task_type" binding:"required"`
}

type TaskAcceptedRequest struct {
	WorkerID string `json:"worker_id" binding:"required"`
}

type TaskCompletedRequest struct {
	WorkerID        string `json:"worker_id" binding:"required"`
	Checksum        string `json:"checksum,omitempty"`
	FrameCount      int    `json:"frame_count" binding:"min=0"`
	DurationMS      int64  `json:"duration_ms" binding:"min=0"`
	OutputSizeBytes int64  `json:"output_size_bytes" binding:"min=0"`
}

type TaskFailedRequest struct {
	WorkerID  string `json:"worker_id" binding:"required"`
	Reason    string `json:"reason" binding:"required"`
	Retryable bool   `json:"retryable"`
}

type TaskUpdateResponse struct {
	Status TaskUpdateStatus `json:"status"`
}
