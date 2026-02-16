# cronbat LLM Project Guide

This file is a compact, model-agnostic guide for LLM agents working on `cronbat`.

## Project goal

`cronbat` is a lightweight cron daemon with:

- YAML-defined jobs (`jobs/*.yaml`)
- Embedded SQLite run history (`data/cronbat.db`)
- REST API (`/api/v1/*`)
- Built-in minimal web UI (`/ui/`)

The daemon is a single Go binary (`cmd/cronbat/main.go`).

## Runtime flow

1. Load daemon config from `cronbat.yaml`.
2. Ensure `data_dir` exists and open SQLite store.
3. Load job files from `jobs_dir`.
4. Build in-memory job map (`name -> *config.Job`).
5. Start scheduler for enabled jobs.
6. Start HTTP server (API + embedded UI).
7. On signal (`SIGINT`/`SIGTERM`), stop scheduler and shut down HTTP server.

Primary wiring: `cmd/cronbat/main.go`.

## Backend architecture

### Config and job loading

- `internal/config/config.go`
  - Loads top-level daemon config.
  - Applies defaults:
    - `listen: :8080`
    - `data_dir: ./data`
    - `jobs_dir: ./jobs`
    - `log_level: info`
    - `run_logs`: enabled with conservative retention/size defaults
- `internal/config/job.go`
  - Loads job files from `jobs/*.yaml`.
  - Parses and applies defaults (default executor: `shell`).
  - Stores source path in-memory via `Job.FilePath` (not serialized to YAML).
  - Supports parse/marshal/save helpers used by runtime job editing.
  - Supports `working_dir` to execute commands from a specific folder.

### Scheduler

- `internal/scheduler/scheduler.go`
  - Min-heap + one timer goroutine.
  - No polling loop.
  - `AddJob`, `RemoveJob`, `NextRunTime`, `Start`, `Stop`.
- `internal/scheduler/cron.go`
  - Uses `robfig/cron/v3` parser.
  - Supports 5-field cron expressions and descriptor shortcuts (`@daily`, etc.).

### Runner

- `internal/runner/runner.go`
  - Executes commands with `sh -c`.
  - Optional timeout via context deadline.
  - Captures stdout/stderr with 64KB ring buffers (tail only).
- `internal/runner/env.go`
  - Builds env from process env + job env + `CRONBAT_*` metadata vars.

### Storage

- `internal/store/sqlite.go`
  - Uses `modernc.org/sqlite` (pure Go; no CGo).
  - Enables WAL mode.
  - `RecordRun`, `GetRun`, `ListRuns`, `GetJobStats`.
- `internal/store/migrate.go`
  - Creates `runs` table and indexes.

### Persistent run logs

- `internal/runlog/manager.go`
  - Stores per-run stdout/stderr files under `run_logs.dir`.
  - Enforces per-stream caps and retention.
  - Enforces global size cap by deleting oldest files first.

### API and web server

- `internal/web/server.go`
  - Registers API routes and `/ui/` static UI.
  - Redirects `/` to `/ui/`.
- `internal/web/api/*`
  - Jobs, runs, health, stats handlers.
  - Supports runtime job control and editing.

## API surface (current)

Jobs:

- `POST /api/v1/jobs` (create)
- `GET /api/v1/jobs`
- `GET /api/v1/jobs/{name}`
- `PUT /api/v1/jobs/{name}` (update settings)
- `DELETE /api/v1/jobs/{name}`
- `POST /api/v1/jobs/{name}/run`
- `PUT /api/v1/jobs/{name}/start`
- `PUT /api/v1/jobs/{name}/stop`
- `PUT /api/v1/jobs/{name}/pause`
- `PUT /api/v1/jobs/{name}/enable` (legacy-compatible alias)
- `PUT /api/v1/jobs/{name}/disable` (legacy-compatible alias)
- `GET /api/v1/jobs/{name}/yaml`
- `PUT /api/v1/jobs/{name}/yaml`

Runs and system:

- `GET /api/v1/runs`
- `GET /api/v1/runs/{id}`
- `GET /api/v1/runs/{id}/logs` (persisted output, fallback to DB tails)
- `GET /api/v1/events` (SSE realtime stream)
- `GET /api/v1/config` (read-only daemon config)
- `GET /api/v1/health`
- `GET /api/v1/stats`

## Built-in UI

Served from embedded assets:

- `internal/web/ui/static/index.html`
  - Job listing + action buttons (start/stop/pause/delete/run/edit/logs)
  - Link to new job form
  - Realtime updates via `EventSource` + fallback polling
- `internal/web/ui/static/new.html`
  - Create new job from settings form
- `internal/web/ui/static/settings.html`
  - Global daemon settings/status page
- `internal/web/ui/static/job.html`
  - Per-job settings editor + raw YAML editor
- `internal/web/ui/static/logs.html`
  - Per-job run history + side-by-side log detail viewer
- `internal/web/ui/static/run.html`
  - Single-run full output page

No frontend build tooling or JS framework is used.

## Job management behavior

- Job updates are applied in-memory and persisted to their YAML file.
- `start` enables scheduling.
- `stop` and `pause` both disable scheduling (same behavior currently).
- `delete` removes job from memory/scheduler and deletes the YAML file.
- YAML updates validate parse/name/schedule/command before applying.
- Command working directory can be set per job (`working_dir`).
- Full stdout/stderr are persisted to files with conservative default limits.

## Important constraints and caveats

- Job names are treated as stable identifiers; settings editor does not rename jobs.
- `stop` does not kill a currently running command; it prevents future scheduled runs.
- No authentication layer is built in; CORS is permissive for local/dev usage.
- Job map iteration is map-order (API list order is not guaranteed unless sorted client-side).

## Quick local dev

```bash
go build ./cmd/cronbat
./cronbat -config cronbat.yaml
```

Then open:

- UI: `http://localhost:8080/ui/`
- API root examples: `http://localhost:8080/api/v1/jobs`
