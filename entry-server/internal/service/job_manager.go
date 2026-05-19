package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"framefleet/entry-server/internal/logger"
	"framefleet/entry-server/internal/model"
	"framefleet/pkg/protocol"
)

var ErrInsufficientResources = errors.New("insufficient worker resources")

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrTaskWorkerMismatch = errors.New("task worker mismatch")
	ErrTaskInvalidState   = errors.New("task invalid state")
)

type JobManager struct {
	mu sync.RWMutex

	logger  *logger.Logger
	workers *WorkerRegistry
	client  *http.Client

	jobs            map[string]*model.Job
	tasks           map[string]*model.Task
	jobIDByIdentity map[string]string
}

type CreateJobResult struct {
	JobID          string
	RequiredSlots  int
	AvailableSlots int
	Assignments    []protocol.TaskAssignment
	AlreadyExists  bool
	JobStatus      protocol.JobStatus
}

func NewJobManager(workers *WorkerRegistry, appLogger *logger.Logger) *JobManager {
	return &JobManager{
		logger:          appLogger,
		workers:         workers,
		client:          &http.Client{Timeout: 5 * time.Second},
		jobs:            make(map[string]*model.Job),
		tasks:           make(map[string]*model.Task),
		jobIDByIdentity: make(map[string]string),
	}
}

func (m *JobManager) CreateJob(req protocol.CreateJobRequest) (CreateJobResult, error) {
	if req.Mode != protocol.JobModeInternal && req.Mode != protocol.JobModeExternal {
		return CreateJobResult{}, ErrTaskInvalidState
	}

	sourceWorker, ok := m.workers.GetWorker(req.WorkerID)
	if !ok {
		return CreateJobResult{}, ErrWorkerNotFound
	}

	identityKey := jobIdentityKey(sourceWorker.Address, req.VideoName)
	if existing, ok := m.findJobByIdentity(identityKey); ok {
		return CreateJobResult{
			JobID:         existing.ID,
			RequiredSlots: req.SegmentCount,
			AlreadyExists: true,
			JobStatus:     protocol.JobStatus(existing.Status),
		}, nil
	}

	if req.Mode == protocol.JobModeInternal {
		return m.createInternalJob(req, sourceWorker, identityKey)
	}

	return m.createExternalJob(req, identityKey)
}

func (m *JobManager) createExternalJob(req protocol.CreateJobRequest, identityKey string) (CreateJobResult, error) {
	selectedWorkers, enough := m.workers.PickWorkersForReservation(protocol.TaskTypeProcessSegment, req.SegmentCount)
	if !enough {
		return CreateJobResult{
			RequiredSlots:  req.SegmentCount,
			AvailableSlots: len(selectedWorkers),
			Assignments:    previewAssignments(selectedWorkers),
		}, ErrInsufficientResources
	}

	sourceWorker, _ := m.workers.GetWorker(req.WorkerID)
	return m.createJobWithAssignments(req, sourceWorker, selectedWorkers, identityKey)
}

func (m *JobManager) createInternalJob(req protocol.CreateJobRequest, sourceWorker model.Worker, identityKey string) (CreateJobResult, error) {
	selectedWorkers, enough := m.workers.ReserveWorkerSlots(sourceWorker.ID, protocol.TaskTypeProcessSegment, req.SegmentCount)
	if !enough {
		return CreateJobResult{
			RequiredSlots:  req.SegmentCount,
			AvailableSlots: len(selectedWorkers),
			Assignments:    previewAssignments(selectedWorkers),
		}, ErrInsufficientResources
	}

	return m.createJobWithAssignments(req, sourceWorker, selectedWorkers, identityKey)
}

func (m *JobManager) createJobWithAssignments(req protocol.CreateJobRequest, sourceWorker model.Worker, selectedWorkers []model.Worker, identityKey string) (CreateJobResult, error) {
	reservedWorkerIDs := workerIDs(selectedWorkers)
	now := time.Now().UTC()
	jobID, err := nextID("job_")
	if err != nil {
		m.workers.ReleaseReservations(reservedWorkerIDs)
		return CreateJobResult{}, err
	}

	assignments := make([]protocol.TaskAssignment, 0, req.SegmentCount)
	taskIDs := make([]string, 0, req.SegmentCount)
	tasks := make([]*model.Task, 0, req.SegmentCount)

	for segmentIndex, worker := range selectedWorkers {
		taskID, err := nextID("task_")
		if err != nil {
			m.workers.ReleaseReservations(reservedWorkerIDs)
			return CreateJobResult{}, err
		}

		task := &model.Task{
			ID:               taskID,
			JobID:            jobID,
			Type:             protocol.TaskTypeProcessSegment,
			SegmentIndex:     segmentIndex,
			AssignedWorkerID: worker.ID,
			Status:           model.TaskStatusAssigned,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		tasks = append(tasks, task)
		taskIDs = append(taskIDs, taskID)
		assignments = append(assignments, protocol.TaskAssignment{
			SegmentIndex: segmentIndex,
			TaskID:       taskID,
			WorkerID:     worker.ID,
			Address:      worker.Address,
		})
	}

	job := &model.Job{
		ID:                  jobID,
		SourceWorkerID:      req.WorkerID,
		SourceWorkerAddress: sourceWorker.Address,
		VideoName:           req.VideoName,
		SegmentCount:        req.SegmentCount,
		TotalSizeBytes:      req.TotalSizeBytes,
		Mode:                req.Mode,
		IdentityKey:         identityKey,
		Status:              model.JobStatusSegmentAssigned,
		TaskIDs:             taskIDs,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existingJobID, exists := m.jobIDByIdentity[identityKey]; exists {
		m.workers.ReleaseReservations(reservedWorkerIDs)
		existing := m.jobs[existingJobID]
		return CreateJobResult{
			JobID:         existing.ID,
			RequiredSlots: req.SegmentCount,
			AlreadyExists: true,
			JobStatus:     protocol.JobStatus(existing.Status),
		}, nil
	}

	m.jobs[jobID] = job
	m.jobIDByIdentity[identityKey] = jobID
	for _, task := range tasks {
		m.tasks[task.ID] = task
	}

	m.logger.Info("job created",
		"event", "job_created",
		"job_id", jobID,
		"source_worker_id", req.WorkerID,
		"source_worker_address", sourceWorker.Address,
		"video_name", req.VideoName,
		"segment_count", req.SegmentCount,
		"total_size_bytes", req.TotalSizeBytes,
		"mode", string(req.Mode),
	)

	return CreateJobResult{
		JobID:          jobID,
		JobStatus:      protocol.JobStatus(job.Status),
		RequiredSlots:  req.SegmentCount,
		AvailableSlots: len(assignments),
		Assignments:    assignments,
	}, nil
}

func (m *JobManager) findJobByIdentity(identityKey string) (model.Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobID, ok := m.jobIDByIdentity[identityKey]
	if !ok {
		return model.Job{}, false
	}
	job, ok := m.jobs[jobID]
	if !ok {
		return model.Job{}, false
	}
	return *job, true
}

func (m *JobManager) GetJobResultByIdentity(address string, videoName string) (protocol.QueryJobResultResponse, bool) {
	identityKey := jobIdentityKey(address, videoName)

	m.mu.RLock()
	defer m.mu.RUnlock()

	jobID, ok := m.jobIDByIdentity[identityKey]
	if !ok {
		return protocol.QueryJobResultResponse{
			Status: protocol.QueryJobResultStatusNotFound,
		}, false
	}

	job, ok := m.jobs[jobID]
	if !ok {
		return protocol.QueryJobResultResponse{
			Status: protocol.QueryJobResultStatusNotFound,
		}, false
	}

	response := protocol.QueryJobResultResponse{
		Status:    protocol.QueryJobResultStatusSuccess,
		JobID:     job.ID,
		JobStatus: protocol.JobStatus(job.Status),
		VideoName: job.VideoName,
		Mode:      job.Mode,
	}

	if job.Status == model.JobStatusCompleted {
		response.Result = &protocol.JobResultInfo{
			WorkerID:        job.ResultWorkerID,
			WorkerAddress:   job.ResultWorkerAddress,
			Name:            job.ResultName,
			URI:             job.ResultURI,
			Checksum:        job.Checksum,
			OutputSizeBytes: job.OutputSizeBytes,
		}
	}
	if job.Status == model.JobStatusFailed {
		response.Failure = &protocol.JobFailureInfo{
			Reason:    job.FailureReason,
			Retryable: job.Retryable,
		}
	}

	return response, true
}

func (m *JobManager) AcceptSegmentTask(taskID string, req protocol.TaskAcceptedRequest) error {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	task, job, err := m.segmentTaskAndJobLocked(taskID, req.WorkerID)
	if err != nil {
		return err
	}
	if task.Status != model.TaskStatusAssigned {
		return ErrTaskInvalidState
	}

	task.Status = model.TaskStatusRunning
	task.AcceptedAt = &now
	task.UpdatedAt = now
	job.Status = model.JobStatusSegmentRunning
	job.UpdatedAt = now

	m.logger.Info("segment task accepted",
		"event", "segment_task_accepted",
		"job_id", job.ID,
		"task_id", task.ID,
		"worker_id", req.WorkerID,
		"segment_index", task.SegmentIndex,
	)

	return nil
}

func (m *JobManager) CompleteSegmentTask(taskID string, req protocol.TaskCompletedRequest) error {
	now := time.Now().UTC()

	m.mu.Lock()
	task, job, err := m.segmentTaskAndJobLocked(taskID, req.WorkerID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if task.Status != model.TaskStatusAssigned && task.Status != model.TaskStatusRunning {
		m.mu.Unlock()
		return ErrTaskInvalidState
	}

	task.Status = model.TaskStatusCompleted
	task.Checksum = req.Checksum
	task.FrameCount = req.FrameCount
	task.DurationMS = req.DurationMS
	task.OutputSizeBytes = req.OutputSizeBytes
	task.CompletedAt = &now
	task.UpdatedAt = now
	job.UpdatedAt = now

	allCompleted := m.allSegmentTasksCompletedLocked(job)
	if allCompleted {
		job.Status = model.JobStatusSegmentCompleted
	}
	assignedWorkerID := task.AssignedWorkerID
	jobID := job.ID
	segmentIndex := task.SegmentIndex
	segmentCount := job.SegmentCount
	m.mu.Unlock()

	m.workers.ReleaseReservations([]string{assignedWorkerID})

	m.logger.Info("segment task completed",
		"event", "segment_task_completed",
		"job_id", jobID,
		"task_id", taskID,
		"worker_id", req.WorkerID,
		"segment_index", segmentIndex,
		"all_segments_completed", allCompleted,
	)

	if allCompleted {
		m.logger.Info("job segments completed",
			"event", "job_segments_completed",
			"job_id", jobID,
			"segment_count", segmentCount,
		)
		if err := m.startAssembleGIF(jobID); err != nil {
			m.logger.Warn("start assemble gif failed",
				"event", "start_assemble_gif_failed",
				"job_id", jobID,
				"error", err,
			)
		}
	}

	return nil
}

func (m *JobManager) startAssembleGIF(jobID string) error {
	m.mu.RLock()
	job, ok := m.jobs[jobID]
	if !ok {
		m.mu.RUnlock()
		return ErrTaskInvalidState
	}

	sourceWorkerID := job.SourceWorkerID
	jobMode := job.Mode
	videoName := job.VideoName
	totalSizeBytes := job.TotalSizeBytes
	jobTaskIDs := append([]string(nil), job.TaskIDs...)

	type segmentSnapshot struct {
		segmentIndex    int
		taskID          string
		workerID        string
		checksum        string
		frameCount      int
		outputSizeBytes int64
	}

	snapshots := make([]segmentSnapshot, 0, len(jobTaskIDs))
	var totalOutputSize int64
	allOutputSizesKnown := true
	for _, taskID := range jobTaskIDs {
		task, ok := m.tasks[taskID]
		if !ok || task.Type != protocol.TaskTypeProcessSegment || task.Status != model.TaskStatusCompleted {
			m.mu.RUnlock()
			return ErrTaskInvalidState
		}
		if task.OutputSizeBytes <= 0 {
			allOutputSizesKnown = false
		}
		totalOutputSize += task.OutputSizeBytes

		snapshots = append(snapshots, segmentSnapshot{
			segmentIndex:    task.SegmentIndex,
			taskID:          task.ID,
			workerID:        task.AssignedWorkerID,
			checksum:        task.Checksum,
			frameCount:      task.FrameCount,
			outputSizeBytes: task.OutputSizeBytes,
		})
	}
	m.mu.RUnlock()

	if jobMode == protocol.JobModeInternal {
		return nil
	}

	sourceWorker, ok := m.workers.GetWorker(sourceWorkerID)
	if !ok {
		m.markJobFailed(jobID)
		return ErrWorkerNotFound
	}

	segments := make([]protocol.AssembleSegmentRef, 0, len(snapshots))
	for _, snapshot := range snapshots {
		worker, ok := m.workers.GetWorker(snapshot.workerID)
		if !ok {
			m.markJobFailed(jobID)
			return ErrWorkerNotFound
		}
		segments = append(segments, protocol.AssembleSegmentRef{
			SegmentIndex:    snapshot.segmentIndex,
			TaskID:          snapshot.taskID,
			WorkerID:        worker.ID,
			WorkerAddress:   worker.Address,
			Checksum:        snapshot.checksum,
			FrameCount:      snapshot.frameCount,
			OutputSizeBytes: snapshot.outputSizeBytes,
		})
	}

	requiredDiskBytes := estimateAssembleDiskBytes(totalSizeBytes, totalOutputSize, allOutputSizesKnown)
	assembleWorker, ok := m.workers.PickWorkerForDiskReservation(protocol.TaskTypeAssembleGIF, requiredDiskBytes)
	if !ok {
		m.markJobFailed(jobID)
		return ErrInsufficientResources
	}

	taskID, err := nextID("task_")
	if err != nil {
		m.workers.ReleaseReservation(assembleWorker.ID, requiredDiskBytes)
		m.markJobFailed(jobID)
		return err
	}

	now := time.Now().UTC()
	assembleTask := &model.Task{
		ID:                taskID,
		JobID:             job.ID,
		Type:              protocol.TaskTypeAssembleGIF,
		SegmentIndex:      -1,
		AssignedWorkerID:  assembleWorker.ID,
		Status:            model.TaskStatusAssigned,
		RequiredDiskBytes: requiredDiskBytes,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	m.mu.Lock()
	job, ok = m.jobs[jobID]
	if !ok || job.Status != model.JobStatusSegmentCompleted {
		m.mu.Unlock()
		m.workers.ReleaseReservation(assembleWorker.ID, requiredDiskBytes)
		return ErrTaskInvalidState
	}
	m.tasks[taskID] = assembleTask
	job.TaskIDs = append(job.TaskIDs, taskID)
	job.Status = model.JobStatusAssembleAssigned
	job.UpdatedAt = now

	req := protocol.StartAssembleGIFRequest{
		JobID:          job.ID,
		AssembleTaskID: taskID,
		Video: protocol.AssembleVideoInfo{
			Name:                videoName,
			SourceWorkerID:      sourceWorker.ID,
			SourceWorkerAddress: sourceWorker.Address,
			TotalSizeBytes:      totalSizeBytes,
		},
		Segments: segments,
	}
	m.mu.Unlock()

	resp, err := m.notifyAssembleWorker(assembleWorker.Address, req)
	if err != nil {
		m.markAssembleStartFailed(jobID, taskID, assembleWorker.ID, requiredDiskBytes, err.Error(), false)
		return err
	}
	if resp.Status != protocol.StartAssembleGIFStatusSuccess {
		if resp.Status == protocol.StartAssembleGIFStatusInsufficientStorage && resp.DiskFreeBytes > 0 {
			m.workers.UpdateWorkerObservedDiskFree(assembleWorker.ID, resp.DiskFreeBytes)
		}
		err := fmt.Errorf("assemble worker returned status %s", resp.Status)
		m.markAssembleStartFailed(jobID, taskID, assembleWorker.ID, requiredDiskBytes, err.Error(), false)
		return err
	}

	m.mu.Lock()
	if task, ok := m.tasks[taskID]; ok && task.Status == model.TaskStatusAssigned {
		now := time.Now().UTC()
		task.Status = model.TaskStatusRunning
		task.UpdatedAt = now
		task.AcceptedAt = &now
	}
	if job, ok := m.jobs[jobID]; ok && job.Status == model.JobStatusAssembleAssigned {
		job.Status = model.JobStatusAssembleRunning
		job.UpdatedAt = time.Now().UTC()
	}
	m.mu.Unlock()

	m.logger.Info("assemble gif started",
		"event", "assemble_gif_started",
		"job_id", jobID,
		"task_id", taskID,
		"worker_id", assembleWorker.ID,
		"worker_address", assembleWorker.Address,
		"required_disk_bytes", requiredDiskBytes,
	)

	return nil
}

func (m *JobManager) AssembleJob(jobID string, req protocol.JobAssembledRequest) error {
	switch req.Status {
	case protocol.JobResultStatusSuccess:
		return m.completeAssembleJob(jobID, req)
	case protocol.JobResultStatusFailed:
		return m.failAssembleJob(jobID, req)
	default:
		return ErrTaskInvalidState
	}
}

func (m *JobManager) completeAssembleJob(jobID string, req protocol.JobAssembledRequest) error {
	now := time.Now().UTC()

	resultWorker, ok := m.workers.GetWorker(req.WorkerID)
	if !ok {
		return ErrWorkerNotFound
	}

	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return ErrTaskNotFound
	}

	assembleTask, releaseWorkerID, releaseDiskBytes, err := m.assembleCompletionTargetLocked(job, req.WorkerID)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	resultName := req.ResultName
	if resultName == "" {
		resultName = job.ID + ".gif"
	}
	resultURI := resultURI(resultWorker.Address, resultName)

	if assembleTask != nil {
		assembleTask.Status = model.TaskStatusCompleted
		assembleTask.Checksum = req.Checksum
		assembleTask.DurationMS = req.DurationMS
		assembleTask.OutputSizeBytes = req.OutputSizeBytes
		assembleTask.CompletedAt = &now
		assembleTask.UpdatedAt = now
	}

	job.Status = model.JobStatusCompleted
	job.ResultWorkerID = resultWorker.ID
	job.ResultWorkerAddress = resultWorker.Address
	job.ResultName = resultName
	job.ResultURI = resultURI
	job.Checksum = req.Checksum
	job.DurationMS = req.DurationMS
	job.OutputSizeBytes = req.OutputSizeBytes
	job.CompletedAt = &now
	job.UpdatedAt = now

	notify := m.jobResultNotificationLocked(job, protocol.JobResultStatusSuccess)
	shouldNotify := job.Mode == protocol.JobModeExternal
	m.mu.Unlock()

	if releaseWorkerID != "" {
		m.workers.ReleaseReservation(releaseWorkerID, releaseDiskBytes)
	}
	if shouldNotify {
		m.notifySourceWorkerBestEffort(job.SourceWorkerAddress, notify)
	}

	m.logger.Info("job assembled",
		"event", "job_assembled",
		"job_id", jobID,
		"worker_id", req.WorkerID,
		"result_uri", resultURI,
	)

	return nil
}

func (m *JobManager) failAssembleJob(jobID string, req protocol.JobAssembledRequest) error {
	now := time.Now().UTC()

	m.mu.Lock()
	job, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return ErrTaskNotFound
	}

	assembleTask, releaseWorkerID, releaseDiskBytes, err := m.assembleCompletionTargetLocked(job, req.WorkerID)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	if assembleTask != nil {
		assembleTask.Status = model.TaskStatusFailed
		assembleTask.FailureReason = req.Reason
		assembleTask.Retryable = req.Retryable
		assembleTask.FailedAt = &now
		assembleTask.UpdatedAt = now
	}

	job.Status = model.JobStatusFailed
	job.FailureReason = req.Reason
	job.Retryable = req.Retryable
	job.FailedAt = &now
	job.UpdatedAt = now

	notify := m.jobResultNotificationLocked(job, protocol.JobResultStatusFailed)
	shouldNotify := job.Mode == protocol.JobModeExternal
	m.mu.Unlock()

	if releaseWorkerID != "" {
		m.workers.ReleaseReservation(releaseWorkerID, releaseDiskBytes)
	}
	if shouldNotify {
		m.notifySourceWorkerBestEffort(job.SourceWorkerAddress, notify)
	}

	m.logger.Warn("job assemble failed",
		"event", "job_assemble_failed",
		"job_id", jobID,
		"worker_id", req.WorkerID,
		"reason", req.Reason,
		"retryable", req.Retryable,
	)

	return nil
}

func (m *JobManager) FailSegmentTask(taskID string, req protocol.TaskFailedRequest) error {
	now := time.Now().UTC()

	m.mu.Lock()
	task, job, err := m.segmentTaskAndJobLocked(taskID, req.WorkerID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if task.Status == model.TaskStatusCompleted || task.Status == model.TaskStatusFailed {
		m.mu.Unlock()
		return ErrTaskInvalidState
	}

	task.Status = model.TaskStatusFailed
	task.FailureReason = req.Reason
	task.Retryable = req.Retryable
	task.FailedAt = &now
	task.UpdatedAt = now
	job.Status = model.JobStatusFailed
	job.UpdatedAt = now
	assignedWorkerID := task.AssignedWorkerID
	jobID := job.ID
	segmentIndex := task.SegmentIndex
	m.mu.Unlock()

	m.workers.ReleaseReservations([]string{assignedWorkerID})

	m.logger.Warn("segment task failed",
		"event", "segment_task_failed",
		"job_id", jobID,
		"task_id", taskID,
		"worker_id", req.WorkerID,
		"segment_index", segmentIndex,
		"reason", req.Reason,
		"retryable", req.Retryable,
	)

	return nil
}

func (m *JobManager) segmentTaskAndJobLocked(taskID string, workerID string) (*model.Task, *model.Job, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return nil, nil, ErrTaskNotFound
	}
	if task.Type != protocol.TaskTypeProcessSegment {
		return nil, nil, ErrTaskInvalidState
	}
	if task.AssignedWorkerID != workerID {
		return nil, nil, ErrTaskWorkerMismatch
	}

	job, ok := m.jobs[task.JobID]
	if !ok {
		return nil, nil, ErrTaskInvalidState
	}

	return task, job, nil
}

func (m *JobManager) allSegmentTasksCompletedLocked(job *model.Job) bool {
	for _, taskID := range job.TaskIDs {
		task, ok := m.tasks[taskID]
		if !ok || task.Type != protocol.TaskTypeProcessSegment || task.Status != model.TaskStatusCompleted {
			return false
		}
	}
	return true
}

func (m *JobManager) assembleCompletionTargetLocked(job *model.Job, workerID string) (*model.Task, string, int64, error) {
	if job.Status != model.JobStatusAssembleAssigned && job.Status != model.JobStatusAssembleRunning && job.Status != model.JobStatusSegmentCompleted {
		return nil, "", 0, ErrTaskInvalidState
	}

	if job.Mode == protocol.JobModeInternal {
		if workerID != job.SourceWorkerID {
			return nil, "", 0, ErrTaskWorkerMismatch
		}
		return nil, "", 0, nil
	}

	for _, taskID := range job.TaskIDs {
		task, ok := m.tasks[taskID]
		if !ok || task.Type != protocol.TaskTypeAssembleGIF {
			continue
		}
		if task.AssignedWorkerID != workerID {
			return nil, "", 0, ErrTaskWorkerMismatch
		}
		if task.Status != model.TaskStatusAssigned && task.Status != model.TaskStatusRunning {
			return nil, "", 0, ErrTaskInvalidState
		}
		return task, task.AssignedWorkerID, task.RequiredDiskBytes, nil
	}

	return nil, "", 0, ErrTaskInvalidState
}

func (m *JobManager) jobResultNotificationLocked(job *model.Job, status protocol.JobResultStatus) protocol.JobResultNotificationRequest {
	return protocol.JobResultNotificationRequest{
		JobID:               job.ID,
		VideoName:           job.VideoName,
		Status:              status,
		ResultWorkerID:      job.ResultWorkerID,
		ResultWorkerAddress: job.ResultWorkerAddress,
		ResultName:          job.ResultName,
		ResultURI:           job.ResultURI,
		Checksum:            job.Checksum,
		OutputSizeBytes:     job.OutputSizeBytes,
		Reason:              job.FailureReason,
		Retryable:           job.Retryable,
	}
}

func (m *JobManager) notifyAssembleWorker(address string, req protocol.StartAssembleGIFRequest) (protocol.StartAssembleGIFResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return protocol.StartAssembleGIFResponse{}, err
	}

	httpResp, err := m.client.Post("http://"+address+"/tasks/assemble_gif", "application/json", bytes.NewReader(body))
	if err != nil {
		return protocol.StartAssembleGIFResponse{}, err
	}
	defer httpResp.Body.Close()

	var resp protocol.StartAssembleGIFResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return protocol.StartAssembleGIFResponse{}, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return resp, fmt.Errorf("assemble worker http status %d", httpResp.StatusCode)
	}

	return resp, nil
}

func (m *JobManager) notifySourceWorkerBestEffort(address string, req protocol.JobResultNotificationRequest) {
	body, err := json.Marshal(req)
	if err != nil {
		m.logger.Warn("marshal source notification failed", "event", "source_notification_failed", "job_id", req.JobID, "error", err)
		return
	}

	httpResp, err := m.client.Post("http://"+address+"/jobs/result", "application/json", bytes.NewReader(body))
	if err != nil {
		m.logger.Warn("notify source worker failed", "event", "source_notification_failed", "job_id", req.JobID, "address", address, "error", err)
		return
	}
	defer httpResp.Body.Close()

	var resp protocol.JobResultNotificationResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		m.logger.Warn("decode source notification response failed", "event", "source_notification_failed", "job_id", req.JobID, "address", address, "error", err)
		return
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 || resp.Status != protocol.JobResultNotifyStatusSuccess {
		m.logger.Warn("source worker rejected notification",
			"event", "source_notification_failed",
			"job_id", req.JobID,
			"address", address,
			"http_status", httpResp.StatusCode,
			"status", string(resp.Status),
		)
		return
	}

	m.logger.Info("source worker notified",
		"event", "source_notification_sent",
		"job_id", req.JobID,
		"address", address,
		"status", string(req.Status),
	)
}

func (m *JobManager) markAssembleStartFailed(jobID string, taskID string, workerID string, requiredDiskBytes int64, reason string, retryable bool) {
	now := time.Now().UTC()

	m.mu.Lock()
	if task, ok := m.tasks[taskID]; ok && task.Status != model.TaskStatusCompleted && task.Status != model.TaskStatusFailed {
		task.Status = model.TaskStatusFailed
		task.FailureReason = reason
		task.Retryable = retryable
		task.FailedAt = &now
		task.UpdatedAt = now
	}
	if job, ok := m.jobs[jobID]; ok {
		job.Status = model.JobStatusFailed
		job.UpdatedAt = now
	}
	m.mu.Unlock()

	m.workers.ReleaseReservation(workerID, requiredDiskBytes)
}

func (m *JobManager) markJobFailed(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job, ok := m.jobs[jobID]; ok {
		job.Status = model.JobStatusFailed
		job.UpdatedAt = time.Now().UTC()
	}
}

func estimateAssembleDiskBytes(totalSizeBytes int64, totalOutputSize int64, allOutputSizesKnown bool) int64 {
	base := totalSizeBytes
	if allOutputSizesKnown && totalOutputSize > 0 {
		base = totalOutputSize * 2
	}
	if base < 1 {
		base = 1
	}
	return base * 12 / 10
}

func resultURI(address string, resultName string) string {
	return "http://" + address + "/results/" + resultName
}

func previewAssignments(workers []model.Worker) []protocol.TaskAssignment {
	assignments := make([]protocol.TaskAssignment, 0, len(workers))
	for segmentIndex, worker := range workers {
		assignments = append(assignments, protocol.TaskAssignment{
			SegmentIndex: segmentIndex,
			WorkerID:     worker.ID,
			Address:      worker.Address,
		})
	}
	return assignments
}

func workerIDs(workers []model.Worker) []string {
	ids := make([]string, len(workers))
	for i, worker := range workers {
		ids[i] = worker.ID
	}
	return ids
}

func jobIdentityKey(sourceWorkerAddress string, videoName string) string {
	return sourceWorkerAddress + "\x00" + videoName
}

func nextID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
