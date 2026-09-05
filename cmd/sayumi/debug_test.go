package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDebugServerLifecycle(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	stop := startDebugServer(true, 0)
	defer stop()
	url := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/debug/pprof/`).FindString(logs.String())
	if url == "" || strings.Contains(url, ":0/") {
		t.Fatalf("no real loopback URL announced: %q", logs.String())
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("pprof status = %d, body error = %v", response.StatusCode, readErr)
	}
	stop()
	response, err = client.Get(url)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("debug server still accepts requests after cleanup")
	}
}

func TestDebugServerBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("listener is not TCP")
	}
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	stop := startDebugServer(true, addr.Port)
	stop()
	if output := logs.String(); strings.Contains(output, "listening") || !strings.Contains(output, "error") {
		t.Errorf("failed bind announced success: %q", output)
	}
}

func TestRunFailureFlushesProfiles(t *testing.T) {
	previousLogger, previousDebug, previousLogOutput := slog.Default(), debugMode, log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		debugMode = previousDebug
		log.SetOutput(previousLogOutput)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("listener is not TCP")
	}
	dir := t.TempDir()
	profile := filepath.Join(dir, "cpu.pprof")
	trace := filepath.Join(dir, "execution.trace")
	err = run(cliOptions{
		port: addr.Port, libraryPath: filepath.Join(dir, "library"), fontsPath: filepath.Join(dir, "fonts"),
		cpuProfile: profile, tracePath: trace, noBrowser: true, noColor: true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot listen on") {
		t.Fatalf("run error = %v, want occupied-port failure", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("CPU profile was not flushed: %v", err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Errorf("incomplete CPU profile: %v", err)
	}
	_ = reader.Close()
	data, err = os.ReadFile(trace)
	if err != nil || len(data) <= 16 {
		t.Fatalf("trace was not flushed: bytes=%d, error=%v", len(data), err)
	}
}

func TestDiagnosticsDisabled(t *testing.T) {
	t.Parallel()
	startProfiling("", "")()
	startDebugServer(false, 0)()
}
