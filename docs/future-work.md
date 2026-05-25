# Future Work

This file tracks features and optimizations that are intentionally deferred from
the first Entry Server implementation, plus migration notes that should not be
lost when moving the project forward.

## Worker Liveness Index

Current implementation:

- Worker liveness is stored on each worker as `LastHeartbeatAt`.
- A background ticker periodically scans all workers and marks timed-out workers
  offline.
- Complexity is `O(N)` per expiry scan.

Deferred improvement:

- Maintain an active-worker linked list plus a hash index:
  - `worker_id -> list element`
  - list ordered from oldest heartbeat to newest heartbeat
- On worker heartbeat, update the timestamp and move the node to the newest end.
- On expiry check, walk from the oldest end and stop as soon as a worker is not
  expired.
- Expected complexity:
  - heartbeat update: `O(1)`
  - expiry check: `O(K)`, where `K` is the number of expired workers processed

Notes:

- Offline workers should remain in the main worker registry for history and
  stable IDs, but should be removed from the active liveness list.
- If an offline worker heartbeats or registers again, it should be marked online
  and reinserted into the active liveness list.

## Worker Scheduling Index

Current implementation:

- `PickBestWorker` scans all workers and selects the online worker with the most
  free slots that supports the requested task type.
- Complexity is `O(N)` per scheduling decision.

Deferred improvement:

- Maintain a dedicated scheduling index ordered by scheduling weight.
- First weight can remain `free_slots`.
- Later weights may include:
  - free slots
  - disk free bytes
  - average task duration by task type
  - task-type support
  - recent failure rate

Possible structures:

- heap with lazy stale-entry cleanup
- balanced tree keyed by weight
- per-task-type candidate queues

## Slot Reservation

Current implementation:

- Entry reserves worker slots when assigning `process_segment` tasks from
  `POST /jobs`.
- Internal jobs also reserve slots on the source worker, because Entry is the
  global task ledger even when a worker processes its own video locally.
- Entry reserves worker slot and estimated disk bytes when assigning
  `assemble_gif` work.
- Heartbeat-reported running task counts are stored as observed worker state,
  but they do not overwrite Entry's scheduling reservations.
- `completed` and `failed` release reserved slots for `process_segment` tasks.
- `assembled` success/failure releases the assemble worker's slot and disk
  reservation.

Deferred improvement:

- Add timeout handling for tasks that remain assigned but are never accepted by
  the target worker.
- Add timeout handling for tasks that remain running but never complete.
- Add retry policy for failed or timed-out task reservations.

## Disk Accounting

Current implementation:

- Worker heartbeat reports observed `disk_free_bytes`.
- Entry keeps `ReservedDiskBytes` for assemble work.
- Assemble worker selection requires:
  `disk_free_bytes - reserved_disk_bytes >= estimated_required_disk_bytes`.
- Assemble disk estimate is currently:
  - `sum(segment output_size_bytes) * 2 * 1.2` when all segment output sizes are
    known
  - otherwise `job.total_size_bytes * 1.2`
- If an assemble worker returns `insufficient_storage`, Entry updates the
  worker's observed free disk when the response includes `disk_free_bytes`.

Deferred improvement:

- Separate persistent artifact/result usage from temporary working-space
  reservation.
- Add cleanup events so Entry can shrink persistent disk accounting after
  artifact/result deletion.
- Track disk estimate corrections by task type and codec profile.
- Add a safety margin configuration instead of a hard-coded 20% margin.

## Persistence

Current implementation:

- Worker registry, jobs, tasks, status transitions, reservations, and result
  metadata are in memory only.

Deferred improvement:

- Add database persistence through GORM.
- Persist:
  - workers
  - jobs
  - tasks
  - task status transitions
  - segment artifact manifests
  - final GIF result metadata
  - job identity index: `source_worker_address + video_name -> job_id`
  - reservation records, especially disk reservations
  - notification attempts and outcomes

The in-memory service layer should remain the main API used by handlers so that
database integration does not leak into HTTP handlers.

## Migration Notes

When migrating or restarting Entry Server, the current in-memory state is lost.
Before running this in a durable environment, persist enough data to rebuild:

- registered workers and their last known addresses/capabilities
- job records, including `mode`, `source_worker_address`, `video_name`, and the
  identity key
- task records and current task status
- reserved slot and reserved disk state
- final job result metadata
- failed task/job reason and retryability
- notification state for best-effort source-worker result notifications

The current code intentionally keeps the service layer in front of storage so
that GORM can be added without changing handler contracts.

## Job And Task Lifecycle

Current implementation:

- `POST /jobs` supports `mode=internal|external`.
- Jobs are idempotent by `source_worker_address + video_name`.
- Duplicate jobs return `already_exists` with the existing job ID and status.
- External jobs create `process_segment` tasks and assign them to available
  workers.
- Internal jobs create `process_segment` tasks assigned to the source worker.
- If resources are insufficient, Entry returns available worker candidates but
  does not create a partial job.
- `POST /tasks/:task_id/accepted` marks a segment task running.
- `POST /tasks/:task_id/completed` marks a segment task completed, stores basic
  output statistics, releases the segment slot, and triggers assemble scheduling
  when all segments are complete.
- `POST /tasks/:task_id/failed` marks a segment task failed, releases the
  segment slot, and marks the job failed.
- External jobs create and notify an `assemble_gif` task after all segments
  complete.
- Internal jobs leave the second stage to the source worker and expect the
  source worker to report `assembled`.
- `POST /jobs/:job_id/assembled` marks the final job success/failure and stores
  result metadata.
- `GET /jobs/result?address=...&video_name=...` looks up a job by source
  address and video name, returning current status and final GIF location when
  available.

Deferred implementation:

- `POST /jobs/:job_id/assign`
- `GET /jobs/:job_id`
- `POST /tasks/:task_id/renew`
- Retry or reassignment for failed/timed-out segment tasks.
- Retry or reassignment for failed assemble notification.

Important lifecycle details:

- `process_segment` completion currently stores checksum, frame count,
  duration, output size, and owning worker. Artifact URI is not required because
  artifact location is derived by convention:
  `http://{worker_address}/artifacts/{task_id}`.
- When all external segments complete, Entry creates one `assemble_gif` task.
- The assemble worker should pull segment artifacts directly from segment
  workers.
- Final GIF location is derived as:
  `http://{result_worker_address}/results/{result_name}`.
- Entry should only store metadata and should never proxy video or artifact
  bytes.

## Worker Node Implementation

Current implementation:

- `worker-node/cmd/test-worker` can register and send heartbeats.
- `worker-node/go/cmd/worker-agent` starts a real WorkerGo HTTP server,
  registers with Entry, sends heartbeats, starts a C++ engine process pool,
  accepts `POST /segments/:task_id/upload`, synchronously processes the segment
  through the engine pool, reports task completion/failure to Entry, and exposes
  segment artifacts through `GET /artifacts/:task_id`. It also accepts Entry
  assemble requests through `POST /tasks/assemble_gif`, pulls artifacts from peer
  workers, runs fake `assemble_gif` work through the engine pool, reports final
  success/failure to Entry, and exposes final results through
  `GET /results/:result_name`.
- Segment upload is currently synchronous: the source worker's upload request is
  held until the receiving worker finishes local `process_segment` work and
  reports the task result to Entry.

Deferred implementation:

- Harden the engine slot process manager. Each slot should behave like a
  well-contained child-process RPC client: detect child death and broken pipes,
  retire broken slots instead of returning them to the idle pool, restart child
  processes, optionally retry idempotent requests with a bounded retry count,
  and return clear errors when retries are exhausted.
- Define cancellation semantics inside the slot manager. If Go gives up waiting
  for a response after a request has been written, the slot must not be returned
  to the idle pool until the stale response is drained and matched, or the child
  process is restarted. This prevents the next caller from reading the previous
  request's response.
- Consider changing segment upload to asynchronous processing: return success to
  the source worker after the segment is durably received and accepted, then run
  `process_segment` in a background goroutine and report completed/failed to
  Entry separately. This would reduce long-lived source-to-segment-worker HTTP
  connections for large or slow videos.
- Optimize assemble work to overlap artifact download and processing where the
  engine format permits it. The first WorkerGo implementation should download
  all artifacts first, then acquire an engine slot and run `assemble_gif`; later
  versions can parallelize downloads and potentially stream artifacts into the
  engine.
- Receive best-effort Entry result notifications through:
  `POST /jobs/result`
- Track local running tasks and include task IDs in heartbeat.

## Worker Runtime Observability

Current implementation:

- Worker heartbeat reports task counts and running task IDs to Entry.
- WorkerGo logs a local runtime snapshot on each heartbeat cycle. This snapshot
  is intentionally local-only and is not part of the public HTTP heartbeat
  protocol. It includes source queue counts, source active phase, engine slot
  state, slot operation, request ID, and slot hold/execution durations.

Deferred improvement:

- Define a unified WorkerEvent object for structured worker runtime events.
- Keep stable correlation fields across modules:
  - `worker_id`
  - `worker_address`
  - `video_name`
  - `job_id`
  - `task_id`
  - `task_type`
  - `segment_index`
  - `slot_id`
  - `engine_op`
  - `request_id`
  - `phase`
  - `duration_ms`
- Use this event object consistently for source scanning, split, job creation,
  segment dispatch, segment processing, artifact serving, assemble work, slot
  acquire/release, engine calls, and result notification.
- Consider adding a worker-local debug endpoint such as `GET /debug/runtime`
  after the log shape stabilizes.
- Only promote selected summary fields into the public heartbeat protocol if
  Entry needs them for scheduling or operator visibility.


## Source Worker Scheduling Strategy

Planned MVP behavior:

- Source WorkerGo scans local input videos from `WORKER_INPUT_DIR` and asks the
  C++ engine to split a video into segment files before calling `POST /jobs`.
- The MVP source scanner only keeps an in-memory seen set and does not persist
  per-file state. It is meant for smoke testing; restarts may rescan unfinished
  or previously processed inputs.
- After splitting, the source worker tries to acquire one local engine slot with
  a non-blocking lease.
- If a slot is available, the source worker creates an `internal` job and
  processes all segment tasks serially through that leased local engine slot.
- If no local slot is immediately available, the source worker creates an
  `external` job and uploads the segment files to the workers returned by Entry
  assignments.
- This keeps the first source-worker implementation compatible with the current
  job-level `mode=internal|external` Entry model.

Deferred improvement:

- Persist source-worker input file state, or move processed inputs to durable
  `.done` / `.failed` locations, so worker restarts do not accidentally rerun
  already processed videos.
- Move from job-level `internal|external` mode to per-segment task assignment.
  In the hybrid model, one job can assign some `process_segment` tasks to the
  source worker and others to remote workers, and the assemble worker pulls
  artifacts from whichever worker owns each completed task. This should improve
  CPU utilization and load balancing for long videos without forcing the entire
  job to be either local or remote.

## Source Worker Notification

Current implementation:

- For external jobs, Entry best-effort notifies the source worker at:
  `POST http://{source_worker_address}/jobs/result`
- Notification failure does not change the final job status.

Deferred improvement:

- Persist notification attempts.
- Retry failed notifications.
- Add notification state to job query output.
- Make notification endpoint idempotent on worker side.

## Authentication And Worker Identity

Current implementation:

- Request bodies include `worker_id`.
- Handlers and services validate worker existence where needed.

Deferred improvement:

- Add Gin middleware for worker identity extraction and validation.
- Later upgrade worker identity to API keys or JWT.
- JWT claims should include worker ID and possibly advertised address or node
  capabilities.
- Keep HTTP handlers independent from the final authentication mechanism.

## Metrics

Current implementation:

- Worker heartbeat can report cumulative task metrics by task type:
  - completed count
  - total duration in milliseconds

Deferred improvement:

- Use these metrics to compute average processing time per task type.
- Feed the averages into scheduling weight calculation.
- Track metrics over rolling windows, not only process-lifetime cumulative
  counters.
