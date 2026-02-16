package api

import (
	"strings"
	"testing"
)

func TestParseImportedJobsYAML(t *testing.T) {
	t.Parallel()

	payload := `
# export header
---
name: alpha
schedule: "*/5 * * * *"
command: "echo alpha"

---
# empty document should be ignored

---
name: beta
schedule: "0 2 * * *"
command: "echo beta"
enabled: false
`

	jobs, err := parseImportedJobsYAML([]byte(payload))
	if err != nil {
		t.Fatalf("parseImportedJobsYAML: %v", err)
	}
	if got, want := len(jobs), 2; got != want {
		t.Fatalf("expected %d jobs, got %d", want, got)
	}
	if jobs[0].Name != "alpha" || jobs[1].Name != "beta" {
		t.Fatalf("unexpected job names: %q, %q", jobs[0].Name, jobs[1].Name)
	}
}

func TestParseImportedJobsYAMLDuplicateName(t *testing.T) {
	t.Parallel()

	payload := `
name: alpha
schedule: "*/5 * * * *"
command: "echo one"
---
name: alpha
schedule: "0 * * * *"
command: "echo two"
`

	_, err := parseImportedJobsYAML([]byte(payload))
	if err == nil {
		t.Fatal("expected duplicate-name error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate job name") {
		t.Fatalf("expected duplicate-name error, got: %v", err)
	}
}
