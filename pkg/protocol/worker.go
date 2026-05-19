package protocol

type RegisterWorkerStatus string

const (
	RegisterWorkerStatusSuccess RegisterWorkerStatus = "success"
	RegisterWorkerStatusFailed  RegisterWorkerStatus = "failed"
	RegisterWorkerStatusExists  RegisterWorkerStatus = "exists"
)

type HeartbeatWorkerStatus string

const (
	HeartbeatWorkerStatusSuccess  HeartbeatWorkerStatus = "success"
	HeartbeatWorkerStatusFailed   HeartbeatWorkerStatus = "failed"
	HeartbeatWorkerStatusNotFound HeartbeatWorkerStatus = "not_found"
)

type RegisterWorkerRequest struct {
	Address        string     `json:"address" binding:"required"`
	TotalSlots     int        `json:"total_slots" binding:"required,min=1"`
	SupportedTasks []TaskType `json:"supported_tasks" binding:"required,min=1"`
	DiskTotalBytes int64      `json:"disk_total_bytes" binding:"required,min=0"`
	DiskFreeBytes  int64      `json:"disk_free_bytes" binding:"required,min=0"`
}

type RegisterWorkerResponse struct {
	Status   RegisterWorkerStatus `json:"status"`
	WorkerID string               `json:"worker_id,omitempty"`
}

type HeartbeatWorkerRequest struct {
	WorkerID              string                     `json:"worker_id" binding:"required"`
	TotalSlots            int                        `json:"total_slots" binding:"required,min=1"`
	RunningProcessSegment int                        `json:"running_process_segment" binding:"min=0"`
	RunningAssembleGIF    int                        `json:"running_assemble_gif" binding:"min=0"`
	RunningTasks          []RunningTask              `json:"running_tasks,omitempty"`
	DiskTotalBytes        int64                      `json:"disk_total_bytes" binding:"required,min=0"`
	DiskFreeBytes         int64                      `json:"disk_free_bytes" binding:"required,min=0"`
	Metrics               map[TaskType]TaskRunMetric `json:"metrics,omitempty"`
}

type HeartbeatWorkerResponse struct {
	Status HeartbeatWorkerStatus `json:"status"`
}

type TaskRunMetric struct {
	CompletedCount  int64 `json:"completed_count" binding:"min=0"`
	TotalDurationMS int64 `json:"total_duration_ms" binding:"min=0"`
}
