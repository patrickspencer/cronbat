package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/realtime"
	"github.com/patrickspencer/cronbat/internal/store"
)

// API holds dependencies for all API handlers.
type API struct {
	Store             store.RunStore
	Events            *realtime.Broker
	GetConfig         func() *config.Config
	Jobs              func() []*config.Job
	JobState          func(name string) string
	CreateJob         func(newJob config.Job) error
	ReadRunLogs       func(jobName string, runID string) (stdout string, stderr string, stdoutPath string, stderrPath string, err error)
	TriggerRun        func(jobName string)
	NextRunTime       func(name string) (time.Time, bool)
	EnableJob         func(name string) error
	DisableJob        func(name string) error
	StartJob          func(name string) error
	StopJob           func(name string) error
	PauseJob          func(name string) error
	ArchiveJob        func(name string) error
	DeleteJob         func(name string) error
	GetJobYAML        func(name string) (string, error)
	UpdateJobYAML     func(name string, data string) (string, error)
	UpdateJobSettings func(name string, updated config.Job) error
}

// RegisterRoutes registers all API routes on the given ServeMux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/jobs/export", a.handleExportJobs)
	mux.HandleFunc("/api/v1/jobs/import", a.handleImportJobs)
	mux.HandleFunc("/api/v1/jobs/", a.routeJobs)
	mux.HandleFunc("/api/v1/jobs", a.handleListJobs)
	mux.HandleFunc("/api/v1/runs/", a.routeRuns)
	mux.HandleFunc("/api/v1/runs", a.handleListRuns)
	mux.HandleFunc("/api/v1/events", a.handleEvents)
	mux.HandleFunc("/api/v1/config", a.handleConfig)
	mux.HandleFunc("/api/v1/health", a.handleHealth)
	mux.HandleFunc("/api/v1/stats", a.handleStats)
}

// routeJobs dispatches /api/v1/jobs/{name}[/action] requests.
func (a *API) routeJobs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	if name == "" {
		a.handleListJobs(w, r)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "run" && r.Method == http.MethodPost:
		a.handleTriggerRun(w, r, name)
	case action == "start" && r.Method == http.MethodPut:
		a.handleStartJob(w, r, name)
	case action == "stop" && r.Method == http.MethodPut:
		a.handleStopJob(w, r, name)
	case action == "pause" && r.Method == http.MethodPut:
		a.handlePauseJob(w, r, name)
	case action == "archive" && r.Method == http.MethodPut:
		a.handleArchiveJob(w, r, name)
	case action == "enable" && r.Method == http.MethodPut:
		a.handleEnableJob(w, r, name)
	case action == "disable" && r.Method == http.MethodPut:
		a.handleDisableJob(w, r, name)
	case action == "yaml" && r.Method == http.MethodGet:
		a.handleGetJobYAML(w, r, name)
	case action == "yaml" && r.Method == http.MethodPut:
		a.handleUpdateJobYAML(w, r, name)
	case action == "" && r.Method == http.MethodPut:
		a.handleUpdateJobSettings(w, r, name)
	case action == "" && r.Method == http.MethodDelete:
		a.handleDeleteJob(w, r, name)
	case action == "" && r.Method == http.MethodGet:
		a.handleGetJob(w, r, name)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// routeRuns dispatches /api/v1/runs/{id} requests.
func (a *API) routeRuns(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runs/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		a.handleListRuns(w, r)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	switch action {
	case "":
		a.handleGetRun(w, r, id)
	case "logs":
		a.handleGetRunLogs(w, r, id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("ERROR: failed to write JSON response: %v", err)
	}
}
