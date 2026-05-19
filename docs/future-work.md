# Future Work

This file tracks features and optimizations that are intentionally deferred from
the first Entry Server implementation.

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
- Heartbeat-reported running task counts are stored as observed worker state,
  but they do not overwrite Entry's scheduling reservations.

Deferred improvement:

- Release reservations on task accepted/completed/failed/timeout.
- Add timeout handling for tasks that remain assigned but are never accepted by
  the target worker.

## Persistence

Current implementation:

- Worker registry state is in memory only.

Deferred improvement:

- Add database persistence through GORM.
- Persist:
  - workers
  - jobs
  - tasks
  - task status transitions
  - segment artifact manifests
  - final GIF result metadata

The in-memory service layer should remain the main API used by handlers so that
database integration does not leak into HTTP handlers.

## Job And Task Lifecycle

Current implementation:

- `POST /jobs` creates a job and `process_segment` tasks when enough worker
  slots are available.
- If resources are insufficient, Entry returns available worker candidates but
  does not create a partial job.

Deferred implementation:

- `POST /jobs/:job_id/assign`
- `GET /jobs/:job_id`
- `POST /tasks/:task_id/accepted`
- `POST /tasks/:task_id/completed`
- `POST /tasks/:task_id/failed`
- `POST /tasks/:task_id/renew`

Important lifecycle details:

- `process_segment` completion should store artifact URI, checksum, frame
  metadata, and owning worker.
- When all segments complete, Entry should create one `assemble_gif` task.
- The assemble worker should pull segment artifacts directly from segment
  workers.
- Entry should only store metadata and should never proxy video or artifact
  bytes.

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
