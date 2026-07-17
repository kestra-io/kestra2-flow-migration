//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const kestraHost = "https://postgres-ee.preview.dev.kestra.io/"

// TestMigratedFlowsPassValidation builds the migration tool, runs it against
// ./input-flows, then validates the output against the v2 Kestra instance.
// Requires KESTRACTL_TOKEN to be set; skips otherwise.
func TestMigratedFlowsPassValidation(t *testing.T) {
	token := os.Getenv("KESTRACTL_TOKEN")
	if token == "" {
		t.Skip("KESTRACTL_TOKEN not set")
	}

	bin := buildBinary(t)
	outDir := runMigration(t, bin)
	results := validateFlows(t, token, outDir)

	var failCount int
	for _, r := range results {
		if !r.Success {
			failCount++
			name := filepath.Base(r.FilePath)
			for _, c := range r.Constraints {
				t.Errorf("%s: %s", name, c)
			}
		}
	}

	passed := len(results) - failCount
	t.Logf("validated %d flows: %d passed, %d failed", len(results), passed, failCount)
}

// buildBinary compiles the migration tool and returns the path to the binary.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "kestra-migrate")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/kestra-io/kestra2-flow-migration")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// runMigration runs the migration tool on ../input-flows and returns the output directory.
func runMigration(t *testing.T, bin string) string {
	t.Helper()
	outDir := t.TempDir()
	// Working dir for the test is e2e/, so ../input-flows resolves correctly.
	cmd := exec.Command(bin, "--out", outDir, "../input-flows")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("migration failed: %v\n%s", err, out)
	}
	return outDir
}

type validationResult struct {
	FilePath    string   `json:"file_path"`
	FlowID      string   `json:"flow_id"`
	Success     bool     `json:"success"`
	Constraints []string `json:"constraints"`
}

// validateFlows runs kestractl against the given directory and returns parsed results.
func validateFlows(t *testing.T, token, dir string) []validationResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("kestractl",
		"--token", token,
		"--host", kestraHost,
		"flows", "validate", dir,
		"--output", "json",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	// kestractl exits 1 when any flow fails validation; we parse stdout regardless.
	// A truly fatal error (auth failure, unreachable host) produces empty stdout.
	if err != nil && stdout.Len() == 0 {
		t.Fatalf("kestractl error: %v\nstderr: %s", err, stderr.String())
	}

	var results []validationResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("failed to parse kestractl output: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	return results
}

