package main

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	rtpprof "runtime/pprof"
	"runtime/trace"
	"strconv"
	"time"
)

// startProfiling optionally starts a CPU profile and/or an execution trace,
// writing each to the given file path. It returns a stop function (safe to call
// even when nothing was started) that flushes and closes both, intended to be
// invoked via defer in main. When a path is empty the corresponding profiler is
// not started, so normal runs pay nothing.
func startProfiling(cpuProfilePath, tracePath string) func() {
	var cleanups []func()
	if c := startCPUProfile(cpuProfilePath); c != nil {
		cleanups = append(cleanups, c)
	}
	if c := startTrace(tracePath); c != nil {
		cleanups = append(cleanups, c)
	}
	return func() {
		// Stop in reverse order so the trace stops before the CPU profile,
		// mirroring the order in which they were started.
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
}

func startCPUProfile(path string) func() {
	if path == "" {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		slog.Error("create cpu profile failed", "path", path, "err", err)
		return nil
	}
	if err := rtpprof.StartCPUProfile(f); err != nil {
		slog.Error("start cpu profile failed", "err", err)
		_ = f.Close()
		return nil
	}
	slog.Warn("cpu profiling enabled", "path", path)
	return func() {
		rtpprof.StopCPUProfile()
		if err := f.Close(); err != nil {
			slog.Error("close cpu profile failed", "err", err)
		}
	}
}

func startTrace(path string) func() {
	if path == "" {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		slog.Error("create trace file failed", "path", path, "err", err)
		return nil
	}
	if err := trace.Start(f); err != nil {
		slog.Error("start trace failed", "err", err)
		_ = f.Close()
		return nil
	}
	slog.Warn("execution tracing enabled", "path", path)
	return func() {
		trace.Stop()
		if err := f.Close(); err != nil {
			slog.Error("close trace file failed", "err", err)
		}
	}
}

// startDebugServer starts a localhost-only HTTP server exposing the
// net/http/pprof handlers when enabled. It binds to 127.0.0.1 exclusively
// (never the LAN, even in --network mode) so profiling data is never exposed
// off-device. The server runs until the process exits.
func startDebugServer(enabled bool, port int) {
	if !enabled {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Warn("pprof debug server listening", "addr", "http://"+addr+"/debug/pprof/")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("pprof debug server error", "err", err)
		}
	}()
}
