package store

import (
	"context"
	"time"
)

// Run represents a single execution of a cron job.
type Run struct {
	ID            string
	JobName       string
	Status        string // "running", "success", "failure"
	ExitCode      int
	StartedAt     time.Time
	FinishedAt    *time.Time
	DurationMs    int64
	StdoutTail    string
	StderrTail    string
	ErrorMsg      string
	Trigger       string
	LLMAnalysis   string
	LLMTokensUsed int
	CreatedAt     time.Time
}

// ListOpts controls filtering and pagination for run queries.
type ListOpts struct {
	JobName string
	Limit   int
	Offset  int
}

// JobStats holds aggregate statistics for a job.
type JobStats struct {
	TotalRuns     int
	Successes     int
	Failures      int
	LastRun       *time.Time
	AvgDurationMs float64
}

// RunStore is the interface for persisting and querying job runs.
type RunStore interface {
	RecordRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	ListRuns(ctx context.Context, opts ListOpts) ([]*Run, error)
	GetJobStats(ctx context.Context, jobName string) (*JobStats, error)
}
