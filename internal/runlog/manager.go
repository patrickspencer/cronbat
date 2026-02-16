package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	stdoutSuffix = ".stdout.log"
	stderrSuffix = ".stderr.log"
)

// Manager handles persistent per-run stdout/stderr log files and retention.
type Manager struct {
	baseDir           string
	maxBytesPerStream int64
	retentionDays     int
	maxTotalBytes     int64
}

// NewManager creates a new run log manager.
func NewManager(baseDir string, maxBytesPerStream int64, retentionDays int, maxTotalBytes int64) *Manager {
	return &Manager{
		baseDir:           baseDir,
		maxBytesPerStream: maxBytesPerStream,
		retentionDays:     retentionDays,
		maxTotalBytes:     maxTotalBytes,
	}
}

// BaseDir returns the base log directory.
func (m *Manager) BaseDir() string {
	return m.baseDir
}

// Paths returns stdout/stderr log file paths for a run.
func (m *Manager) Paths(jobName, runID string) (string, string) {
	safeJob := sanitizeSegment(jobName)
	dir := filepath.Join(m.baseDir, safeJob)
	return filepath.Join(dir, runID+stdoutSuffix), filepath.Join(dir, runID+stderrSuffix)
}

// OpenRunWriters opens capped stdout/stderr writers for the run.
func (m *Manager) OpenRunWriters(jobName, runID string) (*RunWriters, error) {
	stdoutPath, stderrPath := m.Paths(jobName, runID)
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0755); err != nil {
		return nil, err
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, err
	}

	return &RunWriters{
		Stdout:     NewCappedFileWriter(stdoutFile, m.maxBytesPerStream),
		Stderr:     NewCappedFileWriter(stderrFile, m.maxBytesPerStream),
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	}, nil
}

// ReadRunLogs reads persisted logs for the run.
// If neither file exists, os.ErrNotExist is returned.
func (m *Manager) ReadRunLogs(jobName, runID string) (stdout string, stderr string, stdoutPath string, stderrPath string, err error) {
	stdoutPath, stderrPath = m.Paths(jobName, runID)

	stdoutData, stdoutErr := os.ReadFile(stdoutPath)
	stderrData, stderrErr := os.ReadFile(stderrPath)

	switch {
	case stdoutErr == nil && stderrErr == nil:
		return string(stdoutData), string(stderrData), stdoutPath, stderrPath, nil
	case stdoutErr == nil && errors.Is(stderrErr, os.ErrNotExist):
		return string(stdoutData), "", stdoutPath, stderrPath, nil
	case stderrErr == nil && errors.Is(stdoutErr, os.ErrNotExist):
		return "", string(stderrData), stdoutPath, stderrPath, nil
	case errors.Is(stdoutErr, os.ErrNotExist) && errors.Is(stderrErr, os.ErrNotExist):
		return "", "", stdoutPath, stderrPath, os.ErrNotExist
	case stdoutErr != nil:
		return "", "", stdoutPath, stderrPath, stdoutErr
	default:
		return "", "", stdoutPath, stderrPath, stderrErr
	}
}

// Cleanup removes old logs and enforces a maximum total log size.
func (m *Manager) Cleanup() error {
	cutoff := time.Now().AddDate(0, 0, -m.retentionDays)

	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}

	var files []fileInfo

	err := filepath.WalkDir(m.baseDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, stdoutSuffix) && !strings.HasSuffix(path, stderrSuffix) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			return nil
		}

		files = append(files, fileInfo{
			path:    path,
			size:    info.Size(),
			modTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	if m.maxTotalBytes <= 0 {
		return nil
	}

	var total int64
	for _, f := range files {
		total += f.size
	}
	if total <= m.maxTotalBytes {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if total <= m.maxTotalBytes {
			break
		}
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		total -= f.size
	}

	return nil
}

// RunWriters holds stdout/stderr writers for one run.
type RunWriters struct {
	Stdout     *CappedFileWriter
	Stderr     *CappedFileWriter
	StdoutPath string
	StderrPath string
}

// Close closes both writers.
func (r *RunWriters) Close() error {
	var firstErr error
	if r.Stdout != nil {
		if err := r.Stdout.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.Stderr != nil {
		if err := r.Stderr.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// CappedFileWriter writes to a file up to maxBytes, then discards new bytes.
type CappedFileWriter struct {
	file      *os.File
	maxBytes  int64
	written   int64
	truncated bool
}

// NewCappedFileWriter creates a capped writer.
func NewCappedFileWriter(file *os.File, maxBytes int64) *CappedFileWriter {
	return &CappedFileWriter{
		file:     file,
		maxBytes: maxBytes,
	}
}

// Write stores as much as allowed, discarding excess bytes while reporting success.
func (w *CappedFileWriter) Write(p []byte) (int, error) {
	if w.maxBytes <= 0 {
		w.truncated = true
		return len(p), nil
	}

	remaining := w.maxBytes - w.written
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}

	toWrite := p
	if int64(len(p)) > remaining {
		toWrite = p[:remaining]
		w.truncated = true
	}

	n, err := w.file.Write(toWrite)
	if err != nil {
		// Ignore file write errors so job execution does not fail on log storage issues.
		return len(p), nil
	}
	w.written += int64(n)
	return len(p), nil
}

// Close closes the underlying file.
func (w *CappedFileWriter) Close() error {
	return w.file.Close()
}

// WrittenBytes returns the number of bytes persisted.
func (w *CappedFileWriter) WrittenBytes() int64 {
	return w.written
}

// Truncated reports whether content exceeded maxBytes.
func (w *CappedFileWriter) Truncated() bool {
	return w.truncated
}

func sanitizeSegment(value string) string {
	if value == "" {
		return "unknown"
	}

	var b strings.Builder
	b.Grow(len(value))
	for _, ch := range value {
		isLower := ch >= 'a' && ch <= 'z'
		isUpper := ch >= 'A' && ch <= 'Z'
		isDigit := ch >= '0' && ch <= '9'
		if isLower || isUpper || isDigit || ch == '-' || ch == '_' || ch == '.' {
			b.WriteRune(ch)
			continue
		}
		b.WriteByte('_')
	}
	result := strings.Trim(b.String(), "._")
	if result == "" {
		return "unknown"
	}
	return result
}
