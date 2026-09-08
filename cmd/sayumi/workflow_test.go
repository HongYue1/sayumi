package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Workflow-only edits must be exercised by the ordinary local gate too, not
// only by a JavaScript heredoc hidden in a hosted CI step. The script uses real
// temporary Git repositories and mocked remote commands; it never publishes.
func TestWorkflowContracts(t *testing.T) {
	root := filepath.Join("..", "..")
	got := runProvisionCommand(t, root, nil, "bun", ".github/scripts/workflow-tests.mjs")
	if got.code != 0 || !strings.Contains(got.output, "Workflow regressions passed") {
		t.Fatalf("workflow regressions: exit=%d\n%s", got.code, got.output)
	}
	t.Log(strings.TrimSpace(got.output))
}
