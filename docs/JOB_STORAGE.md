# Job Storage and Jobs Folder

This document explains where Cronbat stores job definitions and how those files are used.

## Jobs Are Stored as YAML Files

- Each job is stored as one YAML file.
- The folder is controlled by `jobs_dir` in `cronbat.yaml`.
- Default `jobs_dir` is `~/.config/cronbat/jobs`.

Example job file:

```yaml
name: hello
schedule: "*/5 * * * *"
command: "echo hello from cronbat"
enabled: true
```

## Startup Behavior

On startup, Cronbat:

1. Loads `cronbat.yaml`.
2. Resolves `~` in paths (for example `~/.config/cronbat/jobs`).
3. Creates `jobs_dir` if it does not exist.
4. Reads all `*.yaml` files in that folder.
5. Loads them into memory and schedules enabled jobs.

Subdirectories are not loaded as active jobs.

## Runtime Behavior

- Creating a job via API writes a new YAML file to `jobs_dir`.
- Updating a job rewrites its YAML file.
- Deleting a job removes its YAML file.
- Archiving a job moves its YAML file into `jobs_dir/archive/`.

Cronbat keeps an in-memory job map for runtime scheduling, but YAML files are the durable source for job definitions.

## Import and Export

- `GET /api/v1/jobs/export` returns all jobs as one multi-document YAML stream.
- `POST /api/v1/jobs/import` reads multi-document YAML and creates/updates jobs.
- `POST /api/v1/jobs/import?replace=true` also deletes existing jobs not present in the import payload.
- `POST /api/v1/jobs/import?dry_run=true` validates and reports planned changes without applying them.
