package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickspencer/cronbat/internal/config"
)

const (
	cronbatBeginMarker = "# --- cronbat managed begin ---"
	cronbatEndMarker   = "# --- cronbat managed end ---"
	cronbatTag         = "#cronbat"
)

func runCronSync(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: cronbat cron-sync <install|import> [flags]")
		return 1
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "install":
		return runCronSyncInstall(rest)
	case "import":
		return runCronSyncImport(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown cron-sync subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: cronbat cron-sync <install|import> [flags]")
		return 1
	}
}

func runCronSyncInstall(args []string) int {
	fs := flag.NewFlagSet("cron-sync install", flag.ExitOnError)
	apiURL := fs.String("api", "", "API URL (if set, reads jobs from API)")
	configPath := fs.String("config", "cronbat.yaml", "path to config file (for direct file access)")
	dryRun := fs.Bool("dry-run", false, "preview only, don't modify crontab")
	fs.Parse(args)

	jobs, err := loadJobsForSync(*apiURL, *configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading jobs: %v\n", err)
		return 1
	}

	if len(jobs) == 0 {
		fmt.Println("no jobs to install")
		return 0
	}

	// Resolve cronbat binary path.
	cronbatBin, err := os.Executable()
	if err != nil {
		cronbatBin = "cronbat"
	}

	// Resolve absolute config path for crontab entries.
	absConfig, err := filepath.Abs(*configPath)
	if err != nil {
		absConfig = *configPath
	}

	// Build managed crontab entries.
	var managed strings.Builder
	managed.WriteString(cronbatBeginMarker + "\n")
	for _, j := range jobs {
		if !j.IsEnabled() {
			continue
		}
		line := fmt.Sprintf("%s %s wrap --name %s --config %s -- %s  %s",
			j.Schedule, cronbatBin, j.Name, absConfig, j.Command, cronbatTag)
		managed.WriteString(line + "\n")
	}
	managed.WriteString(cronbatEndMarker + "\n")

	if *dryRun {
		fmt.Println("--- dry run: would install the following crontab section ---")
		fmt.Print(managed.String())
		return 0
	}

	// Read existing crontab.
	existing, err := readCrontab()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading crontab: %v\n", err)
		return 1
	}

	// Merge: replace managed section, preserve everything else.
	merged := mergeCrontab(existing, managed.String())

	if err := writeCrontab(merged); err != nil {
		fmt.Fprintf(os.Stderr, "error writing crontab: %v\n", err)
		return 1
	}

	fmt.Printf("installed %d job(s) into crontab\n", len(jobs))
	return 0
}

func runCronSyncImport(args []string) int {
	fs := flag.NewFlagSet("cron-sync import", flag.ExitOnError)
	apiURL := fs.String("api", "", "API URL (if set, creates jobs via API)")
	configPath := fs.String("config", "cronbat.yaml", "path to config file (for direct file access)")
	dryRun := fs.Bool("dry-run", false, "preview only, don't create jobs")
	prefix := fs.String("prefix", "cron-", "name prefix for imported jobs")
	fs.Parse(args)

	crontab, err := readCrontab()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading crontab: %v\n", err)
		return 1
	}

	entries := parseCronbatEntries(crontab, *prefix)
	if len(entries) == 0 {
		fmt.Println("no #cronbat tagged entries found in crontab")
		return 0
	}

	if *dryRun {
		fmt.Println("--- dry run: would import the following jobs ---")
		for _, e := range entries {
			fmt.Printf("  name: %s, schedule: %s, command: %s\n", e.Name, e.Schedule, e.Command)
		}
		return 0
	}

	for _, e := range entries {
		if *apiURL != "" {
			if err := createJobViaAPI(*apiURL, e); err != nil {
				fmt.Fprintf(os.Stderr, "error creating job %q via API: %v\n", e.Name, err)
				continue
			}
		} else {
			if err := createJobViaFile(*configPath, e); err != nil {
				fmt.Fprintf(os.Stderr, "error creating job %q: %v\n", e.Name, err)
				continue
			}
		}
		fmt.Printf("imported job %q\n", e.Name)
	}

	return 0
}

type importedJob struct {
	Name     string
	Schedule string
	Command  string
}

func loadJobsForSync(apiURL, configPath string) ([]*config.Job, error) {
	if apiURL != "" {
		return loadJobsFromAPI(apiURL)
	}
	return loadJobsFromConfig(configPath)
}

func loadJobsFromAPI(apiURL string) ([]*config.Job, error) {
	apiURL = strings.TrimRight(apiURL, "/")
	resp, err := http.Get(apiURL + "/api/v1/jobs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result struct {
		Jobs []config.Job `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var jobs []*config.Job
	for i := range result.Jobs {
		jobs = append(jobs, &result.Jobs[i])
	}
	return jobs, nil
}

func loadJobsFromConfig(configPath string) ([]*config.Job, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return config.LoadJobs(cfg.JobsDir)
}

func readCrontab() (string, error) {
	cmd := exec.Command("crontab", "-l")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err != nil {
		// crontab -l returns error if no crontab exists; treat as empty.
		return "", nil
	}
	return out.String(), nil
}

func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mergeCrontab(existing, managed string) string {
	lines := strings.Split(existing, "\n")

	var before, after []string
	inManaged := false
	foundManaged := false

	for _, line := range lines {
		if strings.TrimSpace(line) == cronbatBeginMarker {
			inManaged = true
			foundManaged = true
			continue
		}
		if strings.TrimSpace(line) == cronbatEndMarker {
			inManaged = false
			continue
		}
		if inManaged {
			continue
		}
		if !foundManaged {
			before = append(before, line)
		} else {
			after = append(after, line)
		}
	}

	var result strings.Builder
	beforeStr := strings.Join(before, "\n")
	// Remove trailing empty lines before managed block.
	beforeStr = strings.TrimRight(beforeStr, "\n")
	if beforeStr != "" {
		result.WriteString(beforeStr + "\n")
	}
	result.WriteString(managed)
	afterStr := strings.Join(after, "\n")
	afterStr = strings.TrimLeft(afterStr, "\n")
	if afterStr != "" {
		result.WriteString(afterStr)
	}

	// Ensure trailing newline.
	s := result.String()
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

func parseCronbatEntries(crontab, prefix string) []importedJob {
	var entries []importedJob
	seen := make(map[string]bool)

	for _, line := range strings.Split(crontab, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, cronbatTag) {
			continue
		}

		// Remove the #cronbat tag.
		line = strings.Replace(line, cronbatTag, "", 1)
		line = strings.TrimSpace(line)

		// Parse cron schedule (first 5 fields) and command.
		parts := strings.Fields(line)
		if len(parts) < 6 {
			continue
		}

		schedule := strings.Join(parts[:5], " ")
		command := strings.Join(parts[5:], " ")

		// If the command is a cronbat wrap invocation, extract the original command.
		if wrapCmd, jobName, ok := parseWrapCommand(command); ok {
			name := jobName
			if !seen[name] {
				seen[name] = true
				entries = append(entries, importedJob{
					Name:     name,
					Schedule: schedule,
					Command:  wrapCmd,
				})
			}
			continue
		}

		// Generate a name from prefix + sanitized command.
		name := generateJobName(prefix, command)
		if seen[name] {
			// Append a number to make unique.
			for i := 2; seen[name]; i++ {
				name = fmt.Sprintf("%s%d", name, i)
			}
		}
		seen[name] = true

		entries = append(entries, importedJob{
			Name:     name,
			Schedule: schedule,
			Command:  command,
		})
	}

	return entries
}

func parseWrapCommand(command string) (wrappedCmd, jobName string, ok bool) {
	// Look for "cronbat wrap" pattern and extract --name and the command after --.
	if !strings.Contains(command, "wrap") {
		return "", "", false
	}

	parts := strings.Fields(command)
	var name string
	var dashDashIdx int = -1

	for i := 0; i < len(parts); i++ {
		if parts[i] == "--name" && i+1 < len(parts) {
			name = parts[i+1]
			i++
		} else if parts[i] == "--" {
			dashDashIdx = i
			break
		}
	}

	if name == "" || dashDashIdx < 0 || dashDashIdx+1 >= len(parts) {
		return "", "", false
	}

	return strings.Join(parts[dashDashIdx+1:], " "), name, true
}

func generateJobName(prefix, command string) string {
	// Take the base command name.
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return prefix + "job"
	}
	base := filepath.Base(parts[0])
	// Remove extension.
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	// Sanitize.
	var b strings.Builder
	for _, ch := range base {
		isLower := ch >= 'a' && ch <= 'z'
		isUpper := ch >= 'A' && ch <= 'Z'
		isDigit := ch >= '0' && ch <= '9'
		if isLower || isUpper || isDigit || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	name := b.String()
	if name == "" {
		name = "job"
	}
	return prefix + name
}

func createJobViaAPI(apiURL string, entry importedJob) error {
	apiURL = strings.TrimRight(apiURL, "/")
	payload := map[string]any{
		"name":     entry.Name,
		"schedule": entry.Schedule,
		"command":  entry.Command,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(apiURL+"/api/v1/jobs", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func createJobViaFile(configPath string, entry importedJob) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.JobsDir, 0755); err != nil {
		return err
	}

	job := &config.Job{
		Name:     entry.Name,
		Schedule: entry.Schedule,
		Command:  entry.Command,
	}

	path := filepath.Join(cfg.JobsDir, entry.Name+".yaml")
	return config.SaveJob(path, job)
}
