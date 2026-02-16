package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/realtime"
	"github.com/patrickspencer/cronbat/internal/store"
)

type jobSummary struct {
	Name          string         `json:"name"`
	Schedule      string         `json:"schedule"`
	Command       string         `json:"command"`
	WorkingDir    string         `json:"working_dir,omitempty"`
	Executor      string         `json:"executor"`
	Enabled       bool           `json:"enabled"`
	State         string         `json:"state,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	NextRun       *time.Time     `json:"next_run,omitempty"`
	LastRun       *time.Time     `json:"last_run,omitempty"`
	LastRunStatus string         `json:"last_run_status,omitempty"`
}

type jobDetail struct {
	jobSummary
	Timeout   string            `json:"timeout,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	OnSuccess []string          `json:"on_success,omitempty"`
	OnFailure []string          `json:"on_failure,omitempty"`
	Stats     *jobStatsResp     `json:"stats,omitempty"`
}

type jobStatsResp struct {
	TotalRuns     int        `json:"total_runs"`
	Successes     int        `json:"successes"`
	Failures      int        `json:"failures"`
	LastRun       *time.Time `json:"last_run,omitempty"`
	AvgDurationMs float64    `json:"avg_duration_ms"`
}

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// continue
	case http.MethodPost:
		a.handleCreateJob(w, r)
		return
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	jobs := a.Jobs()
	result := make([]jobSummary, 0, len(jobs))

	for _, j := range jobs {
		state := ""
		if a.JobState != nil {
			state = strings.TrimSpace(a.JobState(j.Name))
		}
		if state == "" {
			if j.IsEnabled() {
				state = "started"
			} else {
				state = "stopped"
			}
		}

		s := jobSummary{
			Name:       j.Name,
			Schedule:   j.Schedule,
			Command:    j.Command,
			WorkingDir: j.WorkingDir,
			Executor:   j.Executor,
			Enabled:    j.IsEnabled(),
			State:      state,
			Metadata:   j.Metadata,
		}
		if next, ok := a.NextRunTime(j.Name); ok {
			s.NextRun = &next
		}
		if a.Store != nil {
			runs, err := a.Store.ListRuns(r.Context(), store.ListOpts{
				JobName: j.Name,
				Limit:   1,
			})
			if err != nil {
				log.Printf("ERROR: failed to get latest run for %s: %v", j.Name, err)
			} else if len(runs) > 0 {
				s.LastRun = &runs[0].StartedAt
				s.LastRunStatus = runs[0].Status
			}
		}
		result = append(result, s)
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	if a.CreateJob == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create operation not available"})
		return
	}

	var newJob config.Job
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&newJob); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if err := a.CreateJob(newJob); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}

	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: strings.TrimSpace(newJob.Name),
		Action:  "create",
	})
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "name": strings.TrimSpace(newJob.Name)})
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request, name string) {
	var found *jobDetail
	for _, j := range a.Jobs() {
		if j.Name == name {
			state := ""
			if a.JobState != nil {
				state = strings.TrimSpace(a.JobState(j.Name))
			}
			if state == "" {
				if j.IsEnabled() {
					state = "started"
				} else {
					state = "stopped"
				}
			}

			d := &jobDetail{
				jobSummary: jobSummary{
					Name:       j.Name,
					Schedule:   j.Schedule,
					Command:    j.Command,
					WorkingDir: j.WorkingDir,
					Executor:   j.Executor,
					Enabled:    j.IsEnabled(),
					State:      state,
					Metadata:   j.Metadata,
				},
				Timeout:   j.Timeout,
				Env:       j.Env,
				OnSuccess: j.OnSuccess,
				OnFailure: j.OnFailure,
			}
			if next, ok := a.NextRunTime(j.Name); ok {
				d.NextRun = &next
			}
			if a.Store != nil {
				runs, err := a.Store.ListRuns(r.Context(), store.ListOpts{
					JobName: j.Name,
					Limit:   1,
				})
				if err != nil {
					log.Printf("ERROR: failed to get latest run for %s: %v", j.Name, err)
				} else if len(runs) > 0 {
					d.LastRun = &runs[0].StartedAt
					d.LastRunStatus = runs[0].Status
				}
			}
			found = d
			break
		}
	}

	if found == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	stats, err := a.Store.GetJobStats(r.Context(), name)
	if err != nil {
		log.Printf("ERROR: failed to get job stats for %s: %v", name, err)
	} else {
		found.Stats = &jobStatsResp{
			TotalRuns:     stats.TotalRuns,
			Successes:     stats.Successes,
			Failures:      stats.Failures,
			LastRun:       stats.LastRun,
			AvgDurationMs: stats.AvgDurationMs,
		}
	}

	writeJSON(w, http.StatusOK, found)
}

func (a *API) handleTriggerRun(w http.ResponseWriter, r *http.Request, name string) {
	var exists bool
	for _, j := range a.Jobs() {
		if j.Name == name {
			exists = true
			break
		}
	}
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	go a.TriggerRun(name)
	log.Printf("manual run triggered for job %s", name)
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "run",
	})

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "triggered"})
}

func (a *API) handleEnableJob(w http.ResponseWriter, r *http.Request, name string) {
	if err := a.EnableJob(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "enable",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "enabled"})
}

func (a *API) handleDisableJob(w http.ResponseWriter, r *http.Request, name string) {
	if err := a.DisableJob(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "disable",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

func statusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case strings.Contains(msg, "already exists"):
		return http.StatusConflict
	case strings.Contains(msg, "required"):
		return http.StatusBadRequest
	case strings.Contains(msg, "invalid"), strings.Contains(msg, "parse"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) handleStartJob(w http.ResponseWriter, _ *http.Request, name string) {
	fn := a.StartJob
	if fn == nil {
		fn = a.EnableJob
	}
	if fn == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "start operation not available"})
		return
	}
	if err := fn(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "start",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (a *API) handleStopJob(w http.ResponseWriter, _ *http.Request, name string) {
	fn := a.StopJob
	if fn == nil {
		fn = a.DisableJob
	}
	if fn == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stop operation not available"})
		return
	}
	if err := fn(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "stop",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (a *API) handlePauseJob(w http.ResponseWriter, _ *http.Request, name string) {
	fn := a.PauseJob
	if fn == nil {
		fn = a.DisableJob
	}
	if fn == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "pause operation not available"})
		return
	}
	if err := fn(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "pause",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (a *API) handleDeleteJob(w http.ResponseWriter, _ *http.Request, name string) {
	if a.DeleteJob == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete operation not available"})
		return
	}
	if err := a.DeleteJob(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "delete",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleArchiveJob(w http.ResponseWriter, _ *http.Request, name string) {
	if a.ArchiveJob == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "archive operation not available"})
		return
	}
	if err := a.ArchiveJob(name); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "archive",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (a *API) handleGetJobYAML(w http.ResponseWriter, _ *http.Request, name string) {
	if a.GetJobYAML == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "yaml operation not available"})
		return
	}
	data, err := a.GetJobYAML(name)
	if err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"name": name,
		"yaml": data,
	})
}

type yamlPayload struct {
	YAML string `json:"yaml"`
}

func (a *API) handleUpdateJobYAML(w http.ResponseWriter, r *http.Request, name string) {
	if a.UpdateJobYAML == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "yaml operation not available"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
		return
	}

	payload := string(body)
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var req yamlPayload
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		payload = req.YAML
	}

	if strings.TrimSpace(payload) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "yaml payload is empty"})
		return
	}

	updatedName, err := a.UpdateJobYAML(name, payload)
	if err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	if updatedName == "" {
		updatedName = name
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: updatedName,
		Action:  "update_yaml",
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "updated",
		"name":   updatedName,
	})
}

func (a *API) handleUpdateJobSettings(w http.ResponseWriter, r *http.Request, name string) {
	if a.UpdateJobSettings == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "settings operation not available"})
		return
	}

	var updated config.Job
	if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&updated); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if updated.Name == "" {
		updated.Name = name
	}
	if updated.Name != name {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "changing job name is not supported in settings editor"})
		return
	}

	if err := a.UpdateJobSettings(name, updated); err != nil {
		writeJSON(w, statusFromError(err), map[string]string{"error": err.Error()})
		return
	}
	a.emitEvent(realtime.Event{
		Type:    "job.changed",
		JobName: name,
		Action:  "update_settings",
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
