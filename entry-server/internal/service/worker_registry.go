package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"framefleet/entry-server/internal/model"
)

var (
	ErrInvalidWorkerAddress = errors.New("invalid worker address")
	ErrInvalidWorkerTask    = errors.New("invalid worker supported task")
)

type WorkerRegistry struct {
	mu sync.RWMutex

	byID     map[string]*model.Worker
	idByAddr map[string]string
	addrByID map[string]string
}

func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		byID:     make(map[string]*model.Worker),
		idByAddr: make(map[string]string),
		addrByID: make(map[string]string),
	}
}

func (r *WorkerRegistry) RegisterWorker(req model.RegisterWorkerRequest) (*model.Worker, bool, error) {
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
		worker.FreeSlots = req.TotalSlots - worker.RunningProcessSegment - worker.RunningAssembleGIF
		if worker.FreeSlots < 0 {
			worker.FreeSlots = 0
		}
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
		ID:              id,
		Address:         req.Address,
		TotalSlots:      req.TotalSlots,
		SupportedTasks:  cloneTaskTypes(req.SupportedTasks),
		FreeSlots:       req.TotalSlots,
		Online:          true,
		RegisteredAt:    now,
		LastHeartbeatAt: now,
		UpdatedAt:       now,
	}

	r.byID[id] = worker
	r.idByAddr[req.Address] = id
	r.addrByID[id] = req.Address

	return worker, false, nil
}

func (r *WorkerRegistry) GetWorker(id string) (*model.Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	worker, ok := r.byID[id]
	return worker, ok
}

func (r *WorkerRegistry) PickBestWorker(taskType model.TaskType) (*model.Worker, bool) {
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
		return nil, false
	}
	return best, true
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

func validateRegisterWorkerRequest(req model.RegisterWorkerRequest) error {
	if _, _, err := net.SplitHostPort(req.Address); err != nil {
		return ErrInvalidWorkerAddress
	}

	for _, taskType := range req.SupportedTasks {
		if !model.IsSupportedTaskType(taskType) {
			return ErrInvalidWorkerTask
		}
	}

	return nil
}

func cloneTaskTypes(tasks []model.TaskType) []model.TaskType {
	cloned := make([]model.TaskType, len(tasks))
	copy(cloned, tasks)
	return cloned
}

func NormalizeAddress(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
