package runner

import (
	"context"
	"io"
	"os/exec"
	"time"

	"github.com/patrickspencer/cronbat/pkg/plugin"
)

const ringBufSize = 64 * 1024 // 64KB

// RingBuffer is a fixed-size circular buffer that implements io.Writer.
// It retains only the most recent bytes written, up to its capacity.
type RingBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
}

// NewRingBuffer creates a RingBuffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{buf: make([]byte, size), size: size}
}

// Write implements io.Writer. It writes p into the ring buffer,
// overwriting the oldest data if capacity is exceeded.
func (rb *RingBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= rb.size {
		// Data larger than buffer; keep only the tail.
		copy(rb.buf, p[n-rb.size:])
		rb.pos = 0
		rb.full = true
		return n, nil
	}

	// Copy what fits before wrap-around.
	oldPos := rb.pos
	first := rb.size - rb.pos
	if first >= n {
		copy(rb.buf[rb.pos:], p)
	} else {
		copy(rb.buf[rb.pos:], p[:first])
		copy(rb.buf, p[first:])
	}

	rb.pos = (rb.pos + n) % rb.size
	if !rb.full && rb.pos <= oldPos {
		rb.full = true
	}
	return n, nil
}

// String returns the buffered contents in chronological order.
func (rb *RingBuffer) String() string {
	if !rb.full {
		return string(rb.buf[:rb.pos])
	}
	// Buffer is full: data from pos..end is oldest, then 0..pos is newest.
	out := make([]byte, rb.size)
	n := copy(out, rb.buf[rb.pos:])
	copy(out[n:], rb.buf[:rb.pos])
	return string(out)
}

// Runner executes shell commands for jobs.
type Runner struct{}

// RunOptions controls optional output destinations for a command run.
type RunOptions struct {
	ExtraStdout io.Writer
	ExtraStderr io.Writer
	WorkDir     string
}

// NewRunner creates a new Runner.
func NewRunner() *Runner {
	return &Runner{}
}

// Run executes the given shell command with the provided job context and timeout.
func (r *Runner) Run(ctx context.Context, command string, job plugin.JobContext, timeout time.Duration, opts *RunOptions) *plugin.RunResult {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = BuildEnv(nil, job)
	if opts != nil && opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	stdoutBuf := NewRingBuffer(ringBufSize)
	stderrBuf := NewRingBuffer(ringBufSize)

	if opts != nil {
		cmd.Stdout = newTeeWriter(stdoutBuf, opts.ExtraStdout)
		cmd.Stderr = newTeeWriter(stderrBuf, opts.ExtraStderr)
	} else {
		cmd.Stdout = stdoutBuf
		cmd.Stderr = stderrBuf
	}

	start := time.Now()
	err := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	result := &plugin.RunResult{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMs: durationMs,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result.Error = "timeout"
		} else {
			result.Error = err.Error()
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	return result
}

type teeWriter struct {
	primary   io.Writer
	secondary io.Writer
}

func newTeeWriter(primary io.Writer, secondary io.Writer) io.Writer {
	if secondary == nil {
		return primary
	}
	return &teeWriter{
		primary:   primary,
		secondary: secondary,
	}
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.primary.Write(p)
	if t.secondary != nil {
		_, _ = t.secondary.Write(p)
	}
	return n, err
}
