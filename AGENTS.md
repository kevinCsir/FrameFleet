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
- Worker Node is not fully implemented yet. `worker-node/cmd/test-worker` is
  only a simple registration/heartbeat test client.
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

- real Worker HTTP server
- segment upload and processing
- artifact download endpoint
- final GIF generation and result serving
- persistence through GORM
- task timeout and retry
- notification retry
- authentication middleware/JWT/API keys

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

If the sandbox blocks local networking, request approval instead of rewriting
the test to avoid network use.

## Documentation Discipline

- Keep `docs/future-work.md` as the authoritative list of known deferred work.
- Keep `docs/architecture-distilled.md` as the detailed handoff document.
- Keep this `AGENTS.md` short and operational.
- Do not stuff large explanations into this file; link or point to the detailed
  docs instead.
