package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/model"
	"framefleet/pkg/protocol"
)

var (
	ErrInvalidWorkerAddress = errors.New("invalid worker address")
	ErrInvalidWorkerTask    = errors.New("invalid worker supported task")
	ErrWorkerNotFound       = errors.New("worker not found")
)

type WorkerRegistry struct {
	mu sync.RWMutex

	logger *logger.Logger

	byID     map[string]*model.Worker
	idByAddr map[string]string
	addrByID map[string]string
}

func NewWorkerRegistry(appLogger *logger.Logger) *WorkerRegistry {
	return &WorkerRegistry{
		logger:   appLogger,
		byID:     make(map[string]*model.Worker),
		idByAddr: make(map[string]string),
		addrByID: make(map[string]string),
	}
}

func (r *WorkerRegistry) RegisterWorker(req protocol.RegisterWorkerRequest) (*model.Worker, bool, error) {
	if err := validateRegisterWorkerRequest(req); err != nil {
		return nil, false, err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	if id, ok := r.idByAddr[req.Address]; ok {
		worker := r.byID[id]
		worker.TotalSlots = req.TotalSlots
		worker.SupportedTasks = cloneTaskTypes(req.SupportedTasks)
		worker.DiskTotalBytes = req.DiskTotalBytes
		worker.DiskFreeBytes = req.DiskFreeBytes
		worker.FreeSlots = calculateFreeSlots(req.TotalSlots, worker.ReservedSlots)
		worker.Online = true
		worker.LastHeartbeatAt = now
		worker.UpdatedAt = now

		return worker, true, nil
	}

	id, err := r.nextWorkerIDLocked()
	if err != nil {
		return nil, false, err
	}

	worker := &model.Worker{
		ID:                 id,
		Address:            req.Address,
		TotalSlots:         req.TotalSlots,
		SupportedTasks:     cloneTaskTypes(req.SupportedTasks),
		ReportedTotalSlots: req.TotalSlots,
		DiskTotalBytes:     req.DiskTotalBytes,
		DiskFreeBytes:      req.DiskFreeBytes,
		FreeSlots:          req.TotalSlots,
		Online:             true,
		Metrics:            make(map[protocol.TaskType]protocol.TaskRunMetric),
		RegisteredAt:       now,
		LastHeartbeatAt:    now,
		UpdatedAt:          now,
	}

	r.byID[id] = worker
	r.idByAddr[req.Address] = id
	r.addrByID[id] = req.Address

	return worker, false, nil
}

func (r *WorkerRegistry) HeartbeatWorker(req protocol.HeartbeatWorkerRequest) error {
	if err := validateHeartbeatWorkerRequest(req); err != nil {
		return err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	worker, ok := r.byID[req.WorkerID]
	if !ok {
		return ErrWorkerNotFound
	}

	worker.ReportedTotalSlots = req.TotalSlots
	worker.RunningProcessSegment = req.RunningProcessSegment
	worker.RunningAssembleGIF = req.RunningAssembleGIF
	worker.RunningTasks = cloneRunningTasks(req.RunningTasks)
	worker.FreeSlots = calculateFreeSlots(worker.TotalSlots, worker.ReservedSlots)
	worker.DiskTotalBytes = req.DiskTotalBytes
	worker.DiskFreeBytes = req.DiskFreeBytes
	worker.Metrics = cloneMetrics(req.Metrics)
	worker.Online = true
	worker.LastHeartbeatAt = now
	worker.UpdatedAt = now

	return nil
}

func (r *WorkerRegistry) MarkExpiredWorkers(timeout time.Duration) int {
	now := time.Now().UTC()
	expired := 0

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, worker := range r.byID {
		if worker.Online && now.Sub(worker.LastHeartbeatAt) > timeout {
			worker.Online = false
			worker.FreeSlots = 0
			worker.UpdatedAt = now
			expired++
			r.logger.Warn("worker expired",
				"event", "worker_expired",
				"worker_id", worker.ID,
				"address", worker.Address,
				"last_heartbeat_at", worker.LastHeartbeatAt.Format(time.RFC3339Nano),
				"timeout_seconds", int(timeout.Seconds()),
			)
		}
	}

	return expired
}

func (r *WorkerRegistry) GetWorker(id string) (model.Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	worker, ok := r.byID[id]
	if !ok {
		return model.Worker{}, false
	}
	return cloneWorker(worker), true
}

func (r *WorkerRegistry) PickBestWorker(taskType protocol.TaskType) (model.Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best *model.Worker
	for _, worker := range r.byID {
		if !worker.Online || worker.FreeSlots <= 0 || !worker.Supports(taskType) {
			continue
		}
		if best == nil || worker.FreeSlots > best.FreeSlots {
			best = worker
		}
	}

	if best == nil {
		return model.Worker{}, false
	}
	return cloneWorker(best), true
}

func (r *WorkerRegistry) PickWorkersForReservation(taskType protocol.TaskType, count int) ([]model.Worker, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if count <= 0 {
		return nil, true
	}

	selected := make([]*model.Worker, 0, count)
	for len(selected) < count {
		var best *model.Worker
		for _, worker := range r.byID {
			if !worker.Online || worker.FreeSlots <= 0 || !worker.Supports(taskType) {
				continue
			}
			if best == nil || worker.FreeSlots > best.FreeSlots {
				best = worker
			}
		}
		if best == nil {
			for _, worker := range selected {
				worker.ReservedSlots--
				worker.FreeSlots = calculateFreeSlots(worker.TotalSlots, worker.ReservedSlots)
			}
			return cloneWorkers(selected), false
		}

		best.ReservedSlots++
		best.FreeSlots = calculateFreeSlots(best.TotalSlots, best.ReservedSlots)
		best.UpdatedAt = time.Now().UTC()
		selected = append(selected, best)
	}

	return cloneWorkers(selected), true
}

func (r *WorkerRegistry) ReserveWorkerSlots(workerID string, taskType protocol.TaskType, count int) ([]model.Worker, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if count <= 0 {
		return nil, true
	}

	worker, ok := r.byID[workerID]
	if !ok || !worker.Online || !worker.Supports(taskType) || worker.FreeSlots < count {
		return nil, false
	}

	worker.ReservedSlots += count
	worker.FreeSlots = calculateFreeSlots(worker.TotalSlots, worker.ReservedSlots)
	worker.UpdatedAt = time.Now().UTC()

	selected := make([]*model.Worker, count)
	for i := 0; i < count; i++ {
		selected[i] = worker
	}

	return cloneWorkers(selected), true
}

func (r *WorkerRegistry) PickWorkerForDiskReservation(taskType protocol.TaskType, requiredDiskBytes int64) (model.Worker, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var best *model.Worker
	var bestSchedulableDisk int64

	for _, worker := range r.byID {
		if !worker.Online || worker.FreeSlots <= 0 || !worker.Supports(taskType) {
			continue
		}

		schedulableDisk := worker.DiskFreeBytes - worker.ReservedDiskBytes
		if schedulableDisk < requiredDiskBytes {
			continue
		}

		if best == nil ||
			worker.FreeSlots > best.FreeSlots ||
			(worker.FreeSlots == best.FreeSlots && schedulableDisk > bestSchedulableDisk) {
			best = worker
			bestSchedulableDisk = schedulableDisk
		}
	}

	if best == nil {
		return model.Worker{}, false
	}

	best.ReservedSlots++
	best.ReservedDiskBytes += requiredDiskBytes
	best.FreeSlots = calculateFreeSlots(best.TotalSlots, best.ReservedSlots)
	best.UpdatedAt = time.Now().UTC()

	return cloneWorker(best), true
}

func (r *WorkerRegistry) ReleaseReservations(workerIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, workerID := range workerIDs {
		worker, ok := r.byID[workerID]
		if !ok {
			continue
		}
		if worker.ReservedSlots > 0 {
			worker.ReservedSlots--
		}
		worker.FreeSlots = calculateFreeSlots(worker.TotalSlots, worker.ReservedSlots)
		worker.UpdatedAt = time.Now().UTC()
	}
}

func (r *WorkerRegistry) ReleaseReservation(workerID string, diskBytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	worker, ok := r.byID[workerID]
	if !ok {
		return
	}

	if worker.ReservedSlots > 0 {
		worker.ReservedSlots--
	}
	if diskBytes > 0 {
		worker.ReservedDiskBytes -= diskBytes
		if worker.ReservedDiskBytes < 0 {
			worker.ReservedDiskBytes = 0
		}
	}
	worker.FreeSlots = calculateFreeSlots(worker.TotalSlots, worker.ReservedSlots)
	worker.UpdatedAt = time.Now().UTC()
}

func (r *WorkerRegistry) UpdateWorkerObservedDiskFree(workerID string, diskFreeBytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	worker, ok := r.byID[workerID]
	if !ok {
		return
	}

	worker.DiskFreeBytes = diskFreeBytes
	worker.UpdatedAt = time.Now().UTC()
}

func (r *WorkerRegistry) nextWorkerIDLocked() (string, error) {
	for {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}

		id := "wrk_" + hex.EncodeToString(buf)
		if _, exists := r.byID[id]; !exists {
			return id, nil
		}
	}
}

func validateRegisterWorkerRequest(req protocol.RegisterWorkerRequest) error {
	if _, _, err := net.SplitHostPort(req.Address); err != nil {
		return ErrInvalidWorkerAddress
	}

	for _, taskType := range req.SupportedTasks {
		if !protocol.IsSupportedTaskType(taskType) {
			return ErrInvalidWorkerTask
		}
	}

	return nil
}

func validateHeartbeatWorkerRequest(req protocol.HeartbeatWorkerRequest) error {
	for taskType := range req.Metrics {
		if !protocol.IsSupportedTaskType(taskType) {
			return ErrInvalidWorkerTask
		}
	}

	for _, task := range req.RunningTasks {
		if !protocol.IsSupportedTaskType(task.TaskType) {
			return ErrInvalidWorkerTask
		}
	}

	return nil
}

func calculateFreeSlots(totalSlots, reservedSlots int) int {
	freeSlots := totalSlots - reservedSlots
	if freeSlots < 0 {
		return 0
	}
	return freeSlots
}

func cloneTaskTypes(tasks []protocol.TaskType) []protocol.TaskType {
	cloned := make([]protocol.TaskType, len(tasks))
	copy(cloned, tasks)
	return cloned
}

func cloneRunningTasks(tasks []protocol.RunningTask) []protocol.RunningTask {
	cloned := make([]protocol.RunningTask, len(tasks))
	copy(cloned, tasks)
	return cloned
}

func cloneMetrics(metrics map[protocol.TaskType]protocol.TaskRunMetric) map[protocol.TaskType]protocol.TaskRunMetric {
	cloned := make(map[protocol.TaskType]protocol.TaskRunMetric, len(metrics))
	for taskType, metric := range metrics {
		cloned[taskType] = metric
	}
	return cloned
}

func cloneWorker(worker *model.Worker) model.Worker {
	cloned := *worker
	cloned.SupportedTasks = cloneTaskTypes(worker.SupportedTasks)
	cloned.RunningTasks = cloneRunningTasks(worker.RunningTasks)
	cloned.Metrics = cloneMetrics(worker.Metrics)
	return cloned
}

func cloneWorkers(workers []*model.Worker) []model.Worker {
	cloned := make([]model.Worker, len(workers))
	for i, worker := range workers {
		cloned[i] = cloneWorker(worker)
	}
	return cloned
}

func NormalizeAddress(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
