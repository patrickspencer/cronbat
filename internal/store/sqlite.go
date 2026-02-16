package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"
)

// NewRunID generates a new ULID-based run identifier.
func NewRunID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// SQLiteStore implements RunStore backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens the SQLite database at dbPath and runs migrations.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for use by other packages.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

const timeFormat = time.RFC3339Nano

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func formatTimePtr(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(timeFormat), Valid: true}
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeFormat, s)
}

func parseTimePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

// RecordRun inserts or updates a run record.
func (s *SQLiteStore) RecordRun(ctx context.Context, run *Run) error {
	if run.ID == "" {
		run.ID = NewRunID()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (
			id, job_name, status, exit_code, started_at, finished_at,
			duration_ms, stdout_tail, stderr_tail, error_msg, trigger_type,
			llm_analysis, llm_tokens_used, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			exit_code = excluded.exit_code,
			finished_at = excluded.finished_at,
			duration_ms = excluded.duration_ms,
			stdout_tail = excluded.stdout_tail,
			stderr_tail = excluded.stderr_tail,
			error_msg = excluded.error_msg,
			llm_analysis = excluded.llm_analysis,
			llm_tokens_used = excluded.llm_tokens_used`,
		run.ID,
		run.JobName,
		run.Status,
		run.ExitCode,
		formatTime(run.StartedAt),
		formatTimePtr(run.FinishedAt),
		run.DurationMs,
		nullString(run.StdoutTail),
		nullString(run.StderrTail),
		nullString(run.ErrorMsg),
		run.Trigger,
		nullString(run.LLMAnalysis),
		nullInt64(run.LLMTokensUsed),
		formatTime(run.CreatedAt),
	)
	return err
}

func (s *SQLiteStore) scanRun(row interface{ Scan(...any) error }) (*Run, error) {
	var r Run
	var startedAt, createdAt string
	var finishedAt, stdoutTail, stderrTail, errorMsg, llmAnalysis sql.NullString
	var exitCode, durationMs, llmTokensUsed sql.NullInt64

	err := row.Scan(
		&r.ID,
		&r.JobName,
		&r.Status,
		&exitCode,
		&startedAt,
		&finishedAt,
		&durationMs,
		&stdoutTail,
		&stderrTail,
		&errorMsg,
		&r.Trigger,
		&llmAnalysis,
		&llmTokensUsed,
		&createdAt,
	)
	if err != nil {
		return nil, err
	}

	r.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	r.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	r.FinishedAt, err = parseTimePtr(finishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse finished_at: %w", err)
	}

	if exitCode.Valid {
		r.ExitCode = int(exitCode.Int64)
	}
	if durationMs.Valid {
		r.DurationMs = durationMs.Int64
	}
	if stdoutTail.Valid {
		r.StdoutTail = stdoutTail.String
	}
	if stderrTail.Valid {
		r.StderrTail = stderrTail.String
	}
	if errorMsg.Valid {
		r.ErrorMsg = errorMsg.String
	}
	if llmAnalysis.Valid {
		r.LLMAnalysis = llmAnalysis.String
	}
	if llmTokensUsed.Valid {
		r.LLMTokensUsed = int(llmTokensUsed.Int64)
	}

	return &r, nil
}

const selectRunCols = `id, job_name, status, exit_code, started_at, finished_at,
	duration_ms, stdout_tail, stderr_tail, error_msg, trigger_type,
	llm_analysis, llm_tokens_used, created_at`

// GetRun retrieves a single run by ID.
func (s *SQLiteStore) GetRun(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+selectRunCols+" FROM runs WHERE id = ?", id)
	run, err := s.scanRun(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return run, err
}

// ListRuns returns runs matching the given options, ordered by started_at descending.
func (s *SQLiteStore) ListRuns(ctx context.Context, opts ListOpts) ([]*Run, error) {
	query := "SELECT " + selectRunCols + " FROM runs"
	var args []any

	if opts.JobName != "" {
		query += " WHERE job_name = ?"
		args = append(args, opts.JobName)
	}
	query += " ORDER BY started_at DESC"

	if opts.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.Limit)
	}
	if opts.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*Run
	for rows.Next() {
		r, err := s.scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetJobStats returns aggregate statistics for a given job.
func (s *SQLiteStore) GetJobStats(ctx context.Context, jobName string) (*JobStats, error) {
	var stats JobStats
	var lastRun sql.NullString
	var avgDuration sql.NullFloat64
	var successes, failures sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total_runs,
			SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS successes,
			SUM(CASE WHEN status = 'failure' THEN 1 ELSE 0 END) AS failures,
			MAX(started_at) AS last_run,
			AVG(duration_ms) AS avg_duration_ms
		FROM runs
		WHERE job_name = ?`, jobName).Scan(
		&stats.TotalRuns,
		&successes,
		&failures,
		&lastRun,
		&avgDuration,
	)
	if successes.Valid {
		stats.Successes = int(successes.Int64)
	}
	if failures.Valid {
		stats.Failures = int(failures.Int64)
	}
	if err != nil {
		return nil, err
	}

	if lastRun.Valid {
		t, err := parseTime(lastRun.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_run: %w", err)
		}
		stats.LastRun = &t
	}
	if avgDuration.Valid {
		stats.AvgDurationMs = avgDuration.Float64
	}

	return &stats, nil
}
