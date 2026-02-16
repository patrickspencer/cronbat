package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// AnalyzeConfig holds LLM post-processing configuration for a job.
type AnalyzeConfig struct {
	Provider string   `yaml:"provider" json:"provider"`
	Prompt   string   `yaml:"prompt" json:"prompt"`
	Model    string   `yaml:"model" json:"model"`
	OnResult []string `yaml:"on_result" json:"on_result"`
}

// Job is the definition of a single cron job parsed from a YAML file.
type Job struct {
	Name       string            `yaml:"name" json:"name"`
	Schedule   string            `yaml:"schedule" json:"schedule"`
	Command    string            `yaml:"command" json:"command"`
	WorkingDir string            `yaml:"working_dir" json:"working_dir,omitempty"`
	Executor   string            `yaml:"executor" json:"executor,omitempty"`
	Timeout    string            `yaml:"timeout" json:"timeout,omitempty"`
	Env        map[string]string `yaml:"env" json:"env,omitempty"`
	Enabled    *bool             `yaml:"enabled" json:"enabled,omitempty"`
	OnSuccess  []string          `yaml:"on_success" json:"on_success,omitempty"`
	OnFailure  []string          `yaml:"on_failure" json:"on_failure,omitempty"`
	Analyze    *AnalyzeConfig    `yaml:"analyze" json:"analyze,omitempty"`
	Metadata   map[string]any    `yaml:"metadata" json:"metadata,omitempty"`
	FilePath   string            `yaml:"-" json:"-"`
}

// IsEnabled returns whether the job is enabled. Defaults to true if not set.
func (j *Job) IsEnabled() bool {
	if j.Enabled == nil {
		return true
	}
	return *j.Enabled
}

// ParseTimeout parses the Timeout string into a time.Duration.
// Returns 0 if the timeout is empty.
func (j *Job) ParseTimeout() (time.Duration, error) {
	if j.Timeout == "" {
		return 0, nil
	}
	return time.ParseDuration(j.Timeout)
}

func applyJobDefaults(j *Job) {
	if j.Executor == "" {
		j.Executor = "shell"
	}
}

// ParseJobYAML parses a single job YAML payload and applies defaults.
func ParseJobYAML(data []byte) (*Job, error) {
	var job Job
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	applyJobDefaults(&job)
	return &job, nil
}

// MarshalJobYAML serializes a job to YAML.
func MarshalJobYAML(job *Job) ([]byte, error) {
	return yaml.Marshal(job)
}

// SaveJob writes a single job definition file.
func SaveJob(path string, job *Job) error {
	data, err := MarshalJobYAML(job)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	job.FilePath = path
	return nil
}

// LoadJobs reads all *.yaml files from dir, parses each into a Job,
// and returns the collected jobs.
func LoadJobs(dir string) ([]*Job, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var jobs []*Job
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}

		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		job, err := ParseJobYAML(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		job.FilePath = path
		jobs = append(jobs, job)
	}

	return jobs, nil
}
