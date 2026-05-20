# FrameFleet Architecture Distilled

This document is a detailed handoff note for continuing FrameFleet development
in a new conversation or on another machine. It describes the current design,
implemented control-plane behavior, protocols, state machines, and known
follow-up work.

## Project Goal

FrameFleet is a distributed rebuild of FramePipeline. Its job is to split a
video into multiple time segments, process those segments in parallel, and then
assemble the final GIF.

The current phase has a working in-memory Entry control plane and a first
WorkerGo implementation. WorkerGo uses Gin for HTTP/control-plane behavior and
runs a pool of C++ engine child processes for video-like file operations. The
C++ engine is currently fake: it copies/splits/concatenates files so the full
distributed lifecycle can be tested before real OpenCV/FFmpeg logic is ported.

## Process Model

FrameFleet has two process types:

- Entry Server
- Worker Node

There is one Entry Server. It owns the control plane:

- worker registration
- worker heartbeat and observed load
- job identity and status
- segment task allocation
- task state transitions
- assemble task creation
- final job result metadata

Entry does not transfer video bytes, segment artifacts, or GIF files. It only
stores metadata and state.

Worker nodes are peers and can play several roles:

- source worker: owns a local video and creates a job
- segment worker: processes an assigned segment
- assemble worker: pulls segment artifacts and creates the final GIF

If a worker can process a video internally, it still registers the job with
Entry first. Entry is the global task ledger for both internal and external
jobs.

## Data Flow

External processing flow:

1. Source worker A registers a job with Entry using `POST /jobs` and
   `mode=external`.
2. Entry creates `process_segment` tasks and assigns them to workers B/C/D.
3. Entry returns ordered task IDs and worker addresses to A.
4. A directly sends each video segment to B/C/D.
5. B/C/D call `POST /tasks/:task_id/accepted` after receiving a segment.
6. B/C/D process segments and call `POST /tasks/:task_id/completed` or
   `POST /tasks/:task_id/failed`.
7. When all segments complete, Entry creates an `assemble_gif` task.
8. Entry selects an assemble worker E and POSTs assemble metadata to E.
9. E pulls segment artifacts directly from B/C/D.
10. E generates the final GIF and calls `POST /jobs/:job_id/assembled`.
11. Entry marks the job completed or failed.
12. For external jobs, Entry best-effort notifies source worker A.
13. Clients can query result status by source address and video name.

Internal processing flow:

1. Source worker A registers a job with Entry using `POST /jobs` and
   `mode=internal`.
2. Entry creates `process_segment` tasks assigned to A and reserves A's slots.
3. A processes its own segments and reports task completion/failure to Entry.
4. When all segments complete, Entry marks the job `segment_completed` but does
   not assign an external assemble worker.
5. A performs GIF assembly itself.
6. A calls `POST /jobs/:job_id/assembled`.
7. Entry stores final metadata.

## Repository Layout

Current important paths:

```text
entry-server/
  cmd/server/main.go
  internal/handlers/
  internal/logger/
  internal/model/
  internal/server/
  internal/service/

worker-node/
  cmd/test-worker/main.go
  go/
    cmd/worker-agent/main.go
    internal/agent/
    internal/config/
    internal/enginepool/
    internal/engineprotocol/
    internal/entryclient/
    internal/handlers/
    internal/heartbeat/
    internal/peerclient/
    internal/server/
    internal/sourceworker/
    internal/spool/
    internal/workerstate/
  cpp/
    CMakeLists.txt
    include/framefleet_engine/
    src/
    third_party/nlohmann/json.hpp
  protocol/examples/

pkg/protocol/
  assemble.go
  job.go
  segment.go
  task.go
  worker.go

scripts/
  smoke_task_lifecycle.sh
  smoke_worker_cluster.sh

docs/
  future-work.md
  architecture-distilled.md
```

`pkg/protocol` contains cross-process HTTP request/response DTOs and enum
values. Both Entry and Worker code should import these types.

`entry-server/internal/model` contains Entry-only in-memory domain state such as
`Worker`, `Job`, and `Task`.

`worker-node/go/internal/engineprotocol` contains the Go side of the JSON Lines
IPC protocol used between WorkerGo and the C++ engine. The C++ side lives in
`worker-node/cpp/include/framefleet_engine/protocol.hpp` and
`worker-node/cpp/src/protocol.cpp`. Example IPC messages live under
`worker-node/protocol/examples`.

`worker-node/go/internal/enginepool` owns long-running C++ child processes. Each
engine process represents one local processing slot. WorkerGo sends small JSON
requests over stdin and reads one JSON response from stdout. Large payloads are
exchanged by file path in the current MVP.

## Protocol Package

All HTTP DTOs and protocol enums live in `pkg/protocol`.

Task types:

```text
process_segment
assemble_gif
```

Job modes:

```text
internal
external
```

Job statuses:

```text
segment_assigned
segment_running
segment_completed
assemble_assigned
assemble_running
completed
failed
```

Task statuses are internal Entry state:

```text
assigned
running
completed
failed
```

## Worker Registration

Endpoint:

```http
POST /workers/register
```

Request:

```json
{
  "address": "127.0.0.1:9001",
  "total_slots": 4,
  "supported_tasks": ["process_segment", "assemble_gif"],
  "disk_total_bytes": 1000000000,
  "disk_free_bytes": 800000000
}
```

Response:

```json
{
  "status": "success",
  "worker_id": "wrk_xxx",
  "split_policy": {
    "target_segment_duration_ms": 10000,
    "target_segment_size_bytes": 0,
    "max_segments": 0
  }
}
```

`split_policy` is Entry's global source-video split guidance. Source workers
use it before `POST /jobs` so segment sizes stay consistent across the cluster;
`POST /jobs` still receives the resulting `segment_count`. A zero value means
that specific limit is disabled.

Statuses:

```text
success
failed
exists
```

Identity rule:

```text
worker address = ip:port
```

`address` uniquely identifies a worker. This supports running multiple worker
processes on the same machine using different ports.

Entry stores:

- `worker_id -> Worker`
- `address -> worker_id`
- `worker_id -> address`

If the same address registers again, Entry returns the existing worker ID with
`status=exists`.

## WorkerGo Runtime

`worker-node/go/cmd/worker-agent` is the real Worker Node entry point.

Startup flow:

1. Load environment variables, including `.env` support. Entry loads `.env`
   and `entry-server/.env`; Worker loads `.env` and `worker-node/.env` unless an
   override env file is configured.
2. Create Worker data/spool directories.
3. Start the C++ engine process pool.
4. Register with Entry and store the returned worker ID and split policy.
5. Start heartbeat and the placeholder local task monitor.
6. Start the Gin HTTP server.
7. Start the source scanner after the worker's own `/healthz` endpoint is
   reachable, so source upload requests do not race the HTTP listener.

Important environment variables:

```text
WORKER_LISTEN_ADDR=:9001
WORKER_ADVERTISED_ADDRESS=127.0.0.1:9001
ENTRY_BASE_URL=http://127.0.0.1:8080
WORKER_TOTAL_SLOTS=4
WORKER_DATA_DIR=worker-node/data
WORKER_INPUT_DIR=worker-node/data/input
WORKER_SOURCE_SCAN_INTERVAL_SECONDS=10
WORKER_ENGINE_BINARY=worker-node/cpp/build/framefleet-engine
WORKER_HEARTBEAT_INTERVAL_SECONDS=10
WORKER_DISK_TOTAL_BYTES=1000000000
WORKER_DISK_FREE_BYTES=800000000
```

Worker HTTP endpoints currently implemented:

```text
GET  /healthz
POST /segments/:task_id/upload
GET  /artifacts/:task_id
POST /tasks/assemble_gif
GET  /results/:result_name
POST /jobs/result       # route exists; handler is still not implemented
```

Spool layout under `WORKER_DATA_DIR/spool`:

```text
uploads/    received segment inputs
outgoing/   source-worker split segment files
artifacts/  processed segment artifacts
results/    final GIF/result files
tmp/        upload and assemble temporary files
```

Segment upload is synchronous in the MVP: the receiving worker stores the body,
accepts the task at Entry, calls the C++ engine `process_segment`, reports
completed/failed to Entry, and only then returns to the source worker.

Assemble is also synchronous in the MVP: the assemble worker downloads all
artifacts from peer workers, calls the C++ engine `assemble_gif`, reports the job
assembled to Entry, and then returns to Entry's assemble request.

## C++ Engine IPC

WorkerGo keeps a pool of long-running C++ child processes. Each child process is
owned by one engine slot. Parent-child communication uses JSON Lines over
stdin/stdout:

- WorkerGo writes one JSON request line to the child's stdin.
- The C++ engine reads the line, validates it, performs the operation, and
  writes one JSON response line to stdout.
- The response must echo `request_id`; WorkerGo rejects mismatched responses.
- C++ logs go to stderr; WorkerGo reads and forwards them to its logger.

Supported fake operations:

```text
ping
process_internal_simple
split_video
process_segment
assemble_gif
```

Current fake behavior:

- `split_video` always creates two segment files by splitting the input bytes in
  half, regardless of the requested segment count. This is test scaffolding, not
  final policy.
- `process_segment` copies the segment input file to the artifact output file.
- `assemble_gif` concatenates artifact inputs in order into the result file.
- `process_internal_simple` copies input to output.

The C++ dependency on nlohmann/json is vendored as a single header under
`worker-node/cpp/third_party/nlohmann/json.hpp`; it does not require apt for
normal builds.

Build:

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
```

## Source Worker Scanner

The first source-worker implementation scans `WORKER_INPUT_DIR` periodically. It
is intentionally simple and meant to make the cluster runnable.

For each unseen file:

1. Call C++ `split_video` and write segment files under `spool/outgoing`.
2. Try to acquire a local engine lease without blocking.
3. If a local engine is available, try to create an internal job and process all
   assigned segment tasks serially through that same local engine lease.
4. If no local engine is available, or if internal job creation fails because
   Entry cannot reserve enough source-worker slots, create an external job.
5. For external jobs, upload each segment file directly to the worker address
   returned by Entry assignments.

Current limitations:

- The scanner only keeps an in-memory `seen` set. It does not persist per-file
  state and does not move files to `.done` / `.failed` directories.
- Scanning is sequential to keep the MVP stable with Entry reservations.
- The current Entry model is job-level `internal|external`; hybrid per-segment
  internal/external scheduling is deferred.

## Worker Heartbeat

Endpoint:

```http
POST /workers/heartbeat
```

Request:

```json
{
  "worker_id": "wrk_xxx",
  "total_slots": 4,
  "running_process_segment": 1,
  "running_assemble_gif": 0,
  "running_tasks": [
    {
      "task_id": "task_xxx",
      "task_type": "process_segment"
    }
  ],
  "disk_total_bytes": 1000000000,
  "disk_free_bytes": 800000000,
  "metrics": {
    "process_segment": {
      "completed_count": 10,
      "total_duration_ms": 50000
    }
  }
}
```

Response:

```json
{
  "status": "success"
}
```

Statuses:

```text
success
failed
not_found
```

Heartbeat updates observed runtime state:

- reported total slots
- reported running task counts
- running task IDs
- observed disk total/free bytes
- metrics
- online flag
- last heartbeat time

Heartbeat does not overwrite Entry's scheduling reservations.

Entry default heartbeat settings:

- timeout: 30 seconds
- expiry scan interval: 10 seconds

Expired workers are marked offline and `FreeSlots` is set to zero. Worker
records are not deleted.

## Job Creation

Endpoint:

```http
POST /jobs
```

Request:

```json
{
  "worker_id": "wrk_source",
  "video_name": "demo.mp4",
  "segment_count": 3,
  "total_size_bytes": 123456789,
  "mode": "external"
}
```

`mode`:

- `external`: Entry assigns segment tasks to available workers.
- `internal`: Entry records tasks assigned to the source worker itself.

Response on success:

```json
{
  "status": "success",
  "job_id": "job_xxx",
  "job_status": "segment_assigned",
  "required_slots": 3,
  "available_slots": 3,
  "assignments": [
    {
      "segment_index": 0,
      "task_id": "task_xxx",
      "worker_id": "wrk_b",
      "address": "127.0.0.1:9002"
    }
  ]
}
```

Response when duplicate:

```json
{
  "status": "already_exists",
  "job_id": "job_xxx",
  "job_status": "completed",
  "already_exists": true,
  "required_slots": 3,
  "available_slots": 0
}
```

Response when resources are insufficient:

```json
{
  "status": "insufficient_resources",
  "required_slots": 3,
  "available_slots": 1,
  "assignments": [
    {
      "segment_index": 0,
      "worker_id": "wrk_b",
      "address": "127.0.0.1:9002"
    }
  ]
}
```

Statuses:

```text
success
already_exists
insufficient_resources
failed
not_found
```

Job idempotency:

```text
identity_key = source_worker_address + "\x00" + video_name
```

Entry maintains:

```go
jobIDByIdentity map[string]string
```

If the same source address and video name are submitted again, Entry does not
create a new job. It returns `already_exists` with the existing job ID and
status.

## Segment Task Accepted

Endpoint:

```http
POST /tasks/:task_id/accepted
```

Request:

```json
{
  "worker_id": "wrk_xxx"
}
```

Response:

```json
{
  "status": "success"
}
```

Statuses:

```text
success
failed
not_found
worker_mismatch
invalid_state
```

Logic:

- only valid for `process_segment` tasks
- task must exist
- `worker_id` must match the task's assigned worker
- task must be `assigned`
- task becomes `running`
- job becomes `segment_running`
- no slot is released

## Segment Task Completed

Endpoint:

```http
POST /tasks/:task_id/completed
```

Request:

```json
{
  "worker_id": "wrk_xxx",
  "checksum": "sha256:test",
  "frame_count": 10,
  "duration_ms": 1500,
  "output_size_bytes": 2048
}
```

Response:

```json
{
  "status": "success"
}
```

Logic:

- only valid for `process_segment` tasks
- task must be `assigned` or `running`
- worker must match assigned worker
- stores checksum, frame count, duration, and output size
- task becomes `completed`
- releases one reserved segment slot
- if all segment tasks for the job are completed:
  - job becomes `segment_completed`
  - external jobs trigger assemble scheduling
  - internal jobs wait for source worker to report assembled

Artifact location convention:

```text
http://{worker_address}/artifacts/{task_id}
```

Entry does not store or proxy artifact bytes.

## Segment Task Failed

Endpoint:

```http
POST /tasks/:task_id/failed
```

Request:

```json
{
  "worker_id": "wrk_xxx",
  "reason": "ffmpeg exited with code 1",
  "retryable": true
}
```

Response:

```json
{
  "status": "success"
}
```

Logic:

- only valid for `process_segment` tasks
- worker must match assigned worker
- task must not already be `completed` or `failed`
- task becomes `failed`
- job becomes `failed`
- failure reason and retryability are stored
- reserved segment slot is released

## Segment Upload To Worker

Endpoint on segment worker:

```http
POST /segments/:task_id/upload
```

Request body is raw segment bytes. The first implementation writes the body to a
worker-local spool file before calling the C++ engine.

Success response:

```json
{
  "status": "success"
}
```

Failure response:

```json
{
  "status": "failed",
  "reason": "entry complete task failed: ..."
}
```

Current synchronous flow:

1. Store upload body under `spool/uploads`.
2. Call Entry `POST /tasks/:task_id/accepted`.
3. Call C++ `process_segment`.
4. Store artifact under `spool/artifacts/{task_id}.segment`.
5. Call Entry `POST /tasks/:task_id/completed` or `/failed`.
6. Return success/failure to the source worker.

## Artifact Download From Worker

Endpoint on segment worker:

```http
GET /artifacts/:task_id
```

The worker serves the processed artifact file derived by convention from the
local spool path. Entry never proxies this data.

## Assemble Request From Entry To Worker

Endpoint on worker:

```http
POST /tasks/assemble_gif
```

Request sent by Entry:

```json
{
  "job_id": "job_xxx",
  "assemble_task_id": "task_assemble_xxx",
  "video": {
    "name": "demo.mp4",
    "source_worker_id": "wrk_a",
    "source_worker_address": "127.0.0.1:9001",
    "total_size_bytes": 123456789
  },
  "segments": [
    {
      "segment_index": 0,
      "task_id": "task_seg_xxx",
      "worker_id": "wrk_b",
      "worker_address": "127.0.0.1:9002",
      "checksum": "sha256:test",
      "frame_count": 120,
      "output_size_bytes": 4567890
    }
  ]
}
```

Response:

```json
{
  "status": "success",
  "disk_free_bytes": 123456789
}
```

Statuses:

```text
success
failed
insufficient_storage
invalid_request
```

If worker returns `insufficient_storage` and includes `disk_free_bytes`, Entry
updates the worker's observed disk free bytes.

## Assemble Scheduling

When all external segment tasks complete, Entry:

1. Builds a snapshot of job and segment task metadata.
2. Estimates required assemble disk.
3. Picks an assemble worker that:
   - is online
   - supports `assemble_gif`
   - has `FreeSlots > 0`
   - has enough schedulable disk
4. Reserves one slot and the estimated disk bytes.
5. Creates an `assemble_gif` task.
6. POSTs `/tasks/assemble_gif` to the selected worker.

Selection order:

```text
FreeSlots DESC, schedulableDisk DESC
```

Schedulable disk:

```text
DiskFreeBytes - ReservedDiskBytes
```

Disk estimate:

```text
if every segment reports output_size_bytes:
  required = sum(output_size_bytes) * 2 * 1.2
else:
  required = job.total_size_bytes * 1.2
```

The 20% margin is currently hard-coded.

Locking rule:

- Do not hold `JobManager` and `WorkerRegistry` locks at the same time.
- Snapshot under one lock, release it, then call the other manager.
- Never hold a lock across HTTP requests.

## Job Assembled

Endpoint:

```http
POST /jobs/:job_id/assembled
```

Success request:

```json
{
  "worker_id": "wrk_xxx",
  "status": "success",
  "result_name": "demo.gif",
  "checksum": "sha256:final",
  "duration_ms": 1200,
  "output_size_bytes": 4096
}
```

Failure request:

```json
{
  "worker_id": "wrk_xxx",
  "status": "failed",
  "reason": "gif encode failed",
  "retryable": true
}
```

Response:

```json
{
  "status": "success"
}
```

Statuses:

```text
success
failed
not_found
worker_mismatch
invalid_state
```

External job logic:

- finds the job's `assemble_gif` task
- validates `worker_id` against the task's assigned worker
- success:
  - assemble task becomes `completed`
  - job becomes `completed`
  - result metadata is stored
  - reserved slot/disk is released
  - source worker is best-effort notified
- failure:
  - assemble task becomes `failed`
  - job becomes `failed`
  - reserved slot/disk is released
  - source worker is best-effort notified

Internal job logic:

- validates `worker_id == job.SourceWorkerID`
- success/failure directly updates the job
- there is no assemble task reservation to release
- source worker is not notified because it is the caller

Final GIF URI convention:

```text
http://{result_worker_address}/results/{result_name}
```

If `result_name` is omitted, Entry defaults it to:

```text
{job_id}.gif
```

## Final Result Download From Worker

Endpoint on result worker:

```http
GET /results/:result_name
```

The worker serves the final assembled result from `spool/results`. Entry stores
only the result metadata and URI.

## Source Worker Result Notification

Endpoint on source worker:

```http
POST /jobs/result
```

Entry sends this for external jobs only. It is best-effort.

Success notification:

```json
{
  "job_id": "job_xxx",
  "video_name": "demo.mp4",
  "status": "success",
  "result_worker_id": "wrk_e",
  "result_worker_address": "127.0.0.1:9005",
  "result_name": "demo.gif",
  "result_uri": "http://127.0.0.1:9005/results/demo.gif",
  "checksum": "sha256:final",
  "output_size_bytes": 4096
}
```

Failure notification:

```json
{
  "job_id": "job_xxx",
  "video_name": "demo.mp4",
  "status": "failed",
  "reason": "gif encode failed",
  "retryable": true
}
```

Expected response:

```json
{
  "status": "success"
}
```

Notification failure does not roll back job status. WorkerGo currently has the
`POST /jobs/result` route registered, but the handler still returns
`not_implemented`; this is acceptable for the MVP because Entry treats source
notification as best-effort.

## Result Query

Endpoint:

```http
GET /jobs/result?address=127.0.0.1:9001&video_name=demo.mp4
```

Success response:

```json
{
  "status": "success",
  "job_id": "job_xxx",
  "job_status": "completed",
  "video_name": "demo.mp4",
  "mode": "internal",
  "result": {
    "worker_id": "wrk_xxx",
    "worker_address": "127.0.0.1:19102",
    "name": "demo.gif",
    "uri": "http://127.0.0.1:19102/results/demo.gif",
    "checksum": "sha256:final",
    "output_size_bytes": 4096
  }
}
```

Not found response:

```json
{
  "status": "not_found"
}
```

Failure response includes:

```json
{
  "failure": {
    "reason": "some reason",
    "retryable": true
  }
}
```

## Resource Model

Slots:

- `TotalSlots` comes from registration.
- `ReportedTotalSlots` comes from heartbeat and is observational.
- `ReservedSlots` is Entry's scheduling ledger.
- `FreeSlots = TotalSlots - ReservedSlots`.
- Heartbeat does not overwrite reservations.

Disk:

- `DiskFreeBytes` is the latest observed worker free disk from heartbeat.
- `ReservedDiskBytes` is Entry's assemble work reservation ledger.
- Assemble scheduling uses:
  `DiskFreeBytes - ReservedDiskBytes`.
- The implementation currently reserves disk for assemble work only.

## Logging

Entry uses standard-library `log/slog` through `entry-server/internal/logger`.

Supported outputs:

```text
stdout
file
both
discard
```

Environment:

```text
LOG_LEVEL=info
LOG_OUTPUT=stdout
LOG_FILE=logs/entry-server.log
SPLIT_TARGET_SEGMENT_DURATION_MS=10000
SPLIT_TARGET_SEGMENT_SIZE_BYTES=0
SPLIT_MAX_SEGMENTS=0
```

Gin uses a custom request logging middleware. Logs are JSON.

## Smoke Tests

Entry lifecycle smoke:

```bash
scripts/smoke_task_lifecycle.sh
```

It starts Entry, registers workers, creates external and internal jobs, checks
idempotency, exercises task completion/failure, reports internal assembled
success, and queries final result.

Worker cluster smoke:

```bash
scripts/smoke_worker_cluster.sh
```

It starts one Entry and three WorkerGo processes on different localhost ports.
Worker1 gets ten input files; Worker2 and Worker3 start with empty input
directories. The fake C++ engine splits each input into two segment files and
assembles by concatenation. The script waits for every job to complete, downloads
each final result URI, and verifies the output bytes match the original input
file.

Build and test sequence:

```bash
cmake --build worker-node/cpp/build
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./...
scripts/smoke_worker_cluster.sh
```

## Current Coverage

Entry currently supports:

- worker registration
- split policy returned during registration
- worker heartbeat
- internal and external job creation
- idempotent job registration by source address and video name
- segment task accepted/completed/failed
- slot reservation and release
- assemble worker selection and notification for external jobs
- assemble slot/disk reservation and release
- final assembled success/failure report
- best-effort source worker result notification for external jobs
- result query by source address and video name

WorkerGo currently supports:

- real Gin HTTP server
- `.env` / environment-based configuration
- registration and heartbeat loop
- C++ engine child-process pool
- source input directory scanning
- fake video splitting through C++
- synchronous segment upload processing
- artifact serving
- synchronous assemble handling
- final result serving
- cluster-level smoke testing with three workers and one Entry

## Important Deferred Work

High priority:

- Replace fake C++ copy/split/concat operations with real video processing.
- Persist Entry state with GORM.
- Persist source-worker input file state or move files to `.done` / `.failed`.
- Add task assigned/running timeout handling.
- Add retry/reassignment policy for failed or timed-out tasks.
- Add retry policy for Entry result notifications.
- Implement source-worker `POST /jobs/result` notification handling.
- Move from job-level `internal|external` mode to per-segment hybrid scheduling.

Medium priority:

- Change segment upload from synchronous processing to durable receive +
  background processing.
- Overlap assemble artifact downloads with processing where the engine format
  allows it.
- Dedicated scheduling index instead of scanning workers.
- Liveness linked-list index instead of full scan.
- JWT/API key identity middleware.
- Disk accounting split between temporary workspace and persistent artifacts.
- GET `/jobs/:job_id`.
- `POST /tasks/:task_id/renew`.

## Implementation Principles

- Entry never proxies large data.
- Source worker sends segment bytes directly to segment workers.
- Assemble worker pulls artifacts directly from segment workers.
- Entry stores metadata and state only.
- Internal jobs are still registered with Entry before local processing.
- `address + video_name` defines job idempotency.
- Locks should not be nested across managers.
- Network calls must not happen while holding locks.
- Public protocol DTOs belong in `pkg/protocol`.
- Entry-only state belongs in `entry-server/internal/model`.
