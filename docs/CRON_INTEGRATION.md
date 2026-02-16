# Cron Integration Guide

cronbat can integrate with system cron so that cron handles scheduling while cronbat provides visibility (run history, logs, exit codes) through its UI and API.

## Overview

There are several integration patterns:

1. **Wrap mode** — cron runs commands through `cronbat wrap` for visibility
2. **Sync install** — push cronbat jobs into crontab automatically
3. **Sync import** — pull tagged crontab entries into cronbat
4. **API trigger** — use `curl` in crontab to trigger cronbat-managed jobs
5. **Watchdog** — keep cronbat alive via cron
6. **Hybrid** — combine patterns for different job types

## 1. Wrap Mode

The `cronbat wrap` command runs a command and records the result (duration, exit code, stdout/stderr) in cronbat's database.

### Basic usage

```bash
cronbat wrap --name backup --config /etc/cronbat.yaml -- backup.sh --full
```

### In crontab

```crontab
0 2 * * * /usr/local/bin/cronbat wrap --name backup --config /etc/cronbat.yaml -- /usr/local/bin/backup.sh --full
```

### Flags

| Flag | Description |
|------|-------------|
| `--name` | Job name for recording (required) |
| `--config` | Path to cronbat.yaml (default: `cronbat.yaml`) |
| `--api` | Record via API instead of direct DB access |
| `--timeout` | Command timeout (e.g. `5m`, `1h`) |

### Key behaviors

- **Works without the daemon running** — opens SQLite directly
- **Transparent exit codes** — exits with the wrapped command's exit code, so cron's MAILTO and error handling still work
- **Output passthrough** — stdout/stderr are passed through to cron while also being captured
- **Full log storage** — if run logs are enabled in config, full stdout/stderr are saved to files

### Using API mode

If the daemon is running, you can record runs via the API instead of direct DB access:

```bash
cronbat wrap --name backup --api http://localhost:8080 -- backup.sh
```

## 2. Sync Install

Push cronbat jobs into your system crontab.

### Preview changes

```bash
cronbat cron-sync install --config /etc/cronbat.yaml --dry-run
```

### Install

```bash
cronbat cron-sync install --config /etc/cronbat.yaml
```

This reads all enabled jobs from cronbat and generates crontab entries wrapped in markers:

```crontab
# --- cronbat managed begin ---
*/5 * * * * /usr/local/bin/cronbat wrap --name health-check --config /etc/cronbat.yaml -- /usr/local/bin/check.sh  #cronbat
0 2 * * * /usr/local/bin/cronbat wrap --name backup --config /etc/cronbat.yaml -- /usr/local/bin/backup.sh  #cronbat
# --- cronbat managed end ---
```

Your existing crontab entries outside the managed section are preserved.

### Flags

| Flag | Description |
|------|-------------|
| `--config` | Path to cronbat.yaml |
| `--api` | Read jobs from API instead of config files |
| `--dry-run` | Preview without modifying crontab |

### Using with API

```bash
cronbat cron-sync install --api http://localhost:8080 --dry-run
```

## 3. Sync Import

Pull `#cronbat` tagged crontab entries into cronbat as job definitions.

### Tag entries in your crontab

```crontab
*/5 * * * * /usr/local/bin/check.sh  #cronbat
0 2 * * * /usr/local/bin/backup.sh --full  #cronbat
```

### Preview import

```bash
cronbat cron-sync import --config /etc/cronbat.yaml --dry-run
```

### Import

```bash
cronbat cron-sync import --config /etc/cronbat.yaml
```

### Flags

| Flag | Description |
|------|-------------|
| `--config` | Path to cronbat.yaml |
| `--api` | Create jobs via API instead of files |
| `--dry-run` | Preview without creating jobs |
| `--prefix` | Name prefix for imported jobs (default: `cron-`) |

## 4. API Trigger

Use `curl` in crontab to trigger jobs managed by the cronbat daemon:

```crontab
0 2 * * * curl -s -X POST http://localhost:8080/api/v1/jobs/backup/run
```

This lets cronbat's daemon handle execution, logging, and notifications. The job must already exist in cronbat.

## 5. Watchdog

Keep the cronbat daemon alive using cron:

```crontab
* * * * * /usr/local/bin/cronbat watchdog --api http://localhost:8080 --restart-cmd "/usr/local/bin/cronbat -config /etc/cronbat.yaml &"
```

### Flags

| Flag | Description |
|------|-------------|
| `--api` | cronbat API URL (default: `http://localhost:8080`) |
| `--restart-cmd` | Command to run if health check fails |
| `--timeout` | Health check timeout in seconds (default: 5) |

### Behavior

- Checks `GET /api/v1/health` with a 5-second timeout
- If healthy: exits 0 (silent)
- If unhealthy with `--restart-cmd`: runs the restart command
- If unhealthy without `--restart-cmd`: exits 1

## 6. Hybrid Strategy

Choose the right pattern for each job:

| Job type | Recommended pattern |
|----------|-------------------|
| Jobs that must run even if cronbat is down | `cronbat wrap` via cron |
| Jobs you want to manage/edit in cronbat UI | Daemon-scheduled, with API trigger as backup |
| Existing cron jobs you want visibility into | `cronbat wrap` or sync import |
| Critical infrastructure | Watchdog + wrap mode |

### Example hybrid setup

```crontab
# Watchdog: keep cronbat alive
* * * * * /usr/local/bin/cronbat watchdog --api http://localhost:8080 --restart-cmd "/usr/local/bin/cronbat -config /etc/cronbat.yaml &"

# --- cronbat managed begin ---
# Critical jobs that must run even without daemon:
0 2 * * * /usr/local/bin/cronbat wrap --name db-backup --config /etc/cronbat.yaml -- /usr/local/bin/db-backup.sh  #cronbat
*/5 * * * * /usr/local/bin/cronbat wrap --name health-check --config /etc/cronbat.yaml -- /usr/local/bin/check.sh  #cronbat
# --- cronbat managed end ---

# Less critical jobs triggered via API (cronbat daemon handles scheduling):
# These are managed entirely in the cronbat UI
```

## Typical Workflow

1. Create a job in the cronbat UI: `backup` with schedule `0 2 * * *` and command `backup.sh`
2. Run `cronbat cron-sync install --config cronbat.yaml` to push it to crontab
3. Crontab now contains: `0 2 * * * cronbat wrap --name backup --config ... -- backup.sh  #cronbat`
4. At 2am, cron fires `cronbat wrap`, which executes `backup.sh` and records the run
5. View run history, logs, and exit codes in the cronbat UI at `http://localhost:8080/ui/`
