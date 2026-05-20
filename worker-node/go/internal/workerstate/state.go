package workerstate

import (
	"sync"

	"framefleet/pkg/protocol"
	"framefleet/worker-node/go/internal/diskusage"
)

type Config struct {
	TotalSlots     int
	DiskTotalBytes int64
	DiskFreeBytes  int64
	DiskObserver   *diskusage.Observer
}

type State struct {
	mu sync.RWMutex

	cfg          Config
	workerID     string
	splitPolicy  protocol.SplitPolicy
	backpressure protocol.BackpressureStatus
	runningTasks map[string]protocol.TaskType
}

func New(cfg Config) *State {
	return &State{cfg: cfg, runningTasks: make(map[string]protocol.TaskType)}
}

func (s *State) SetRegistration(workerID string, splitPolicy protocol.SplitPolicy) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.workerID = workerID
	s.splitPolicy = splitPolicy
}

func (s *State) WorkerID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workerID
}

func (s *State) SplitPolicy() protocol.SplitPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.splitPolicy
}

func (s *State) SetBackpressure(status protocol.BackpressureStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backpressure = status
}

func (s *State) BackpressureActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.backpressure.Active
}

func (s *State) StartTask(taskID string, taskType protocol.TaskType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runningTasks[taskID] = taskType
}

func (s *State) FinishTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runningTasks, taskID)
}

func (s *State) HeartbeatRequest() protocol.HeartbeatWorkerRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runningTasks := make([]protocol.RunningTask, 0, len(s.runningTasks))
	runningProcessSegment := 0
	runningAssembleGIF := 0
	for taskID, taskType := range s.runningTasks {
		runningTasks = append(runningTasks, protocol.RunningTask{TaskID: taskID, TaskType: taskType})
		switch taskType {
		case protocol.TaskTypeProcessSegment:
			runningProcessSegment++
		case protocol.TaskTypeAssembleGIF:
			runningAssembleGIF++
		}
	}

	diskTotalBytes, diskFreeBytes := s.diskUsageLocked()

	return protocol.HeartbeatWorkerRequest{
		WorkerID:              s.workerID,
		TotalSlots:            s.cfg.TotalSlots,
		RunningProcessSegment: runningProcessSegment,
		RunningAssembleGIF:    runningAssembleGIF,
		RunningTasks:          runningTasks,
		DiskTotalBytes:        diskTotalBytes,
		DiskFreeBytes:         diskFreeBytes,
		Metrics:               map[protocol.TaskType]protocol.TaskRunMetric{},
	}
}

func (s *State) DiskUsage() diskusage.Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalBytes, freeBytes := s.diskUsageLocked()
	return diskusage.Usage{TotalBytes: totalBytes, FreeBytes: freeBytes}
}

func (s *State) diskUsageLocked() (int64, int64) {
	if s.cfg.DiskObserver != nil {
		if usage, err := s.cfg.DiskObserver.Usage(); err == nil {
			return usage.TotalBytes, usage.FreeBytes
		}
	}
	return s.cfg.DiskTotalBytes, s.cfg.DiskFreeBytes
}
