package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "cronbat.yaml")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Listen != ":8080" {
		t.Fatalf("expected default listen :8080, got %q", cfg.Listen)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("expected default data_dir ./data, got %q", cfg.DataDir)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("UserHomeDir unavailable for test: %v", err)
	}
	expectedJobsDir := filepath.Join(home, ".config", "cronbat", "jobs")
	if cfg.JobsDir != expectedJobsDir {
		t.Fatalf("expected default jobs_dir %q, got %q", expectedJobsDir, cfg.JobsDir)
	}
}

func TestLoadConfigExpandsTildePaths(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "cronbat.yaml")
	body := `
data_dir: "~/cronbat-data"
jobs_dir: "~/.config/cronbat/jobs"
run_logs:
  dir: "~/cronbat-logs"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Fatalf("UserHomeDir unavailable for test: %v", err)
	}

	if got, want := cfg.DataDir, filepath.Join(home, "cronbat-data"); got != want {
		t.Fatalf("expected expanded data_dir %q, got %q", want, got)
	}
	if got, want := cfg.JobsDir, filepath.Join(home, ".config", "cronbat", "jobs"); got != want {
		t.Fatalf("expected expanded jobs_dir %q, got %q", want, got)
	}
	if got, want := cfg.RunLogs.Dir, filepath.Join(home, "cronbat-logs"); got != want {
		t.Fatalf("expected expanded run_logs.dir %q, got %q", want, got)
	}
}
