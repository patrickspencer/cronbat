package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/realtime"
	"github.com/patrickspencer/cronbat/internal/runlog"
	"github.com/patrickspencer/cronbat/internal/runner"
	"github.com/patrickspencer/cronbat/internal/scheduler"
	"github.com/patrickspencer/cronbat/internal/store"
	"github.com/patrickspencer/cronbat/internal/web"
	"github.com/patrickspencer/cronbat/pkg/plugin"
)

func main() {
	// Check for subcommands before flag parsing.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "wrap":
			os.Exit(runWrap(os.Args[2:]))
		case "cron-sync":
			os.Exit(runCronSync(os.Args[2:]))
		case "watchdog":
			os.Exit(runWatchdog(os.Args[2:]))
		}
	}

	configPath := flag.String("config", "cronbat.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Ensure data directory exists.
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory %s: %v", cfg.DataDir, err)
	}
	if err := os.MkdirAll(cfg.JobsDir, 0755); err != nil {
		log.Fatalf("failed to create jobs directory %s: %v", cfg.JobsDir, err)
	}

	// Open SQLite store.
	dbPath := filepath.Join(cfg.DataDir, "cronbat.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()
	log.Printf("store opened at %s", dbPath)

	// Load jobs.
	jobs, err := config.LoadJobs(cfg.JobsDir)
	if err != nil {
		log.Fatalf("failed to load jobs from %s: %v", cfg.JobsDir, err)
	}
	log.Printf("loaded %d job(s)", len(jobs))

	// Build job lookup map protected by mutex for runtime job management.
	var jobsMu sync.RWMutex
	jobMap := make(map[string]*config.Job, len(jobs))
	jobStateMap := make(map[string]string, len(jobs))
	for _, j := range jobs {
		jobMap[j.Name] = j
		if j.IsEnabled() {
			jobStateMap[j.Name] = "started"
		} else {
			jobStateMap[j.Name] = "stopped"
		}
	}

	cloneJob := func(j *config.Job) *config.Job {
		cp := *j
		if j.Enabled != nil {
			v := *j.Enabled
			cp.Enabled = &v
		}
		return &cp
	}

	jobFilePath := func(j *config.Job) string {
		if j.FilePath != "" {
			return j.FilePath
		}
		return filepath.Join(cfg.JobsDir, j.Name+".yaml")
	}

	getJobs := func() []*config.Job {
		jobsMu.RLock()
		defer jobsMu.RUnlock()
		result := make([]*config.Job, 0, len(jobMap))
		for _, j := range jobMap {
			result = append(result, cloneJob(j))
		}
		return result
	}

	getJobState := func(name string) string {
		jobsMu.RLock()
		defer jobsMu.RUnlock()

		if state := jobStateMap[name]; state != "" {
			return state
		}
		j, ok := jobMap[name]
		if !ok {
			return ""
		}
		if j.IsEnabled() {
			return "started"
		}
		return "stopped"
	}

	getConfigSnapshot := func() *config.Config {
		cp := *cfg
		if cfg.RunLogs.Enabled != nil {
			v := *cfg.RunLogs.Enabled
			cp.RunLogs.Enabled = &v
		}
		return &cp
	}

	events := realtime.NewBroker()
	runLogManager := runlog.NewManager(
		cfg.RunLogs.Dir,
		cfg.RunLogs.MaxBytesPerStream,
		cfg.RunLogs.RetentionDays,
		cfg.RunLogs.MaxTotalMB*1024*1024,
	)

	if cfg.RunLogs.IsEnabled() {
		if err := os.MkdirAll(runLogManager.BaseDir(), 0755); err != nil {
			log.Fatalf("failed to create run logs directory %s: %v", runLogManager.BaseDir(), err)
		}
		if err := runLogManager.Cleanup(); err != nil {
			log.Printf("WARN: run log cleanup failed: %v", err)
		}
		log.Printf(
			"run log storage enabled: dir=%s max_bytes_per_stream=%d retention_days=%d max_total_mb=%d",
			runLogManager.BaseDir(),
			cfg.RunLogs.MaxBytesPerStream,
			cfg.RunLogs.RetentionDays,
			cfg.RunLogs.MaxTotalMB,
		)
	} else {
		log.Printf("run log storage disabled")
	}

	r := runner.NewRunner()

	// executeJob runs a job and records the result in the store.
	executeJob := func(jobName string, trigger string) {
		jobsMu.RLock()
		j, ok := jobMap[jobName]
		if ok {
			j = cloneJob(j)
		}
		jobsMu.RUnlock()
		if !ok {
			log.Printf("WARN: job %q not found for execution", jobName)
			return
		}
		if !j.IsEnabled() {
			log.Printf("DEBUG: skipping disabled job %q", jobName)
			return
		}

		timeout, err := j.ParseTimeout()
		if err != nil {
			log.Printf("ERROR: invalid timeout for job %q: %v", jobName, err)
			return
		}

		jctx := plugin.JobContext{
			JobName:  j.Name,
			Schedule: j.Schedule,
			Trigger:  trigger,
			Env:      j.Env,
			Metadata: j.Metadata,
		}

		log.Printf("executing job %q (trigger=%s)", jobName, trigger)
		startedAt := time.Now().UTC()
		runID := store.NewRunID()

		run := &store.Run{
			ID:        runID,
			JobName:   jobName,
			Status:    "running",
			StartedAt: startedAt,
			Trigger:   trigger,
		}
		if err := st.RecordRun(context.Background(), run); err != nil {
			log.Printf("ERROR: failed to record run start: %v", err)
		}
		events.Publish(realtime.Event{
			Type:    "run.started",
			JobName: jobName,
			RunID:   runID,
			Status:  "running",
			Trigger: trigger,
		})

		var runOpts runner.RunOptions
		var fileWriters *runlog.RunWriters
		if cfg.RunLogs.IsEnabled() {
			writers, err := runLogManager.OpenRunWriters(jobName, runID)
			if err != nil {
				log.Printf("WARN: failed to open persistent log files for run %s: %v", runID, err)
			} else {
				fileWriters = writers
				runOpts.ExtraStdout = fileWriters.Stdout
				runOpts.ExtraStderr = fileWriters.Stderr
			}
		}

		runOpts.WorkDir = j.WorkingDir
		result := r.Run(context.Background(), j.Command, jctx, timeout, &runOpts)

		if fileWriters != nil {
			closeErr := fileWriters.Close()
			result.StdoutLogPath = fileWriters.StdoutPath
			result.StderrLogPath = fileWriters.StderrPath
			result.StdoutLogBytes = fileWriters.Stdout.WrittenBytes()
			result.StderrLogBytes = fileWriters.Stderr.WrittenBytes()
			result.StdoutTruncated = fileWriters.Stdout.Truncated()
			result.StderrTruncated = fileWriters.Stderr.Truncated()
			if closeErr != nil {
				result.LogStorageWarning = closeErr.Error()
			}
		}

		finishedAt := time.Now().UTC()
		status := "success"
		if result.ExitCode != 0 || result.Error != "" {
			status = "failure"
		}

		run.Status = status
		run.ExitCode = result.ExitCode
		run.FinishedAt = &finishedAt
		run.DurationMs = result.DurationMs
		run.StdoutTail = result.Stdout
		run.StderrTail = result.Stderr
		run.ErrorMsg = result.Error

		if err := st.RecordRun(context.Background(), run); err != nil {
			log.Printf("ERROR: failed to record run result: %v", err)
		}
		events.Publish(realtime.Event{
			Type:    "run.completed",
			JobName: jobName,
			RunID:   runID,
			Status:  status,
			Trigger: trigger,
		})

		log.Printf("job %q completed: status=%s duration=%dms", jobName, status, result.DurationMs)
	}

	// Set up scheduler.
	sched := scheduler.NewScheduler(func(jobName string) {
		executeJob(jobName, "schedule")
	})
	applyScheduleLocked := func(j *config.Job) error {
		sched.RemoveJob(j.Name)
		if !j.IsEnabled() {
			return nil
		}
		schedule, err := scheduler.ParseSchedule(j.Schedule)
		if err != nil {
			return err
		}
		sched.AddJob(j.Name, schedule)
		return nil
	}

	isSafeJobName := func(name string) bool {
		if name == "" {
			return false
		}
		for _, ch := range name {
			isLower := ch >= 'a' && ch <= 'z'
			isUpper := ch >= 'A' && ch <= 'Z'
			isDigit := ch >= '0' && ch <= '9'
			if isLower || isUpper || isDigit || ch == '-' || ch == '_' || ch == '.' {
				continue
			}
			return false
		}
		return true
	}

	validateJob := func(j *config.Job) error {
		j.Name = strings.TrimSpace(j.Name)
		j.Schedule = strings.TrimSpace(j.Schedule)
		j.Command = strings.TrimSpace(j.Command)
		j.WorkingDir = strings.TrimSpace(j.WorkingDir)
		j.Executor = strings.TrimSpace(j.Executor)
		j.Timeout = strings.TrimSpace(j.Timeout)

		if j.Name == "" {
			return errors.New("job name is required")
		}
		if !isSafeJobName(j.Name) {
			return errors.New("invalid job name: use only letters, numbers, '.', '-', '_'")
		}
		if j.Schedule == "" {
			return errors.New("job schedule is required")
		}
		if j.Command == "" {
			return errors.New("job command is required")
		}
		if j.Executor == "" {
			j.Executor = "shell"
		}
		if _, err := j.ParseTimeout(); err != nil {
			return fmt.Errorf("invalid timeout: %w", err)
		}
		return nil
	}

	saveJobLocked := func(j *config.Job) error {
		return config.SaveJob(jobFilePath(j), j)
	}

	for _, j := range jobs {
		if err := applyScheduleLocked(j); err != nil {
			log.Printf("ERROR: invalid schedule for job %q (%s), skipping: %v", j.Name, j.Schedule, err)
			continue
		}
		if next, ok := sched.NextRunTime(j.Name); ok {
			log.Printf("scheduled job %q, next run at %s", j.Name, next.Format(time.RFC3339))
		}
	}
	sched.Start()

	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	cleanupEvery, err := time.ParseDuration(cfg.RunLogs.CleanupInterval)
	if err != nil || cleanupEvery <= 0 {
		cleanupEvery = time.Hour
	}
	if cfg.RunLogs.IsEnabled() {
		go func() {
			ticker := time.NewTicker(cleanupEvery)
			defer ticker.Stop()
			for {
				select {
				case <-cleanupCtx.Done():
					return
				case <-ticker.C:
					if err := runLogManager.Cleanup(); err != nil {
						log.Printf("WARN: run log cleanup failed: %v", err)
					}
				}
			}
		}()
	}

	triggerRun := func(jobName string) {
		executeJob(jobName, "manual")
	}

	createJob := func(newJob config.Job) error {
		candidate := &newJob
		if err := validateJob(candidate); err != nil {
			return err
		}

		jobsMu.Lock()
		defer jobsMu.Unlock()

		if _, exists := jobMap[candidate.Name]; exists {
			return fmt.Errorf("job already exists: %s", candidate.Name)
		}

		candidate.FilePath = filepath.Join(cfg.JobsDir, candidate.Name+".yaml")
		if err := applyScheduleLocked(candidate); err != nil {
			sched.RemoveJob(candidate.Name)
			return err
		}
		if err := config.SaveJob(candidate.FilePath, candidate); err != nil {
			sched.RemoveJob(candidate.Name)
			return err
		}

		jobMap[candidate.Name] = candidate
		if candidate.IsEnabled() {
			jobStateMap[candidate.Name] = "started"
		} else {
			jobStateMap[candidate.Name] = "stopped"
		}
		return nil
	}

	setJobEnabled := func(name string, enabled bool) error {
		jobsMu.Lock()
		defer jobsMu.Unlock()

		j, ok := jobMap[name]
		if !ok {
			return fmt.Errorf("job not found: %s", name)
		}

		old := cloneJob(j)
		if enabled {
			t := true
			j.Enabled = &t
		} else {
			f := false
			j.Enabled = &f
		}

		if err := applyScheduleLocked(j); err != nil {
			*j = *old
			_ = applyScheduleLocked(j)
			return err
		}
		if err := saveJobLocked(j); err != nil {
			*j = *old
			_ = applyScheduleLocked(j)
			return err
		}
		if enabled {
			jobStateMap[name] = "started"
		} else {
			jobStateMap[name] = "stopped"
		}
		return nil
	}

	enableJob := func(name string) error {
		return setJobEnabled(name, true)
	}

	disableJob := func(name string) error {
		return setJobEnabled(name, false)
	}

	startJob := func(name string) error {
		if err := setJobEnabled(name, true); err != nil {
			return err
		}
		jobsMu.Lock()
		jobStateMap[name] = "started"
		jobsMu.Unlock()
		return nil
	}

	stopJob := func(name string) error {
		if err := setJobEnabled(name, false); err != nil {
			return err
		}
		jobsMu.Lock()
		jobStateMap[name] = "stopped"
		jobsMu.Unlock()
		return nil
	}

	pauseJob := func(name string) error {
		if err := setJobEnabled(name, false); err != nil {
			return err
		}
		jobsMu.Lock()
		jobStateMap[name] = "paused"
		jobsMu.Unlock()
		return nil
	}

	archiveJob := func(name string) error {
		jobsMu.Lock()
		defer jobsMu.Unlock()

		j, ok := jobMap[name]
		if !ok {
			return fmt.Errorf("job not found: %s", name)
		}

		sched.RemoveJob(name)

		archiveDir := filepath.Join(cfg.JobsDir, "archive")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			return err
		}

		srcPath := jobFilePath(j)
		archiveName := fmt.Sprintf("%s-%s.yaml", j.Name, time.Now().UTC().Format("20060102T150405Z"))
		dstPath := filepath.Join(archiveDir, archiveName)

		if err := os.Rename(srcPath, dstPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			// If file is missing, persist the in-memory job snapshot into the archive.
			archivedCopy := cloneJob(j)
			archivedCopy.FilePath = dstPath
			if err := config.SaveJob(dstPath, archivedCopy); err != nil {
				return err
			}
		}

		delete(jobMap, name)
		delete(jobStateMap, name)
		return nil
	}

	deleteJob := func(name string) error {
		jobsMu.Lock()
		defer jobsMu.Unlock()

		j, ok := jobMap[name]
		if !ok {
			return fmt.Errorf("job not found: %s", name)
		}

		path := jobFilePath(j)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		delete(jobMap, name)
		delete(jobStateMap, name)
		sched.RemoveJob(name)
		return nil
	}

	getJobYAML := func(name string) (string, error) {
		jobsMu.RLock()
		j, ok := jobMap[name]
		if !ok {
			jobsMu.RUnlock()
			return "", fmt.Errorf("job not found: %s", name)
		}
		snapshot := cloneJob(j)
		path := jobFilePath(snapshot)
		jobsMu.RUnlock()

		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		// Fallback for jobs that exist in memory but have no file on disk.
		raw, err := config.MarshalJobYAML(snapshot)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}

	readRunLogs := func(jobName string, runID string) (stdout string, stderr string, stdoutPath string, stderrPath string, err error) {
		if !cfg.RunLogs.IsEnabled() {
			return "", "", "", "", os.ErrNotExist
		}
		return runLogManager.ReadRunLogs(jobName, runID)
	}

	updateJobYAML := func(name string, data string) (string, error) {
		parsed, err := config.ParseJobYAML([]byte(data))
		if err != nil {
			return "", err
		}
		parsed.Name = strings.TrimSpace(parsed.Name)
		if parsed.Name == "" {
			return "", errors.New("job name is required in YAML")
		}
		if err := validateJob(parsed); err != nil {
			return "", err
		}
		if !strings.HasSuffix(data, "\n") {
			data += "\n"
		}

		jobsMu.Lock()
		defer jobsMu.Unlock()

		current, ok := jobMap[name]
		if !ok {
			return "", fmt.Errorf("job not found: %s", name)
		}

		newName := parsed.Name
		if newName != name {
			if _, exists := jobMap[newName]; exists {
				return "", fmt.Errorf("job already exists: %s", newName)
			}
		}

		old := cloneJob(current)
		oldState, hadOldState := jobStateMap[name]
		oldPath := jobFilePath(current)
		newPath := filepath.Join(cfg.JobsDir, newName+".yaml")
		parsed.FilePath = newPath

		nextState := oldState
		if parsed.IsEnabled() {
			nextState = "started"
		} else if nextState == "" || nextState == "started" {
			nextState = "stopped"
		}

		*current = *parsed
		if newName != name {
			delete(jobMap, name)
			jobMap[newName] = current
		}
		if newName != name {
			delete(jobStateMap, name)
		}
		jobStateMap[newName] = nextState

		// Refresh schedule with potential new name/schedule.
		sched.RemoveJob(name)
		if err := applyScheduleLocked(current); err != nil {
			if newName != name {
				delete(jobMap, newName)
				jobMap[name] = current
				delete(jobStateMap, newName)
			}
			if hadOldState {
				jobStateMap[name] = oldState
			} else {
				delete(jobStateMap, name)
			}
			*current = *old
			_ = applyScheduleLocked(current)
			return "", err
		}

		restore := func() {
			sched.RemoveJob(name)
			sched.RemoveJob(newName)
			if newName != name {
				delete(jobMap, newName)
				jobMap[name] = current
				delete(jobStateMap, newName)
			}
			if hadOldState {
				jobStateMap[name] = oldState
			} else {
				delete(jobStateMap, name)
			}
			*current = *old
			_ = applyScheduleLocked(current)
		}

		if err := config.SaveJob(newPath, current); err != nil {
			restore()
			return "", err
		}

		if newPath != oldPath {
			if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				restore()
				_ = os.Remove(newPath)
				return "", err
			}
		}
		return newName, nil
	}

	updateJobSettings := func(name string, updated config.Job) error {
		jobsMu.Lock()
		defer jobsMu.Unlock()

		current, ok := jobMap[name]
		if !ok {
			return fmt.Errorf("job not found: %s", name)
		}

		candidate := cloneJob(current)
		candidate.Name = name
		candidate.Schedule = strings.TrimSpace(updated.Schedule)
		candidate.Command = strings.TrimSpace(updated.Command)
		candidate.WorkingDir = strings.TrimSpace(updated.WorkingDir)
		candidate.Executor = strings.TrimSpace(updated.Executor)
		if candidate.Executor == "" {
			candidate.Executor = "shell"
		}
		candidate.Timeout = strings.TrimSpace(updated.Timeout)
		candidate.Env = updated.Env
		candidate.OnSuccess = updated.OnSuccess
		candidate.OnFailure = updated.OnFailure
		candidate.Metadata = updated.Metadata
		candidate.Analyze = updated.Analyze
		if updated.Enabled != nil {
			v := *updated.Enabled
			candidate.Enabled = &v
		}

		if err := validateJob(candidate); err != nil {
			return err
		}

		old := cloneJob(current)
		oldState, hadOldState := jobStateMap[name]
		candidate.FilePath = jobFilePath(current)
		*current = *candidate

		if err := applyScheduleLocked(current); err != nil {
			*current = *old
			if hadOldState {
				jobStateMap[name] = oldState
			} else {
				delete(jobStateMap, name)
			}
			_ = applyScheduleLocked(current)
			return err
		}
		if err := saveJobLocked(current); err != nil {
			*current = *old
			if hadOldState {
				jobStateMap[name] = oldState
			} else {
				delete(jobStateMap, name)
			}
			_ = applyScheduleLocked(current)
			return err
		}
		if current.IsEnabled() {
			jobStateMap[name] = "started"
		} else if jobStateMap[name] == "" || jobStateMap[name] == "started" {
			jobStateMap[name] = "stopped"
		}
		return nil
	}

	// Set up HTTP server.
	srv := web.NewServer(
		cfg.Listen,
		st,
		events,
		getConfigSnapshot,
		getJobs,
		getJobState,
		createJob,
		readRunLogs,
		triggerRun,
		sched.NextRunTime,
		enableJob,
		disableJob,
		startJob,
		stopJob,
		pauseJob,
		archiveJob,
		deleteJob,
		getJobYAML,
		updateJobYAML,
		updateJobSettings,
	)

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", err)
		}
	}()

	log.Printf("cronbat started, listening on %s", cfg.Listen)

	<-sigCh
	log.Println("shutting down...")

	cleanupCancel()
	sched.Stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("ERROR: http server shutdown error: %v", err)
	}

	log.Println("cronbat stopped")
}
