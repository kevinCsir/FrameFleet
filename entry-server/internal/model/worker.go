package model

import (
	"time"

	"framefleet/pkg/protocol"
)

type Worker struct {
	ID string

	Address        string
	TotalSlots     int
	SupportedTasks []protocol.TaskType

	ReportedTotalSlots int
	DiskTotalBytes     int64
	DiskFreeBytes      int64

	FreeSlots int
	Online    bool

	RunningProcessSegment int
	RunningAssembleGIF    int
	RunningTasks          []protocol.RunningTask
	Metrics               map[protocol.TaskType]protocol.TaskRunMetric

	ReservedSlots     int
	ReservedDiskBytes int64

	RegisteredAt    time.Time
	LastHeartbeatAt time.Time
	UpdatedAt       time.Time
}
