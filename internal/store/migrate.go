package store

import "database/sql"

const migrationSQL = `
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    job_name TEXT NOT NULL,
    status TEXT NOT NULL,
    exit_code INTEGER,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    duration_ms INTEGER,
    stdout_tail TEXT,
    stderr_tail TEXT,
    error_msg TEXT,
    trigger_type TEXT NOT NULL DEFAULT 'schedule',
    llm_analysis TEXT,
    llm_tokens_used INTEGER,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_runs_job_name ON runs(job_name);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
`

// RunMigrations applies the database schema migrations.
func RunMigrations(db *sql.DB) error {
	_, err := db.Exec(migrationSQL)
	return err
}
