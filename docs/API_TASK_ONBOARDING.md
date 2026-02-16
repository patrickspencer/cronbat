# Onboarding a New Task via the API

This guide shows how another program can register a new Cronbat job, trigger a test run, and verify the result.

## Prerequisites

- Cronbat is running (default: `http://localhost:8080`)
- Your program can make HTTP requests to Cronbat

## 1) Create a New Job

Use `POST /api/v1/jobs` with JSON.

```bash
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-job",
    "schedule": "0 2 */2 * *",
    "command": "/usr/local/bin/do-work",
    "working_dir": "/usr/local/bin",
    "timeout": "10m",
    "enabled": true
  }'
```

Notes:

- `name`, `schedule`, and `command` are required.
- `working_dir` is optional and sets the command's execution folder.
- `0 2 */2 * *` means every other day at 02:00 (calendar-based by day-of-month).

## 2) Confirm Job Registration

```bash
curl http://localhost:8080/api/v1/jobs/my-job
```

Check fields like `name`, `enabled`, and `next_run`.

## 3) Trigger a Test Run Immediately

Use `POST /api/v1/jobs/{name}/run`.

```bash
curl -X POST http://localhost:8080/api/v1/jobs/my-job/run
```

## 4) Poll Run Status

Fetch latest run for the job:

```bash
curl "http://localhost:8080/api/v1/runs?job=my-job&limit=1"
```

The `status` will be one of `running`, `success`, or `failure`.

## 5) Fetch Run Output

Get full run logs (or DB tails if file logs are missing):

```bash
curl "http://localhost:8080/api/v1/runs/<RUN_ID>/logs"
```

Look at:

- `stdout`
- `stderr`
- `source` (`file` or `tail`)

## Suggested Program Flow

1. `POST /api/v1/jobs`
2. `GET /api/v1/jobs/{name}`
3. `POST /api/v1/jobs/{name}/run`
4. Poll `GET /api/v1/runs?job={name}&limit=1` until not `running`
5. `GET /api/v1/runs/{id}/logs`

## Common Errors

- `400`: invalid payload (missing required fields or invalid schedule)
- `404`: job/run not found
- `409`: job already exists
- `500`: internal server error
