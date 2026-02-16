package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/runlog"
	"github.com/patrickspencer/cronbat/internal/runner"
	"github.com/patrickspencer/cronbat/internal/store"
	"github.com/patrickspencer/cronbat/pkg/plugin"
)

func runWrap(args []string) int {
	fs := flag.NewFlagSet("wrap", flag.ExitOnError)
	name := fs.String("name", "", "job name for recording (required)")
	configPath := fs.String("config", "cronbat.yaml", "path to config file")
	apiURL := fs.String("api", "", "if set, record via API instead of direct DB access")
	timeout := fs.Duration("timeout", 0, "optional command timeout")

	// Find "--" separator for the wrapped command.
	var wrapArgs, cmdArgs []string
	for i, a := range args {
		if a == "--" {
			wrapArgs = args[:i]
			cmdArgs = args[i+1:]
			break
		}
	}
	if cmdArgs == nil {
		wrapArgs = args
	}

	fs.Parse(wrapArgs)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		fs.Usage()
		return 1
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "error: no command specified after --")
		fs.Usage()
		return 1
	}

	command := strings.Join(cmdArgs, " ")

	if *apiURL != "" {
		return wrapViaAPI(*apiURL, *name, command, *timeout)
	}
	return wrapDirect(*configPath, *name, command, *timeout)
}

func wrapDirect(configPath, jobName, command string, timeout time.Duration) int {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		return 1
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating data dir: %v\n", err)
		return 1
	}

	dbPath := filepath.Join(cfg.DataDir, "cronbat.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening store: %v\n", err)
		return 1
	}
	defer st.Close()

	runLogManager := runlog.NewManager(
		cfg.RunLogs.Dir,
		cfg.RunLogs.MaxBytesPerStream,
		cfg.RunLogs.RetentionDays,
		cfg.RunLogs.MaxTotalMB*1024*1024,
	)

	runID := store.NewRunID()
	startedAt := time.Now().UTC()

	run := &store.Run{
		ID:        runID,
		JobName:   jobName,
		Status:    "running",
		StartedAt: startedAt,
		Trigger:   "cron",
	}
	if err := st.RecordRun(context.Background(), run); err != nil {
		log.Printf("WARN: failed to record run start: %v", err)
	}

	r := runner.NewRunner()
	jctx := plugin.JobContext{
		JobName: jobName,
		Trigger: "cron",
	}

	var runOpts runner.RunOptions
	var fileWriters *runlog.RunWriters
	if cfg.RunLogs.IsEnabled() {
		if err := os.MkdirAll(runLogManager.BaseDir(), 0755); err == nil {
			writers, err := runLogManager.OpenRunWriters(jobName, runID)
			if err != nil {
				log.Printf("WARN: failed to open log files: %v", err)
			} else {
				fileWriters = writers
				runOpts.ExtraStdout = fileWriters.Stdout
				runOpts.ExtraStderr = fileWriters.Stderr
			}
		}
	}

	result := r.Run(context.Background(), command, jctx, timeout, &runOpts)

	if fileWriters != nil {
		_ = fileWriters.Close()
	}

	// Also write to real stdout/stderr so cron can capture output for MAILTO.
	if result.Stdout != "" {
		os.Stdout.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		os.Stderr.WriteString(result.Stderr)
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
		log.Printf("WARN: failed to record run result: %v", err)
	}

	return result.ExitCode
}

func wrapViaAPI(apiURL, jobName, command string, timeout time.Duration) int {
	apiURL = strings.TrimRight(apiURL, "/")

	// Execute command locally.
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmdErr := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	errMsg := ""
	if cmdErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "timeout"
		} else {
			errMsg = cmdErr.Error()
		}
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Write to real stdout/stderr.
	os.Stdout.Write(stdout.Bytes())
	os.Stderr.Write(stderr.Bytes())

	// Truncate output for API payload.
	const maxTail = 64 * 1024
	stdoutStr := stdout.String()
	if len(stdoutStr) > maxTail {
		stdoutStr = stdoutStr[len(stdoutStr)-maxTail:]
	}
	stderrStr := stderr.String()
	if len(stderrStr) > maxTail {
		stderrStr = stderrStr[len(stderrStr)-maxTail:]
	}

	status := "success"
	if exitCode != 0 || errMsg != "" {
		status = "failure"
	}

	payload := map[string]any{
		"job_name":    jobName,
		"status":      status,
		"exit_code":   exitCode,
		"duration_ms": durationMs,
		"stdout_tail": stdoutStr,
		"stderr_tail": stderrStr,
		"error_msg":   errMsg,
		"trigger":     "cron",
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(apiURL+"/api/v1/jobs/"+jobName+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("WARN: failed to POST run result to API: %v", err)
	} else {
		resp.Body.Close()
	}

	return exitCode
}
