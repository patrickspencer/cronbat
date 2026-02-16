package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runWatchdog(args []string) int {
	fs := flag.NewFlagSet("watchdog", flag.ExitOnError)
	apiURL := fs.String("api", "http://localhost:8080", "cronbat API URL")
	restartCmd := fs.String("restart-cmd", "", "command to run if unhealthy")
	timeoutSec := fs.Int("timeout", 5, "health check timeout in seconds")
	fs.Parse(args)

	url := strings.TrimRight(*apiURL, "/") + "/api/v1/health"

	client := &http.Client{
		Timeout: time.Duration(*timeoutSec) * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
		return handleUnhealthy(*restartCmd)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "health check returned status %d\n", resp.StatusCode)
		return handleUnhealthy(*restartCmd)
	}

	return 0
}

func handleUnhealthy(restartCmd string) int {
	if restartCmd == "" {
		return 1
	}

	fmt.Fprintf(os.Stderr, "attempting restart: %s\n", restartCmd)
	cmd := exec.Command("sh", "-c", restartCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "restart command failed: %v\n", err)
		return 1
	}
	return 0
}
