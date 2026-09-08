package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleaseContracts(t *testing.T) {
	// This suite compiles the real host archiver once, then exercises the
	// release script and literal publisher verifier in disposable fixtures.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bun", ".github/scripts/release-tests.mjs")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(cmd.Environ(), "BASH_ENV=", "NO_COLOR=1")
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if err != nil || ctx.Err() != nil || !strings.Contains(string(out), "Release regressions passed") {
		t.Fatalf("release regressions: %v (context: %v)\n%s", err, ctx.Err(), out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
