# FrameFleet Architecture Distilled

Current as of 2026-05-21.

This document is the detailed handoff note for FrameFleet. It is intended for
future agent conversations and for developers who need to continue the project
without reconstructing the whole design from code history.

For a short operational memo, read `AGENTS.md`. For deferred items, read
`docs/future-work.md`.

## Goal

FrameFleet is a distributed video processing system. It takes a source video,
splits it into time segments, processes those segments in parallel, and then
assembles a final transparent-background GIF.

The current real processing pipeline is:

```text
mp4 input
  -> ffprobe/ffmpeg split into mp4 segments
  -> OpenCV Canny per segment
  -> single-file segment GIF artifacts
  -> ffmpeg palettegen/paletteuse transparent GIF assembly
```

The intended visual output is red contour lines over transparent background.

Current status:

- Entry Server is implemented as an in-memory Go/Gin control plane.
- WorkerGo is implemented as a real worker agent with Gin HTTP endpoints,
  registration, heartbeat, local source scanning, peer transfer, disk
  observation, and C++ engine slot management.
- The C++ engine implements real `split_video`, `process_segment`, and
  `assemble_gif`.
- The real pipeline has been validated by:
  - `TestCppEngineRealVideoPipeline`
  - `scripts/smoke_worker_single_real.sh`
  - `scripts/smoke_worker_cluster_real.sh`

The 4-worker real smoke test starts one Entry and four workers, puts three
videos on worker 1 and three videos on worker 2, then verifies that all six jobs
finish and produce GIF files.

## High-Level Roles

FrameFleet has two long-running process types:

- Entry Server
- Worker Node

There is one Entry Server. Entry owns the control plane:

- worker identity
- worker heartbeat and observed capacity
- split policy
- global backpressure state
- job identity and status
- segment task allocation
- task status transitions
- assemble task creation
- final result metadata

Entry never proxies large data bytes. It does not transfer video bytes, segment
artifacts, or final GIF bytes. It only stores metadata and state.

Worker nodes are peers. A worker can be:

- source worker A: scans local input videos and creates jobs
- segment worker B/C/D: receives segment bytes and processes one phase-one task
- assemble worker E: pulls phase-one artifacts and creates the final GIF

The A/B/C/D/E names are conversational roles, not fixed process types. One
worker can play more than one role for different jobs or even for the same job.

## End-To-End Flow

The common distributed flow is:

1. Worker A scans a local source video from `WORKER_INPUT_DIR`.
2. A blocks on a local C++ engine slot and asks C++ to split the video.
3. C++ writes segment files under A's outgoing spool.
4. A tries to acquire local engine slots for individual segment work without
   blocking.
5. For each segment where A got a local slot, A marks the task plan as
   `internal`.
6. For each segment where A did not get a local slot, A marks the task plan as
   `external`.
7. A calls Entry `POST /jobs` with a per-task plan.
8. Entry creates one job and one `process_segment` task per segment.
9. Entry reserves worker busy counts for both internal and external tasks.
10. Entry returns task IDs and assignments.
11. A processes internal segment assignments locally.
12. A uploads external segment bytes directly to assigned segment workers.
13. Segment workers store the uploaded bytes, accept the task at Entry, return
    HTTP success quickly, and process the segment in a background goroutine.
14. Every segment task, internal or external, reports completion or failure to
    Entry through `POST /tasks/:task_id/completed` or
    `POST /tasks/:task_id/failed`.
15. When all segment tasks for the job complete, Entry schedules one
    `assemble_gif` task.
16. Entry selects an assemble worker using reserved busy count plus available
    disk.
17. Entry sends the assemble request to that worker.
18. The assemble worker validates disk space, returns HTTP success quickly, and
    runs assembly in a background goroutine.
19. The assemble worker concurrently downloads all segment artifacts directly
    from segment workers.
20. The assemble worker blocks on a local C++ engine slot and asks C++ to create
    the GIF.
21. The assemble worker reports `POST /jobs/:job_id/assembled`.
22. Entry marks the job completed or failed, stores result metadata, releases
    assemble reservations, and best-effort notifies the source worker.
23. The source worker logs the result notification. It does not pull the final
    GIF back to A.
24. Clients can query result metadata with `GET /jobs/result`.

The important rule is that Entry is the ledger. Even internal tasks must be
registered with Entry before processing and must report success or failure back
to Entry.

## Repository Layout

Important paths:

```text
entry-server/
  cmd/server/main.go             Entry process entry point
  internal/handlers/             thin Gin HTTP handlers
  internal/logger/               Entry slog wrapper
  internal/model/                Entry-only in-memory state
  internal/server/               routes and server construction
  internal/service/              worker registry and job manager

pkg/protocol/
  assemble.go                    Entry -> Worker assemble protocol
  job.go                         job/task/result protocol
  segment.go                     Worker peer segment upload protocol
  task.go                        task status protocol
  worker.go                      register/heartbeat protocol

worker-node/
  go/cmd/worker-agent/           real WorkerGo agent
  go/internal/agent/             worker boot sequence
  go/internal/config/            worker env config
  go/internal/diskusage/         real filesystem free-space observation
  go/internal/enginepool/        C++ child process slot pool
  go/internal/engineprotocol/    Go side of C++ JSON Lines IPC
  go/internal/entryclient/       Worker -> Entry client
  go/internal/handlers/          Worker HTTP endpoints
  go/internal/heartbeat/         heartbeat loop
  go/internal/peerclient/        Worker -> Worker client
  go/internal/sourceworker/      local input scanner and job producer
  go/internal/spool/             worker local file layout
  go/internal/workerstate/       worker runtime state and heartbeat snapshot

  cpp/
    CMakeLists.txt
    include/framefleet_engine/
    src/
    tests/
    testdata/videos/
    third_party/nlohmann/json.hpp

  protocol/
    ffaf-v1.md                   phase-one artifact format
    examples/                    Go/C++ IPC examples

scripts/
  smoke_task_lifecycle.sh        Entry-only lifecycle smoke
  smoke_worker_cluster.sh        fake/pressure cluster smoke
  smoke_worker_single_real.sh    one-worker real-video smoke
  smoke_worker_cluster_real.sh   four-worker real-video smoke

docs/
  architecture-distilled.md
  future-work.md
```

Shared HTTP DTOs must live in `pkg/protocol`. Do not duplicate request/response
structs separately in Entry and Worker code.

Entry-only domain state belongs in `entry-server/internal/model`.

## Entry Server

Entry is a Go/Gin control plane. It currently stores state in memory.

Implemented Entry HTTP surface:

```text
POST /workers/register
POST /workers/heartbeat

POST /jobs
GET  /jobs/result

POST /tasks/:task_id/accepted
POST /tasks/:task_id/completed
POST /tasks/:task_id/failed

POST /jobs/:job_id/assembled
```

Entry also initiates outbound requests:

```text
Entry -> assemble worker: POST /tasks/assemble_gif
Entry -> source worker:   POST /jobs/result
```

Entry keeps:

- workers indexed by worker ID and advertised address
- jobs indexed by job ID
- job identity index: `source_worker_address + video_name -> job_id`
- tasks indexed by task ID
- Entry reservations for busy counts and assemble disk

### Locking Rules

Important implementation rule:

- Do not hold `JobManager` and `WorkerRegistry` locks at the same time.
- Do not call another manager's helper while still holding the current manager's
  lock.
- Snapshot state under one lock, release it, then call the other manager.
- Never hold any manager lock while making HTTP requests.

This is not style-only. The scheduling path crosses job state, worker state, and
outbound HTTP, so lock ordering bugs can easily become deadlocks.

## Worker Identity And Registration

Worker registration endpoint:

```http
POST /workers/register
```

Request:

```json
{
  "address": "127.0.0.1:19001",
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
    "target_segment_size_bytes": 0,
    "target_segment_duration_ms": 3000,
    "max_segments": 0
  }
}
```

Registration statuses:

```text
success
failed
exists
```

Worker address identity:

```text
address = ip:port
```

The address must uniquely identify the worker process. Multiple workers on the
same machine use different ports.

If the same address registers again, Entry returns the existing worker ID with
`status=exists`.

The split policy is Entry's cluster-wide guidance for source workers. Source
workers use it when calling C++ `split_video`.

Default Entry split policy:

```text
SPLIT_TARGET_SEGMENT_DURATION_MS=3000
SPLIT_TARGET_SEGMENT_SIZE_BYTES=0
SPLIT_MAX_SEGMENTS=0
```

`target_segment_size_bytes=0` means the size target is disabled.
`max_segments=0` means segment count is not capped. When max is uncapped, C++
uses one ffmpeg segment muxer process and estimates segment time from the active
duration/size targets. When max is positive, C++ keeps the bounded per-segment
ffmpeg path.

## Heartbeat And Backpressure

Heartbeat endpoint:

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
  "status": "success",
  "global_backpressure": {
    "active": false,
    "reason": "",
    "busy_threshold_multiplier": 3,
    "observed_worker_count": 4
  }
}
```

Heartbeat statuses:

```text
success
failed
not_found
```

Heartbeat updates observed worker state:

- reported total slots
- observed running counts
- observed running task IDs
- observed disk total/free bytes
- metrics
- online flag
- last heartbeat time

Heartbeat does not overwrite Entry reservations. Entry reservations are the
source of truth for scheduling.

Backpressure:

- Entry maintains global backpressure state.
- It becomes active when all observed online workers are at or above the busy
  threshold.
- The current threshold multiplier is `3`, meaning:
  `reserved_slots >= total_slots * 3`.
- The heartbeat response sends this state to workers.
- Source workers pause production while backpressure is active.

This is a coarse cluster protection mechanism. It does not cancel work already
accepted or already assigned.

## Job Creation

Job creation endpoint:

```http
POST /jobs
```

New task-level request shape:

```json
{
  "worker_id": "wrk_source",
  "video_name": "demo.mp4",
  "total_size_bytes": 123456789,
  "tasks": [
    {
      "segment_index": 0,
      "mode": "internal"
    },
    {
      "segment_index": 1,
      "mode": "external"
    }
  ]
}
```

Legacy compatibility fields still exist:

```json
{
  "segment_count": 2,
  "mode": "external"
}
```

The new model is task-level. A job is no longer fundamentally internal or
external. Each segment task declares its own processing mode.

Task processing modes:

```text
internal
external
```

Response:

```json
{
  "status": "success",
  "job_id": "job_xxx",
  "job_status": "segment_assigned",
  "required_slots": 2,
  "available_slots": 2,
  "assignments": [
    {
      "segment_index": 0,
      "task_id": "task_a",
      "worker_id": "wrk_source",
      "address": "",
      "mode": "internal"
    },
    {
      "segment_index": 1,
      "task_id": "task_b",
      "worker_id": "wrk_b",
      "address": "127.0.0.1:19002",
      "mode": "external"
    }
  ]
}
```

Job creation statuses:

```text
success
already_exists
insufficient_resources
failed
not_found
```

Idempotency rule:

```text
source_worker_address + video_name
```

If a duplicate job is submitted, Entry returns `already_exists` with the
existing job ID and status.

Internal task semantics:

- The task is assigned to the source worker.
- Entry reserves source worker busy count.
- Response `address` is intentionally empty because the source worker does not
  need to upload to itself.
- The source worker must still report task completion/failure to Entry.

External task semantics:

- Entry selects the least busy compatible worker by `ReservedSlots`.
- Entry prefers a non-source worker when possible.
- If no other compatible worker is available, Entry may assign the external
  task back to the source worker as fallback.
- Entry reserves the selected worker busy count.
- The source worker uploads the segment bytes directly to `address`.

Entry allows reservations to exceed `total_slots`. `FreeSlots` is retained as a
derived compatibility field, but modern scheduling decisions use busy count
(`ReservedSlots`) rather than a hard free-slot cap. Backpressure controls
overproduction at the cluster level.

## Job And Task Status

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

Task statuses are Entry-internal:

```text
assigned
running
completed
failed
```

Task types:

```text
process_segment
assemble_gif
```

Segment lifecycle endpoints:

```http
POST /tasks/:task_id/accepted
POST /tasks/:task_id/completed
POST /tasks/:task_id/failed
```

`accepted` is currently low-value but still present. Workers should call it
after the segment input is safely stored locally.

Completion request:

```json
{
  "worker_id": "wrk_b",
  "checksum": "sha256-or-other",
  "frame_count": 120,
  "duration_ms": 4000,
  "output_size_bytes": 123456
}
```

Failure request:

```json
{
  "worker_id": "wrk_b",
  "reason": "engine process_segment failed",
  "retryable": true
}
```

On segment completion or failure, Entry releases that segment task's reserved
worker busy count.

When all segment tasks for a job are completed, Entry schedules the assemble
stage. This applies to both internal and external segment tasks.

If any segment task fails, the job is marked failed in the current
implementation. There is no reassignment or retry yet.

## Assemble Scheduling

Entry schedules assemble after all segment tasks complete.

Entry snapshots completed segment metadata:

- segment index
- task ID
- producing worker ID
- producing worker address
- checksum
- frame count
- output size

Entry then estimates required assemble disk:

```text
if all segment output sizes are known:
  required = sum(segment_output_size_bytes) * 2 * 1.2
else:
  required = job_total_size_bytes * 1.2
```

Entry picks an assemble worker with:

- `assemble_gif` support
- online state
- enough schedulable disk:
  `disk_free_bytes - reserved_disk_bytes >= required`
- smallest `ReservedSlots`
- larger schedulable disk as tie-breaker
- worker ID as final deterministic tie-breaker

Entry reserves both:

- one worker busy count
- `required_disk_bytes`

Then Entry creates an `assemble_gif` task and sends the assemble request.

If the assemble worker responds `insufficient_storage` and includes
`disk_free_bytes`, Entry updates that worker's observed free disk and marks the
assemble start failed.

If Entry cannot start assemble, the job is marked failed in the current
implementation.

## Entry To Worker Assemble Protocol

Endpoint on Worker:

```http
POST /tasks/assemble_gif
```

Request:

```json
{
  "job_id": "job_xxx",
  "assemble_task_id": "task_assemble",
  "video": {
    "name": "demo.mp4",
    "source_worker_id": "wrk_source",
    "source_worker_address": "127.0.0.1:19001",
    "total_size_bytes": 123456789
  },
  "segments": [
    {
      "segment_index": 0,
      "task_id": "task_segment",
      "worker_id": "wrk_b",
      "worker_address": "127.0.0.1:19002",
      "checksum": "abc",
      "frame_count": 120,
      "output_size_bytes": 123456
    }
  ]
}
```

Response:

```json
{
  "status": "success"
}
```

Other statuses:

```text
failed
insufficient_storage
invalid_request
```

Worker assemble behavior:

1. Validate request.
2. Check local observed disk against the same estimate style.
3. If enough disk exists, start `runAssembleGIF` in a goroutine.
4. Return HTTP success immediately.
5. Concurrently download all segment artifacts from peer workers.
6. Block on an engine slot.
7. Call C++ `assemble_gif`.
8. Report success/failure to Entry through `POST /jobs/:job_id/assembled`.

Entry's HTTP client timeout for the start request is short. This is why the
worker returns immediately and does long work in the background.

## Job Assembled Protocol

Endpoint on Entry:

```http
POST /jobs/:job_id/assembled
```

Success request:

```json
{
  "worker_id": "wrk_e",
  "status": "success",
  "result_name": "job_xxx.gif",
  "checksum": "abc",
  "duration_ms": 10000,
  "output_size_bytes": 654321
}
```

Failure request:

```json
{
  "worker_id": "wrk_e",
  "status": "failed",
  "reason": "engine assemble_gif failed",
  "retryable": true
}
```

Response statuses:

```text
success
failed
not_found
worker_mismatch
invalid_state
```

On assembled success, Entry:

- marks assemble task completed
- marks job completed
- stores result worker ID/address
- stores result name, result URI, checksum, output size
- releases assemble worker busy and disk reservations
- logs the completed job
- sends best-effort result notification to source worker

The result URI is derived by convention:

```text
http://{result_worker_address}/results/{result_name}
```

On assembled failure, Entry:

- marks assemble task failed
- marks job failed
- stores failure reason/retryability
- releases assemble worker busy and disk reservations
- sends best-effort failure notification to source worker

There is no notification retry yet.

## Result Query

Endpoint:

```http
GET /jobs/result?address=127.0.0.1:19001&video_name=demo.mp4
```

Success response:

```json
{
  "status": "success",
  "job_id": "job_xxx",
  "job_status": "completed",
  "video_name": "demo.mp4",
  "mode": "external",
  "result": {
    "worker_id": "wrk_e",
    "worker_address": "127.0.0.1:19004",
    "name": "job_xxx.gif",
    "uri": "http://127.0.0.1:19004/results/job_xxx.gif",
    "checksum": "abc",
    "output_size_bytes": 654321
  }
}
```

The `mode` value on a job is now best-effort summary compatibility:

- all internal tasks -> `internal`
- otherwise -> `external`

It should not be used to decide per-task behavior.

## WorkerGo Runtime

The real worker entry point is:

```text
worker-node/go/cmd/worker-agent
```

Startup sequence:

1. Load `.env` and environment variables.
2. Create spool directories.
3. Create disk observer for the results filesystem.
4. Create Worker state.
5. Create Entry and peer HTTP clients.
6. Create C++ engine pool.
7. Start all C++ engine child processes.
8. Register with Entry.
9. Store returned worker ID and split policy.
10. Start heartbeat loop.
11. Start local task monitor.
12. Build Gin router.
13. Start HTTP server.
14. Probe the worker's own `/healthz` endpoint.
15. Start source scanner only after `/healthz` is reachable.

The last part is important: source work can be assigned back to the same worker.
The worker must not start producing jobs before its own HTTP server can receive
segment uploads and assemble notifications.

Worker HTTP endpoints:

```text
GET  /healthz
POST /segments/:task_id/upload
GET  /artifacts/:task_id
POST /tasks/assemble_gif
GET  /results/:result_name
POST /jobs/result
```

`POST /jobs/result` logs final result notification metadata. It does not pull
the final GIF back to the source worker.

## Worker Spool Layout

Under `WORKER_DATA_DIR/spool`:

```text
uploads/    received segment inputs
outgoing/   source-worker split segment files
artifacts/  processed segment artifacts, served by /artifacts/:task_id
results/    final GIF files, served by /results/:result_name
tmp/        upload and assemble temporary files
```

Segment upload writes the HTTP body to a temp file first, then renames it into
the upload input path. This avoids exposing a partially written input file to
the processing goroutine.

## Segment Worker Behavior

Endpoint:

```http
POST /segments/:task_id/upload
```

Behavior:

1. Validate task ID and worker registration.
2. Write request body to temp file.
3. Rename temp file to the upload input path.
4. Call Entry `POST /tasks/:task_id/accepted`.
5. Start `processUploadedSegment` in a goroutine.
6. Return HTTP success immediately.

Background processing:

1. Use `context.Background()` so local processing is not cancelled when the HTTP
   client disconnects.
2. Block on an engine slot.
3. Mark local worker state task running.
4. Call C++ `process_segment`.
5. Report completed or failed to Entry.
6. Release engine slot.

This prevents long segment processing from causing source-worker HTTP upload
timeouts.

## Source Worker Scanner

The source worker scans `WORKER_INPUT_DIR` periodically.

Current data structures:

- `pendingSplit`: videos seen on disk but not yet fully handled
- `done`: videos already split and registered or abandoned

Current loop model:

- A scanner goroutine periodically reads the input directory and adds unfamiliar
  files to `pendingSplit`.
- A processor goroutine processes at most one pending source video per cycle.
- The processor first checks Entry backpressure state from the last heartbeat.
- If backpressure is active, it skips production until a later cycle.
- If backpressure is inactive, it pops one source video and blocks on a local
  engine slot for splitting.

Source processing steps:

1. Block on an engine slot and call C++ `split_video`.
2. For each returned segment, try to acquire a local engine slot without
   blocking.
3. If the try-acquire succeeds, hold that slot and declare the task `internal`.
4. If try-acquire fails because no idle slot exists, declare the task
   `external`.
5. Call Entry `POST /jobs` with the task plan.
6. If job creation fails, retry after 1s, 2s, and 4s.
7. If all create-job attempts fail, release held internal slots, log
   abandonment, and mark the video done for this process lifetime.
8. For internal assignments, run C++ `process_segment` with the held slot and
   report completion/failure.
9. For external assignments, upload segment files directly to the assigned
   worker address.

The current source state is in memory only. It is not persisted and files are
not moved into done/failed folders yet.

## Worker Notification Handler

Entry sends final result metadata to the source worker:

```http
POST /jobs/result
```

Request:

```json
{
  "job_id": "job_xxx",
  "video_name": "demo.mp4",
  "status": "success",
  "result_worker_id": "wrk_e",
  "result_worker_address": "127.0.0.1:19004",
  "result_name": "job_xxx.gif",
  "result_uri": "http://127.0.0.1:19004/results/job_xxx.gif",
  "checksum": "abc",
  "output_size_bytes": 654321,
  "reason": "",
  "retryable": false
}
```

The worker logs structured fields:

- `job_id`
- `video_name`
- `status`
- `result_worker_id`
- `result_worker_address`
- `result_name`
- `result_uri`
- `checksum`
- `output_size_bytes`
- `reason`
- `retryable`
- `download_url` when result worker address and result name are present

It does not download the GIF back to A. The design keeps large files on the
worker that produced them.

## Disk Accounting

WorkerGo reports real disk information from the filesystem that contains the
results directory.

Relevant worker code:

```text
worker-node/go/internal/diskusage
worker-node/go/internal/workerstate
```

Fallback env values are used if real observation fails:

```text
WORKER_DISK_TOTAL_BYTES=1000000000
WORKER_DISK_FREE_BYTES=800000000
```

Entry stores:

- latest observed `DiskTotalBytes`
- latest observed `DiskFreeBytes`
- `ReservedDiskBytes` for assemble work

Entry assemble scheduling uses:

```text
schedulable_disk = DiskFreeBytes - ReservedDiskBytes
```

The current disk model focuses on final assemble/result capacity. Intermediate
files and temp files are not fully reserved or garbage-collected yet.

## Go/C++ Boundary

C++ must not know distributed task concepts such as:

- `job_id`
- `task_id`
- worker ID
- worker address
- Entry state

WorkerGo owns the distributed model. C++ only sees:

- operation name
- input file path(s)
- output file path or output directory
- split policy fields

This keeps the C++ engine usable as a local single-process video engine and
keeps distributed scheduling in Go.

Go/C++ IPC uses JSON Lines over stdin/stdout:

- WorkerGo writes one JSON request line.
- C++ reads one request.
- C++ writes exactly one JSON response line.
- C++ logs to stderr.
- WorkerGo checks that the response `request_id` matches the request.

The IPC version is currently `1`.

Supported operations:

```text
ping
process_internal_simple
split_video
process_segment
assemble_gif
```

`process_internal_simple` is old compatibility/test behavior and is not part of
the main distributed video pipeline.

Request shape:

```json
{
  "version": 1,
  "request_id": "req_xxx",
  "op": "split_video",
  "target_segment_size_bytes": 0,
  "target_segment_duration_ms": 3000,
  "max_segments": 0,
  "input": {
    "mode": "file",
    "path": "/path/input.mp4",
    "name": "input.mp4",
    "size_bytes": 123456
  },
  "output_dir": "/path/outgoing/job_xxx"
}
```

Response shape:

```json
{
  "version": 1,
  "request_id": "req_xxx",
  "type": "completed",
  "duration_ms": 1234,
  "output_size_bytes": 456789,
  "frame_count": 120,
  "artifact_name": "task_xxx.ffaf",
  "result_name": "job_xxx.gif",
  "checksum": "abc",
  "segments": [
    {
      "segment_index": 0,
      "path": "/path/segment_000.mp4",
      "name": "segment_000.mp4",
      "size_bytes": 123
    }
  ]
}
```

Failure response:

```json
{
  "version": 1,
  "request_id": "req_xxx",
  "type": "failed",
  "reason": "ffmpeg failed",
  "retryable": true
}
```

## C++ Engine

C++ engine path:

```text
worker-node/cpp
```

Binary:

```text
worker-node/cpp/build/framefleet-engine
```

Dependencies:

- CMake 3.16+
- C++17 compiler
- OpenCV 4
- ffmpeg
- ffprobe
- vendored nlohmann/json single header

Build:

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
```

The engine tries to preserve the one-slot-one-thread meaning:

- C++ calls `cv::setNumThreads(1)`.
- ffmpeg/ffprobe calls use single-thread flags where applicable:
  - `-threads 1`
  - `-filter_threads 1`
  - `-filter_complex_threads 1`
- WorkerGo passes environment values intended to reduce hidden thread pools:
  - `OMP_NUM_THREADS=1`
  - related BLAS/OpenMP-style limits where configured in engine pool code

ffmpeg path overrides:

```text
FRAMEFLEET_FFMPEG_PATH
FRAMEFLEET_FFPROBE_PATH
```

Canny threshold env consumed by C++:

Entry owns processing policy and returns it during worker registration:

```text
PROCESS_CANNY_LOW_THRESHOLD
PROCESS_CANNY_HIGH_THRESHOLD
GIF_ASSEMBLE_MODE
```

Worker-local Canny env vars are intentionally not supported; otherwise
different workers can process segments of the same job with inconsistent edge
parameters.

Code defaults are currently:

```text
low=80
high=160
```

The real smoke scripts currently use:

```text
low=180
high=360
```

because that produced better transparent red-edge output for the checked-in
test video.

## C++ Operations

### split_video

Input:

- source mp4 path
- output directory
- target segment duration in ms
- target segment size in bytes
- max segment count

Current behavior:

- use ffprobe to inspect video duration/metadata
- compute a segment duration from split policy and max segment count
- call ffmpeg to cut mp4 segment files
- return segment file paths and sizes

The current implementation favors duration-based splitting. Size-based splitting
is part of the protocol but not the primary production heuristic yet.

### process_segment

Input:

- segment mp4 path
- output artifact path

Current behavior:

- read video frames with OpenCV
- convert to grayscale
- run Canny
- build BGRA frames where edge pixels are opaque red and non-edge pixels are
  transparent
- PNG-encode temporary BGRA frames
- encode frames into one segment GIF artifact
- return checksum/frame count/duration/output size

### assemble_gif

Input:

- ordered segment GIF artifact paths
- output GIF path
- assemble mode: `local_palette_concat` or `global_palette_recode`

Current behavior:

- `local_palette_concat`: concatenate GIF image/control blocks while preserving
  each segment's local/global palette as local image palettes
- `global_palette_recode`: decode segment GIF inputs with ffmpeg and re-encode
  final GIF with palettegen/paletteuse
- write final GIF
- return checksum/duration/output size

## Engine Slot Pool

WorkerGo manages a pool of C++ child processes. Each child is one local slot.

Relevant package:

```text
worker-node/go/internal/enginepool
```

Semantics:

- `Acquire(ctx)` blocks until a slot is available or context is cancelled.
- `TryAcquire()` returns immediately.
- A lease owns exactly one C++ child while held.
- A lease call writes one request and waits for one response.
- Response `request_id` must match the request ID.
- If the caller context is cancelled while waiting for a response, the current
  implementation drains the child response before returning the slot to the
  pool. This prevents the next caller from reading a stale response.

Future hardening is still needed:

- child death detection and restart
- broken pipe handling
- retry policy
- unhealthy slot retirement
- stronger timeout/cancellation semantics
- production-grade drain/isolation behavior

## Configuration

Entry env:

```text
ENTRY_SERVER_ADDR=:8080
WORKER_HEARTBEAT_TIMEOUT_SECONDS=30
WORKER_HEARTBEAT_CHECK_INTERVAL_SECONDS=10
SPLIT_TARGET_SEGMENT_DURATION_MS=3000
SPLIT_TARGET_SEGMENT_SIZE_BYTES=0
SPLIT_MAX_SEGMENTS=0
LOG_LEVEL=info
LOG_OUTPUT=stdout|file|both|discard
LOG_FILE=logs/entry-server.log
```

Worker env:

```text
WORKER_LISTEN_ADDR=:9001
WORKER_ADVERTISED_ADDRESS=127.0.0.1:9001
ENTRY_BASE_URL=http://127.0.0.1:8080
WORKER_TOTAL_SLOTS=4
WORKER_DATA_DIR=worker-node/data
WORKER_INPUT_DIR=worker-node/data/input
WORKER_ENGINE_BINARY=worker-node/cpp/build/framefleet-engine
WORKER_HEARTBEAT_INTERVAL_SECONDS=10
WORKER_SOURCE_SCAN_INTERVAL_SECONDS=10
WORKER_DISK_TOTAL_BYTES=1000000000
WORKER_DISK_FREE_BYTES=800000000
WORKER_LOG_LEVEL=info
WORKER_LOG_OUTPUT=stdout|file|both|discard
WORKER_LOG_FILE=logs/worker-agent.log
```

Real smoke scripts set Entry processing thresholds to `180/360`.

Worker loads `.env` and `worker-node/.env` unless overridden by the current
loader behavior. Entry loads `.env` and `entry-server/.env`.

## Logging

Entry logging:

```text
LOG_LEVEL
LOG_OUTPUT
LOG_FILE
```

Worker logging:

```text
WORKER_LOG_LEVEL
WORKER_LOG_OUTPUT
WORKER_LOG_FILE
```

Smoke scripts set separate log files for Entry and each Worker when
`KEEP_LOGS=1`, so failures can be diagnosed by process.

Important structured events include:

- worker registration
- worker heartbeat
- job created
- task accepted/completed/failed
- assemble requested/running/completed/failed
- job completed
- source result notification
- source scanner queued/split/register/dispatch events

## Testing

General Go tests:

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./...
```

C++ build and tests:

```bash
cmake -S worker-node/cpp -B worker-node/cpp/build
cmake --build worker-node/cpp/build
ctest --test-dir worker-node/cpp/build --output-on-failure
```

C++ real-video integration through WorkerGo engine pool:

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod \
go test ./worker-node/go/internal/enginepool -run TestCppEngineRealVideoPipeline -count=1
```

Entry-only lifecycle smoke:

```bash
source ~/.zshrc
scripts/smoke_task_lifecycle.sh
```

Real one-worker smoke:

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_single_real.sh
```

Real four-worker smoke:

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_cluster_real.sh
```

The real cluster smoke is intentionally heavier. Do not run it casually during
small edits. It starts one Entry, four workers, and processes six real video
jobs using one base test video plus looped 2x and 4x variants.

Fake/pressure cluster smoke:

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_cluster.sh
```

This script is useful for scheduling pressure behavior but does not validate
real video quality.

Reusable test video:

```text
worker-node/cpp/testdata/videos/canny_source_short.mp4
```

Large test inputs should be generated by scripts from the reusable short video
rather than committed as additional binary files.

If sandboxing blocks local networking during smoke tests, request approval
instead of rewriting the test to avoid networking.

## Known Limitations

The system is runnable but not production-hardened.

Known deferred areas:

- database persistence through GORM
- task timeout
- task retry and reassignment
- assemble retry and reassignment
- notification retry
- authentication and authorization
- persistent source scanner state
- moving source files into done/failed folders
- intermediate artifact and temp-file garbage collection
- detailed disk budgeting for temp/intermediate files
- production-grade C++ child process restart/isolation
- richer split heuristics using size, duration, frame count, and resolution
- configurable Canny/output style beyond simple env thresholds
- artifact compression/version evolution

Authoritative deferred-work list:

```text
docs/future-work.md
```

## Design Guardrails

Keep these rules unless the architecture is deliberately changed:

- Entry never proxies video, artifact, or GIF bytes.
- Entry is the source of truth for job/task metadata and reservations.
- Worker heartbeat running counts are observational only.
- Internal tasks still register with Entry and report completion/failure.
- Job idempotency is based on `source_worker_address + video_name`.
- Cross-process HTTP DTOs live in `pkg/protocol`.
- C++ does not know job IDs, task IDs, workers, or Entry.
- Go/C++ IPC passes operation names and file paths, not distributed metadata.
- Source workers send segment bytes directly to segment workers.
- Assemble workers pull artifacts directly from segment workers.
- Final GIF stays on the assemble worker that produced it.
- If an HTTP protocol changes, update this document.
- If a feature is deferred, update `docs/future-work.md`.
