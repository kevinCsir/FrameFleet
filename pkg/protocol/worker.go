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
	Status           RegisterWorkerStatus `json:"status"`
	WorkerID         string               `json:"worker_id,omitempty"`
	SplitPolicy      SplitPolicy          `json:"split_policy,omitempty"`
	ProcessingPolicy ProcessingPolicy     `json:"processing_policy,omitempty"`
}

type SplitPolicy struct {
	TargetSegmentSizeBytes  int64 `json:"target_segment_size_bytes,omitempty"`
	TargetSegmentDurationMS int64 `json:"target_segment_duration_ms,omitempty"`
	MaxSegments             int   `json:"max_segments,omitempty"`
}

type ProcessingPolicy struct {
	CannyLowThreshold  int             `json:"canny_low_threshold,omitempty"`
	CannyHighThreshold int             `json:"canny_high_threshold,omitempty"`
	AssembleMode       GIFAssembleMode `json:"assemble_mode,omitempty"`
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
	Status             HeartbeatWorkerStatus `json:"status"`
	GlobalBackpressure BackpressureStatus    `json:"global_backpressure,omitempty"`
}

type BackpressureStatus struct {
	Active                  bool   `json:"active"`
	Reason                  string `json:"reason,omitempty"`
	BusyThresholdMultiplier int    `json:"busy_threshold_multiplier,omitempty"`
	ObservedWorkerCount     int    `json:"observed_worker_count,omitempty"`
}

type TaskRunMetric struct {
	CompletedCount  int64 `json:"completed_count" binding:"min=0"`
	TotalDurationMS int64 `json:"total_duration_ms" binding:"min=0"`
}
