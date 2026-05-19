package protocol

type StartAssembleGIFStatus string

const (
	StartAssembleGIFStatusSuccess             StartAssembleGIFStatus = "success"
	StartAssembleGIFStatusFailed              StartAssembleGIFStatus = "failed"
	StartAssembleGIFStatusInsufficientStorage StartAssembleGIFStatus = "insufficient_storage"
	StartAssembleGIFStatusInvalidRequest      StartAssembleGIFStatus = "invalid_request"
)

type StartAssembleGIFRequest struct {
	JobID          string               `json:"job_id" binding:"required"`
	AssembleTaskID string               `json:"assemble_task_id" binding:"required"`
	Video          AssembleVideoInfo    `json:"video" binding:"required"`
	Segments       []AssembleSegmentRef `json:"segments" binding:"required,min=1"`
}

type AssembleVideoInfo struct {
	Name                string `json:"name" binding:"required"`
	SourceWorkerID      string `json:"source_worker_id" binding:"required"`
	SourceWorkerAddress string `json:"source_worker_address" binding:"required"`
	TotalSizeBytes      int64  `json:"total_size_bytes" binding:"required,min=0"`
}

type AssembleSegmentRef struct {
	SegmentIndex    int    `json:"segment_index" binding:"min=0"`
	TaskID          string `json:"task_id" binding:"required"`
	WorkerID        string `json:"worker_id" binding:"required"`
	WorkerAddress   string `json:"worker_address" binding:"required"`
	Checksum        string `json:"checksum,omitempty"`
	FrameCount      int    `json:"frame_count" binding:"min=0"`
	OutputSizeBytes int64  `json:"output_size_bytes" binding:"min=0"`
}

type StartAssembleGIFResponse struct {
	Status        StartAssembleGIFStatus `json:"status"`
	DiskFreeBytes int64                  `json:"disk_free_bytes,omitempty"`
}
