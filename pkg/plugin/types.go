package plugin

import "context"

// JobContext provides information about the job being executed.
type JobContext struct {
	JobName  string
	Schedule string
	Trigger  string // "schedule", "manual", "trigger:<name>"
	Env      map[string]string
	Metadata map[string]any
}

// RunResult holds the result of a job execution.
type RunResult struct {
	ExitCode          int
	Stdout            string
	Stderr            string
	DurationMs        int64
	Error             string
	StdoutLogPath     string
	StderrLogPath     string
	StdoutLogBytes    int64
	StderrLogBytes    int64
	StdoutTruncated   bool
	StderrTruncated   bool
	LogStorageWarning string
}

// NotifyEvent holds information for notification plugins.
type NotifyEvent struct {
	JobName  string
	Status   string // "success", "failure"
	Run      RunResult
	Analysis string // LLM analysis result, if any
	Metadata map[string]any
}

// LLMRequest represents a request to an LLM provider.
type LLMRequest struct {
	Model       string
	System      string
	Messages    []LLMMessage
	MaxTokens   int
	Temperature float64
	Metadata    map[string]any
}

// LLMMessage represents a single message in an LLM conversation.
type LLMMessage struct {
	Role    string // "user", "assistant"
	Content string
}

// LLMResponse represents a response from an LLM provider.
type LLMResponse struct {
	Content    string
	TokensUsed int
	Model      string
	Metadata   map[string]any
}

// LLMProvider is a shared service plugin that provides LLM capabilities.
type LLMProvider interface {
	Plugin
	Complete(ctx context.Context, req LLMRequest) (*LLMResponse, error)
}
