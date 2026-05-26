# Agent Notes

This file is the quick-start memo for agents working on FrameFleet.

## Interaction Rule

- Do not modify or add code before the developer has seen and approved the plan.
- For non-trivial changes, first explain what you intend to change, which files
  are affected, and why.
- After the developer confirms the direction, implement it.
- If the developer explicitly asks you to implement something, you can proceed,
  but still keep changes scoped.

## What To Read First

- Read this file first.
- Read `docs/future-work.md` when deciding whether a deferred feature or known
  gap already exists.
- Read `docs/architecture-distilled.md` only when you need full architecture or
  protocol context, or when the developer asks for a detailed handoff. It is a
  long document and does not need to be loaded in full for small changes.

## Current Project Shape

- Entry Server is the Go/Gin control plane.
- Worker Node has a real Go agent at `worker-node/go/cmd/worker-agent`.
  `worker-node/cmd/test-worker` is still only a simple registration/heartbeat
  test client.
- C++ engine lives under `worker-node/cpp` and is driven by WorkerGo slot
  subprocesses over line-delimited JSON.
- Public HTTP protocol DTOs and enums live in `pkg/protocol`.
- Entry-only in-memory state lives in `entry-server/internal/model`.
- Entry business logic lives in `entry-server/internal/service`.
- HTTP handlers live in `entry-server/internal/handlers` and should stay thin.

## Core Design Rules

- Entry never proxies video, segment artifacts, or final GIF bytes.
- Entry stores metadata and state only.
- Source workers send segment bytes directly to segment workers.
- Assemble workers pull artifacts directly from segment workers.
- Internal jobs also register with Entry before local processing.
- Job internal/external handling is task-level, not job-level.
- Go/CPP protocol must stay job/task agnostic: C++ knows ops and file paths,
  not distributed job/task IDs.
- Worker artifact files are opaque to Go. Phase-one C++ artifacts are segment
  GIFs; Go stores and transfers them as bytes without parsing GIF internals.
- Job idempotency is based on:
  `source_worker_address + video_name`.
- Cross-process request/response structs must be added to `pkg/protocol`, not
  duplicated separately in Entry and Worker code.
- If you add, remove, or change an HTTP protocol, update
  `docs/architecture-distilled.md`.
- If you defer a feature, tradeoff, or known limitation, update
  `docs/future-work.md`.

## Locking And State

- Do not hold `JobManager` and `WorkerRegistry` locks at the same time.
- Do not call another manager's helper while still holding the current
  manager's lock.
- Snapshot state under one lock, release it, then call the other manager.
- Never hold a lock while making HTTP requests.
- Slot scheduling uses Entry reservations as the source of truth.
- Heartbeat running-task data is observational and must not overwrite Entry
  reservations.
- Disk free bytes are observed from heartbeat; `ReservedDiskBytes` is Entry's
  reservation ledger.
- WorkerGo observes real disk space for the results filesystem. Entry currently
  reserves final GIF space; intermediate artifact GC and fuller disk budgeting
  remain future work.

## Worker And Engine Notes

- WorkerGo must finish initialization and start its HTTP server before starting
  the source scan loop; Entry may assign work back to the source worker.
- For external/manual multi-worker deployments, prefer
  `deploy/worker-template/init-worker.sh` instead of hand-writing runtime
  directories. The generated runtime keeps `worker.env`, `input/`, `data/`,
  `logs/`, and `run.sh`/`stop.sh`/`status.sh`/`logs.sh` together. Worker logs
  should go to the instance's `logs/worker-agent.log`, and agents should inspect
  logs through files rather than relying on terminal stdout. Generated `run.sh`
  cleans intermediate spool directories before start; generated `stop.sh` stops
  the recorded process group so engine and ffmpeg children are covered.
- B/E HTTP handlers should acknowledge quickly and do blocking slot work in
  background goroutines.
- Slot subprocesses are single-request-at-a-time. Preserve request/response
  draining semantics if adding cancellation or retry behavior.
- C++ split uses `ffprobe`/`ffmpeg`; process uses OpenCV; assemble uses ffmpeg
  palette generation for transparent GIF output.
- Keep engine work effectively single-threaded:
  `cv::setNumThreads(1)`, ffmpeg `-threads 1`, `-filter_threads 1`, and
  `-filter_complex_threads 1`.
- Canny thresholds are Entry processing policy:
  `PROCESS_CANNY_LOW_THRESHOLD` and `PROCESS_CANNY_HIGH_THRESHOLD`; Entry
  returns them in worker registration. Worker-local Canny env vars should not
  be reintroduced.
- Entry split defaults are currently 3000 ms target segment duration and
  max 8 segments.

## Implemented Entry Surface

Implemented:

- `POST /workers/register`
- `POST /workers/heartbeat`
- `POST /jobs`
- `GET /jobs/result`
- `POST /tasks/:task_id/accepted`
- `POST /tasks/:task_id/completed`
- `POST /tasks/:task_id/failed`
- Entry -> Worker `POST /tasks/assemble_gif` protocol
- `POST /jobs/:job_id/assembled`
- Entry -> source worker `POST /jobs/result` notification protocol

Major not-yet-implemented areas:

- persistence through GORM
- task timeout and retry
- notification retry
- authentication middleware/JWT/API keys
- intermediate artifact/tmp GC
- production-grade slot restart/retry management

## Testing

Use these commands after code changes:

```bash
source ~/.zshrc
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./...
```

Smoke test:

```bash
source ~/.zshrc
scripts/smoke_task_lifecycle.sh
```

The smoke script starts a temporary Entry Server, uses HTTP calls to exercise
registration, heartbeat, internal/external jobs, task lifecycle, assembled
reporting, idempotency, and result query.

Real video tests:

```bash
source ~/.zshrc
cmake --build worker-node/cpp/build
ctest --test-dir worker-node/cpp/build --output-on-failure
GOCACHE=/tmp/go-build GOMODCACHE=/tmp/go/pkg/mod go test ./worker-node/go/internal/enginepool
```

Real smoke scripts:

```bash
source ~/.zshrc
KEEP_LOGS=1 bash scripts/smoke_worker_single_real.sh
KEEP_LOGS=1 bash scripts/smoke_worker_cluster_real.sh
```

`smoke_worker_cluster_real.sh` starts Entry plus 4 workers and uses real video
processing. It is slower than unit tests and should only be run when explicitly
requested. The older `smoke_worker_cluster.sh` is still the fake/pressure-style
cluster smoke and compares fake input/output bytes.

If the sandbox blocks local networking, request approval instead of rewriting
the test to avoid network use.

## Documentation Discipline

- Keep `docs/future-work.md` as the authoritative list of known deferred work.
- Keep `docs/architecture-distilled.md` as the detailed handoff document.
- Keep this `AGENTS.md` short and operational.
- Do not stuff large explanations into this file; link or point to the detailed
  docs instead.
