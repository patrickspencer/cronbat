package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/patrickspencer/cronbat/internal/config"
	"github.com/patrickspencer/cronbat/internal/realtime"
	"github.com/patrickspencer/cronbat/internal/scheduler"
	"gopkg.in/yaml.v3"
)

const maxJobsImportBytes = 8 * 1024 * 1024 // 8 MiB

type jobsImportResult struct {
	Status  string   `json:"status"`
	Replace bool     `json:"replace"`
	DryRun  bool     `json:"dry_run"`
	Parsed  int      `json:"parsed"`
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Deleted []string `json:"deleted,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func (a *API) handleExportJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	jobs := a.Jobs()
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].Name < jobs[j].Name
	})

	var out strings.Builder
	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(&out, "# cronbat jobs export\n# generated_at: %s\n# count: %d\n", now, len(jobs))
	for i, job := range jobs {
		if i > 0 {
			out.WriteString("\n---\n")
		}
		data, err := config.MarshalJobYAML(job)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to marshal job %q", job.Name),
			})
			return
		}
		out.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			out.WriteByte('\n')
		}
	}

	filenameTime := time.Now().UTC().Format("20060102T150405Z")
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"cronbat-jobs-%s.yaml\"", filenameTime))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out.String()))
}

func (a *API) handleImportJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.CreateJob == nil || a.UpdateJobSettings == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "import operation not available"})
		return
	}

	replace, err := parseBoolQuery(r, "replace")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	dryRun, err := parseBoolQuery(r, "dry_run")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if replace && a.DeleteJob == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "replace import requires delete operation"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxJobsImportBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read import payload"})
		return
	}
	if int64(len(body)) > maxJobsImportBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "import payload too large"})
		return
	}
	if strings.TrimSpace(string(body)) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "import payload is empty"})
		return
	}

	imported, err := parseImportedJobsYAML(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	existing := make(map[string]struct{})
	for _, j := range a.Jobs() {
		existing[j.Name] = struct{}{}
	}

	importedNames := make(map[string]struct{}, len(imported))
	toCreate := make([]config.Job, 0)
	toUpdate := make([]config.Job, 0)
	for _, job := range imported {
		importedNames[job.Name] = struct{}{}
		if _, ok := existing[job.Name]; ok {
			toUpdate = append(toUpdate, job)
		} else {
			toCreate = append(toCreate, job)
		}
	}

	toDelete := make([]string, 0)
	if replace {
		for name := range existing {
			if _, keep := importedNames[name]; !keep {
				toDelete = append(toDelete, name)
			}
		}
		sort.Strings(toDelete)
	}

	result := jobsImportResult{
		Status:  "imported",
		Replace: replace,
		DryRun:  dryRun,
		Parsed:  len(imported),
		Created: make([]string, 0, len(toCreate)),
		Updated: make([]string, 0, len(toUpdate)),
		Deleted: make([]string, 0, len(toDelete)),
	}
	for _, j := range toCreate {
		result.Created = append(result.Created, j.Name)
	}
	for _, j := range toUpdate {
		result.Updated = append(result.Updated, j.Name)
	}
	result.Deleted = append(result.Deleted, toDelete...)

	if dryRun {
		result.Status = "dry_run"
		writeJSON(w, http.StatusOK, result)
		return
	}

	result.Created = result.Created[:0]
	result.Updated = result.Updated[:0]
	result.Deleted = result.Deleted[:0]

	for _, job := range toCreate {
		if err := a.CreateJob(job); err != nil {
			result.Status = "partial_failure"
			result.Error = err.Error()
			writeJSON(w, statusFromError(err), result)
			return
		}
		result.Created = append(result.Created, job.Name)
		a.emitEvent(realtime.Event{
			Type:    "job.changed",
			JobName: job.Name,
			Action:  "create",
		})
	}

	for _, job := range toUpdate {
		if err := a.UpdateJobSettings(job.Name, job); err != nil {
			result.Status = "partial_failure"
			result.Error = err.Error()
			writeJSON(w, statusFromError(err), result)
			return
		}
		result.Updated = append(result.Updated, job.Name)
		a.emitEvent(realtime.Event{
			Type:    "job.changed",
			JobName: job.Name,
			Action:  "update",
		})
	}

	if replace {
		for _, name := range toDelete {
			if err := a.DeleteJob(name); err != nil {
				result.Status = "partial_failure"
				result.Error = err.Error()
				writeJSON(w, statusFromError(err), result)
				return
			}
			result.Deleted = append(result.Deleted, name)
			a.emitEvent(realtime.Event{
				Type:    "job.changed",
				JobName: name,
				Action:  "delete",
			})
		}
	}

	writeJSON(w, http.StatusOK, result)
}

func parseBoolQuery(r *http.Request, key string) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean query value for %q", key)
	}
}

func parseImportedJobsYAML(data []byte) ([]config.Job, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	imported := make([]config.Job, 0)
	seen := make(map[string]struct{})
	docNum := 0

	for {
		var job config.Job
		err := decoder.Decode(&job)
		if errors.Is(err, io.EOF) {
			break
		}
		docNum++
		if err != nil {
			return nil, fmt.Errorf("invalid YAML in document %d: %w", docNum, err)
		}

		normalizeImportedJob(&job)
		if isEmptyImportDoc(&job) {
			continue
		}
		applyImportedDefaults(&job)
		if err := validateImportedJob(&job); err != nil {
			return nil, fmt.Errorf("invalid document %d: %w", docNum, err)
		}

		if _, exists := seen[job.Name]; exists {
			return nil, fmt.Errorf("duplicate job name in import payload: %s", job.Name)
		}
		seen[job.Name] = struct{}{}
		imported = append(imported, job)
	}

	if len(imported) == 0 {
		return nil, errors.New("no jobs found in import payload")
	}
	return imported, nil
}

func normalizeImportedJob(job *config.Job) {
	job.Name = strings.TrimSpace(job.Name)
	job.Schedule = strings.TrimSpace(job.Schedule)
	job.Command = strings.TrimSpace(job.Command)
	job.WorkingDir = strings.TrimSpace(job.WorkingDir)
	job.Executor = strings.TrimSpace(job.Executor)
	job.Timeout = strings.TrimSpace(job.Timeout)
}

func applyImportedDefaults(job *config.Job) {
	if job.Executor == "" {
		job.Executor = "shell"
	}
	if job.Enabled == nil {
		t := true
		job.Enabled = &t
	}
}

func isEmptyImportDoc(job *config.Job) bool {
	return job.Name == "" &&
		job.Schedule == "" &&
		job.Command == "" &&
		job.WorkingDir == "" &&
		job.Executor == "" &&
		job.Timeout == "" &&
		job.Enabled == nil &&
		len(job.Env) == 0 &&
		len(job.OnSuccess) == 0 &&
		len(job.OnFailure) == 0 &&
		job.Analyze == nil &&
		len(job.Metadata) == 0
}

func validateImportedJob(job *config.Job) error {
	if job.Name == "" {
		return errors.New("job name is required")
	}
	if !isSafeJobName(job.Name) {
		return errors.New("invalid job name: use only letters, numbers, '.', '-', '_'")
	}
	if job.Schedule == "" {
		return errors.New("job schedule is required")
	}
	if _, err := scheduler.ParseSchedule(job.Schedule); err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}
	if job.Command == "" {
		return errors.New("job command is required")
	}
	if _, err := job.ParseTimeout(); err != nil {
		return fmt.Errorf("invalid timeout: %w", err)
	}
	return nil
}

func isSafeJobName(name string) bool {
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
