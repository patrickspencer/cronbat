package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PluginConfig holds configuration for a single plugin.
type PluginConfig struct {
	Name   string         `yaml:"name"`
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// RunLogConfig controls persistent per-run stdout/stderr log files.
type RunLogConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	Dir               string `yaml:"dir"`
	MaxBytesPerStream int64  `yaml:"max_bytes_per_stream"`
	RetentionDays     int    `yaml:"retention_days"`
	MaxTotalMB        int64  `yaml:"max_total_mb"`
	CleanupInterval   string `yaml:"cleanup_interval"`
}

// IsEnabled returns whether persistent run log files are enabled.
// Defaults to true when unset.
func (c RunLogConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// Config is the top-level daemon configuration parsed from cronbat.yaml.
type Config struct {
	Listen   string         `yaml:"listen"`
	DataDir  string         `yaml:"data_dir"`
	JobsDir  string         `yaml:"jobs_dir"`
	LogLevel string         `yaml:"log_level"`
	Plugins  []PluginConfig `yaml:"plugins"`
	RunLogs  RunLogConfig   `yaml:"run_logs"`
}

func applyDefaults(c *Config) {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.JobsDir == "" {
		c.JobsDir = "./jobs"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.RunLogs.Dir == "" {
		c.RunLogs.Dir = filepath.Join(c.DataDir, "logs")
	}
	if c.RunLogs.MaxBytesPerStream <= 0 {
		c.RunLogs.MaxBytesPerStream = 256 * 1024 // 256KB
	}
	if c.RunLogs.RetentionDays <= 0 {
		c.RunLogs.RetentionDays = 7
	}
	if c.RunLogs.MaxTotalMB <= 0 {
		c.RunLogs.MaxTotalMB = 128
	}
	if c.RunLogs.CleanupInterval == "" {
		c.RunLogs.CleanupInterval = "1h"
	}
	if c.RunLogs.Enabled == nil {
		t := true
		c.RunLogs.Enabled = &t
	}
}

// LoadConfig reads a YAML configuration file from path and returns
// a Config with defaults applied for any unset fields.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	applyDefaults(&cfg)
	return &cfg, nil
}
