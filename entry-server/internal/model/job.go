package model

import (
	"time"

	"framefleet/pkg/protocol"
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

type TaskStatus string

const (
	TaskStatusAssigned  TaskStatus = "assigned"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

type Job struct {
	ID                  string
	SourceWorkerID      string
	SourceWorkerAddress string
	VideoName           string
	SegmentCount        int
	TotalSizeBytes      int64
	Mode                protocol.JobMode
	IdentityKey         string
	Status              JobStatus
	TaskIDs             []string
	ResultWorkerID      string
	ResultWorkerAddress string
	ResultName          string
	ResultURI           string
	Checksum            string
	DurationMS          int64
	OutputSizeBytes     int64
	FailureReason       string
	Retryable           bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
	FailedAt            *time.Time
}

type Task struct {
	ID                    string
	JobID                 string
	Type                  protocol.TaskType
	SegmentIndex          int
	ProcessingMode        protocol.TaskProcessingMode
	AssignedWorkerID      string
	AssignedWorkerAddress string
	Status                TaskStatus
	RequiredDiskBytes     int64
	Checksum              string
	FrameCount            int
	DurationMS            int64
	OutputSizeBytes       int64
	FailureReason         string
	Retryable             bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	AcceptedAt            *time.Time
	CompletedAt           *time.Time
	FailedAt              *time.Time
}
