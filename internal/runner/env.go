package runner

import (
	"os"

	"github.com/patrickspencer/cronbat/pkg/plugin"
)

// BuildEnv constructs the environment variable slice for a job execution.
// It starts with the current process environment, overlays job-specific
// variables, and adds PICOCRON_* variables.
func BuildEnv(base map[string]string, job plugin.JobContext) []string {
	// Start with current environment in a map for easy overlay.
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				envMap[e[:i]] = e[i+1:]
				break
			}
		}
	}

	// Overlay base env.
	for k, v := range base {
		envMap[k] = v
	}

	// Overlay job-specific env.
	for k, v := range job.Env {
		envMap[k] = v
	}

	// Add picocron metadata.
	envMap["PICOCRON_JOB_NAME"] = job.JobName
	envMap["PICOCRON_TRIGGER"] = job.Trigger

	// Convert to slice.
	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, k+"="+v)
	}
	return result
}
