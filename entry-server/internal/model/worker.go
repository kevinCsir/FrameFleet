package model

import "time"

type RegisterWorkerStatus string

const (
	RegisterWorkerStatusSuccess RegisterWorkerStatus = "success"
	RegisterWorkerStatusFailed  RegisterWorkerStatus = "failed"
	RegisterWorkerStatusExists  RegisterWorkerStatus = "exists"
)

type RegisterWorkerRequest struct {
	Address        string     `json:"address" binding:"required"`
	TotalSlots     int        `json:"total_slots" binding:"required,min=1"`
	SupportedTasks []TaskType `json:"supported_tasks" binding:"required,min=1"`
}

type RegisterWorkerResponse struct {
	Status   RegisterWorkerStatus `json:"status"`
	WorkerID string               `json:"worker_id,omitempty"`
}

type Worker struct {
	ID string

	Address        string
	TotalSlots     int
	SupportedTasks []TaskType

	FreeSlots int
	Online    bool

	RunningProcessSegment int
	RunningAssembleGIF    int

	RegisteredAt    time.Time
	LastHeartbeatAt time.Time
	UpdatedAt       time.Time
}
