package plugin

import "context"

// Plugin is the base interface all plugins must implement.
type Plugin interface {
	Name() string
	Init(config map[string]any) error
	Close() error
}

// Executor runs a job and returns the result.
type Executor interface {
	Plugin
	Execute(ctx context.Context, job JobContext) (*RunResult, error)
}

// Notifier sends notifications about job events.
type Notifier interface {
	Plugin
	Notify(ctx context.Context, event NotifyEvent) error
}

// Trigger watches for external events and fires jobs.
type Trigger interface {
	Plugin
	Start(ctx context.Context, fire func(jobName string)) error
	Stop() error
}
