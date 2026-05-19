package protocol

type CreateJobStatus string

const (
	CreateJobStatusSuccess               CreateJobStatus = "success"
	CreateJobStatusAlreadyExists         CreateJobStatus = "already_exists"
	CreateJobStatusInsufficientResources CreateJobStatus = "insufficient_resources"
	CreateJobStatusFailed                CreateJobStatus = "failed"
	CreateJobStatusNotFound              CreateJobStatus = "not_found"
)

type JobMode string

const (
	JobModeInternal JobMode = "internal"
	JobModeExternal JobMode = "external"
)

type JobStatus string

const (
	JobStatusSegmentAssigned  JobStatus = "segment_assigned"
	JobStatusSegmentRunning   JobStatus = "segment_running"
	JobStatusSegmentCompleted JobStatus = "segment_completed"
	JobStatusAssembleAssigned JobStatus = "assemble_assigned"
	JobStatusAssembleRunning  JobStatus = "assemble_running"
	JobStatusCompleted        JobStatus = "completed"
	JobStatusFailed           JobStatus = "failed"
)

type JobResultStatus string

const (
	JobResultStatusSuccess JobResultStatus = "success"
	JobResultStatusFailed  JobResultStatus = "failed"
)

type JobResultUpdateStatus string

const (
	JobResultUpdateStatusSuccess        JobResultUpdateStatus = "success"
	JobResultUpdateStatusFailed         JobResultUpdateStatus = "failed"
	JobResultUpdateStatusNotFound       JobResultUpdateStatus = "not_found"
	JobResultUpdateStatusWorkerMismatch JobResultUpdateStatus = "worker_mismatch"
	JobResultUpdateStatusInvalidState   JobResultUpdateStatus = "invalid_state"
)

type JobResultNotifyStatus string

const (
	JobResultNotifyStatusSuccess JobResultNotifyStatus = "success"
	JobResultNotifyStatusFailed  JobResultNotifyStatus = "failed"
)

type QueryJobResultStatus string

const (
	QueryJobResultStatusSuccess  QueryJobResultStatus = "success"
	QueryJobResultStatusNotFound QueryJobResultStatus = "not_found"
	QueryJobResultStatusFailed   QueryJobResultStatus = "failed"
)

type CreateJobRequest struct {
	WorkerID       string  `json:"worker_id" binding:"required"`
	VideoName      string  `json:"video_name" binding:"required"`
	SegmentCount   int     `json:"segment_count" binding:"required,min=1"`
	TotalSizeBytes int64   `json:"total_size_bytes" binding:"required,min=0"`
	Mode           JobMode `json:"mode" binding:"required"`
}

type CreateJobResponse struct {
	Status         CreateJobStatus  `json:"status"`
	JobID          string           `json:"job_id,omitempty"`
	JobStatus      JobStatus        `json:"job_status,omitempty"`
	AlreadyExists  bool             `json:"already_exists,omitempty"`
	RequiredSlots  int              `json:"required_slots"`
	AvailableSlots int              `json:"available_slots"`
	Assignments    []TaskAssignment `json:"assignments,omitempty"`
}

type TaskAssignment struct {
	SegmentIndex int    `json:"segment_index"`
	TaskID       string `json:"task_id,omitempty"`
	WorkerID     string `json:"worker_id"`
	Address      string `json:"address"`
}

type JobAssembledRequest struct {
	WorkerID        string          `json:"worker_id" binding:"required"`
	Status          JobResultStatus `json:"status" binding:"required"`
	ResultName      string          `json:"result_name,omitempty"`
	Checksum        string          `json:"checksum,omitempty"`
	DurationMS      int64           `json:"duration_ms" binding:"min=0"`
	OutputSizeBytes int64           `json:"output_size_bytes" binding:"min=0"`
	Reason          string          `json:"reason,omitempty"`
	Retryable       bool            `json:"retryable"`
}

type JobAssembledResponse struct {
	Status JobResultUpdateStatus `json:"status"`
}

type JobResultNotificationRequest struct {
	JobID               string          `json:"job_id" binding:"required"`
	VideoName           string          `json:"video_name" binding:"required"`
	Status              JobResultStatus `json:"status" binding:"required"`
	ResultWorkerID      string          `json:"result_worker_id,omitempty"`
	ResultWorkerAddress string          `json:"result_worker_address,omitempty"`
	ResultName          string          `json:"result_name,omitempty"`
	ResultURI           string          `json:"result_uri,omitempty"`
	Checksum            string          `json:"checksum,omitempty"`
	OutputSizeBytes     int64           `json:"output_size_bytes,omitempty"`
	Reason              string          `json:"reason,omitempty"`
	Retryable           bool            `json:"retryable,omitempty"`
}

type JobResultNotificationResponse struct {
	Status JobResultNotifyStatus `json:"status"`
}

type QueryJobResultResponse struct {
	Status    QueryJobResultStatus `json:"status"`
	JobID     string               `json:"job_id,omitempty"`
	JobStatus JobStatus            `json:"job_status,omitempty"`
	VideoName string               `json:"video_name,omitempty"`
	Mode      JobMode              `json:"mode,omitempty"`
	Result    *JobResultInfo       `json:"result,omitempty"`
	Failure   *JobFailureInfo      `json:"failure,omitempty"`
}

type JobResultInfo struct {
	WorkerID        string `json:"worker_id"`
	WorkerAddress   string `json:"worker_address"`
	Name            string `json:"name"`
	URI             string `json:"uri"`
	Checksum        string `json:"checksum,omitempty"`
	OutputSizeBytes int64  `json:"output_size_bytes,omitempty"`
}

type JobFailureInfo struct {
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable"`
}
