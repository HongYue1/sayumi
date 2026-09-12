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
	// Windows fixture teardown waits out asynchronously released handles, so
	// leave headroom for a loaded runner: a context deadline here would report a
	// timeout instead of whichever release contract actually failed.
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
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
