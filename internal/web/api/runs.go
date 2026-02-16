package api

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/patrickspencer/cronbat/internal/store"
)

type runResponse struct {
	ID            string     `json:"id"`
	JobName       string     `json:"job_name"`
	Status        string     `json:"status"`
	ExitCode      int        `json:"exit_code"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	DurationMs    int64      `json:"duration_ms"`
	StdoutTail    string     `json:"stdout_tail,omitempty"`
	StderrTail    string     `json:"stderr_tail,omitempty"`
	ErrorMsg      string     `json:"error_msg,omitempty"`
	Trigger       string     `json:"trigger"`
	LLMAnalysis   string     `json:"llm_analysis,omitempty"`
	LLMTokensUsed int        `json:"llm_tokens_used,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

func runToResponse(r *store.Run) runResponse {
	return runResponse{
		ID:            r.ID,
		JobName:       r.JobName,
		Status:        r.Status,
		ExitCode:      r.ExitCode,
		StartedAt:     r.StartedAt,
		FinishedAt:    r.FinishedAt,
		DurationMs:    r.DurationMs,
		StdoutTail:    r.StdoutTail,
		StderrTail:    r.StderrTail,
		ErrorMsg:      r.ErrorMsg,
		Trigger:       r.Trigger,
		LLMAnalysis:   r.LLMAnalysis,
		LLMTokensUsed: r.LLMTokensUsed,
		CreatedAt:     r.CreatedAt,
	}
}

func (a *API) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	q := r.URL.Query()
	opts := store.ListOpts{
		JobName: q.Get("job"),
		Limit:   50,
	}

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			opts.Offset = n
		}
	}

	runs, err := a.Store.ListRuns(r.Context(), opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list runs"})
		return
	}

	result := make([]runResponse, 0, len(runs))
	for _, run := range runs {
		result = append(result, runToResponse(run))
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleGetRun(w http.ResponseWriter, r *http.Request, id string) {
	run, err := a.Store.GetRun(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get run"})
		return
	}
	if run == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}

	writeJSON(w, http.StatusOK, runToResponse(run))
}

type runLogsResponse struct {
	RunID        string `json:"run_id"`
	JobName      string `json:"job_name"`
	Source       string `json:"source"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	StdoutPath   string `json:"stdout_path,omitempty"`
	StderrPath   string `json:"stderr_path,omitempty"`
	StdoutTail   string `json:"stdout_tail,omitempty"`
	StderrTail   string `json:"stderr_tail,omitempty"`
	StorageError string `json:"storage_error,omitempty"`
}

func (a *API) handleGetRunLogs(w http.ResponseWriter, r *http.Request, id string) {
	run, err := a.Store.GetRun(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get run"})
		return
	}
	if run == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}

	resp := runLogsResponse{
		RunID:      run.ID,
		JobName:    run.JobName,
		Source:     "tail",
		Stdout:     run.StdoutTail,
		Stderr:     run.StderrTail,
		StdoutTail: run.StdoutTail,
		StderrTail: run.StderrTail,
	}

	if a.ReadRunLogs != nil {
		stdout, stderr, stdoutPath, stderrPath, err := a.ReadRunLogs(run.JobName, run.ID)
		if err == nil {
			resp.Source = "file"
			resp.Stdout = stdout
			resp.Stderr = stderr
			resp.StdoutPath = stdoutPath
			resp.StderrPath = stderrPath
		} else if !errors.Is(err, os.ErrNotExist) {
			resp.StorageError = err.Error()
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
