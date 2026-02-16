package api

import (
	"log"
	"net/http"

	"github.com/patrickspencer/cronbat/internal/store"
)

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if a.GetConfig == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config provider unavailable"})
		return
	}

	cfg := a.GetConfig()
	if cfg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, cfg)
}

type statsResponse struct {
	TotalJobs      int `json:"total_jobs"`
	EnabledJobs    int `json:"enabled_jobs"`
	TotalRuns      int `json:"total_runs"`
	RecentFailures int `json:"recent_failures"`
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	jobs := a.Jobs()

	totalJobs := len(jobs)
	var enabledJobs int
	for _, j := range jobs {
		if j.IsEnabled() {
			enabledJobs++
		}
	}

	var totalRuns, recentFailures int
	for _, j := range jobs {
		stats, err := a.Store.GetJobStats(r.Context(), j.Name)
		if err != nil {
			log.Printf("ERROR: failed to get job stats for %s: %v", j.Name, err)
			continue
		}
		totalRuns += stats.TotalRuns
		recentFailures += stats.Failures
	}

	// Cross-check with a broad query for total run count.
	runs, err := a.Store.ListRuns(r.Context(), store.ListOpts{Limit: 0})
	if err == nil && len(runs) > totalRuns {
		totalRuns = len(runs)
	}

	writeJSON(w, http.StatusOK, statsResponse{
		TotalJobs:      totalJobs,
		EnabledJobs:    enabledJobs,
		TotalRuns:      totalRuns,
		RecentFailures: recentFailures,
	})
}
