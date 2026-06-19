package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"sayumi/internal/api"
	"sayumi/internal/fonts"
	"sayumi/internal/storage"
)

//go:embed dist
var frontendDist embed.FS

var debugMode bool

// Build metadata, overridden at link time via -ldflags "-X main.version=…".
// Defaults apply to `go run`/`go build` without the build scripts.
var (
	version   = "dev"
	buildDate = "unknown"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiClear  = "\033[H\033[2J"
)

func main() {
	port := flag.Int("port", 8080, "Port to listen on")
	libraryPath := flag.String("library", "", "Path to the library root directory")
	fontsPath := flag.String("fonts", "", "Path to the user fonts directory")
	network := flag.Bool("network", false, "Allow LAN access (bind to 0.0.0.0)")
	debugFlag := flag.Bool("debug", false, "Enable verbose debug logging")
	showVersion := flag.Bool("version", false, "Print version and exit")
	pprofFlag := flag.Bool("pprof", false, "Expose net/http/pprof on 127.0.0.1:<pprof-port> (diagnostics)")
	pprofPort := flag.Int("pprof-port", 6060, "Port for the localhost-only pprof debug server")
	cpuProfile := flag.String("cpuprofile", "", "Write a CPU profile to this file (diagnostics)")
	traceFile := flag.String("trace", "", "Write an execution trace to this file (diagnostics)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sayumi %s (built %s, %s)\n", version, buildDate, runtime.Version())
		return
	}

	debugMode = *debugFlag

	libRoot := *libraryPath
	if libRoot == "" {
		if envPath := os.Getenv("SAYUMI_LIBRARY"); envPath != "" {
			libRoot = envPath
		} else {
			exe, err := os.Executable()
			if err != nil {
				fatal("cannot determine executable path: %v", err)
			}
			libRoot = filepath.Join(filepath.Dir(exe), "Library")
		}
	}

	absLibRoot, err := filepath.Abs(libRoot)
	if err != nil {
		fatal("invalid library path %q: %v", libRoot, err)
	}
	if err := os.MkdirAll(absLibRoot, 0o755); err != nil {
		fatal("cannot create library directory %q: %v", absLibRoot, err)
	}

	if debugMode {
		slog.SetDefault(slog.New(newPrettyHandler(os.Stderr, slog.LevelDebug)))
	} else {
		slog.SetDefault(slog.New(newPrettyHandler(os.Stderr, slog.LevelWarn)))
		log.SetOutput(io.Discard)
	}

	// Diagnostics (all no-ops unless the matching flag is set): an optional CPU
	// profile / execution trace written to a file, and an optional localhost-only
	// pprof server. See cmd/sayumi/debug.go.
	stopProfiling := startProfiling(*cpuProfile, *traceFile)
	defer stopProfiling()
	startDebugServer(*pprofFlag, *pprofPort)

	profilesDB, err := storage.OpenProfilesDB(absLibRoot)
	if err != nil {
		fatal("cannot open profiles database: %v", err)
	}
	defer func() {
		if err := profilesDB.Close(); err != nil {
			slog.Error("profiles db close error", "err", err)
		}
	}()

	profileMgr := api.NewProfileManager(absLibRoot)
	defer profileMgr.CloseAll()

	fontScanner := fonts.NewScanner(resolveFontsDir(*fontsPath))

	deps := api.NewDependencies(profilesDB, profileMgr, absLibRoot, fontScanner)

	handler := buildHandler(deps)

	manager := &serverManager{
		handler:     handler,
		port:        *port,
		networkMode: *network,
		libraryPath: absLibRoot,
		serverErrs:  make(chan error, 1),
	}

	if err := manager.start(); err != nil {
		fatal("%v", err)
	}
	manager.render()
	openBrowser(fmt.Sprintf("http://localhost:%d", *port))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go deps.StartBackgroundTasks(ctx)

	shutdownCtx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	go closeReadCloserOnDone(shutdownCtx, os.Stdin)

	inputCh := make(chan string, 1)
	go readInput(inputCh)

	for {
		select {
		case <-ctx.Done():
			shutdown()
			manager.stop()
			return
		case err := <-manager.Errors():
			shutdown()
			manager.stop()
			fmt.Fprintf(os.Stderr, "\n  %sserver error: %v%s\n\n", ansiRed, err, ansiReset)
			return
		case cmd := <-inputCh:
			switch cmd {
			case "n":
				manager.toggleNetwork()
				manager.render()
			case "q", "quit", "exit":
				shutdown()
				manager.stop()
				return
			}
		}
	}
}

type serverManager struct {
	mu          sync.Mutex
	srv         *http.Server
	handler     http.Handler
	port        int
	networkMode bool
	libraryPath string
	serverErrs  chan error
}

func (sm *serverManager) bindHost() string {
	if sm.networkMode {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (sm *serverManager) addr() string {
	return fmt.Sprintf("%s:%d", sm.bindHost(), sm.port)
}

func (sm *serverManager) reportServeError(err error) {
	select {
	case sm.serverErrs <- err:
	default:
	}
}

func (sm *serverManager) Errors() <-chan error {
	return sm.serverErrs
}

func (sm *serverManager) start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	addr := sm.addr()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	server := &http.Server{
		Handler: sm.handler,
		// ReadTimeout is intentionally 0 (unlimited) because the upload
		// handler accepts bodies up to 100 MB. Per-handler protection is
		// provided by http.MaxBytesReader in uploadBookHandler.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	sm.srv = server

	go func(srv *http.Server, ln net.Listener) {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			sm.reportServeError(err)
		}
	}(server, listener)

	return nil
}

func (sm *serverManager) stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.srv == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sm.srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	sm.srv = nil
}

func (sm *serverManager) toggleNetwork() {
	sm.stop()

	sm.mu.Lock()
	sm.networkMode = !sm.networkMode
	sm.mu.Unlock()

	if err := sm.start(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  %serror: %v%s\n\n", ansiRed, err, ansiReset)

		sm.mu.Lock()
		sm.networkMode = !sm.networkMode
		sm.mu.Unlock()

		if restoreErr := sm.start(); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "  %serror: could not restore server: %v%s\n\n",
				ansiRed, restoreErr, ansiReset)
			// Both listen attempts failed; signal the main loop to exit cleanly
			// rather than leaving the process alive with no TCP listener.
			sm.reportServeError(fmt.Errorf("server failed and could not be restored: %w", restoreErr))
		}
	}
}

func (sm *serverManager) render() {
	const sep = "────────────────────────────────────────"

	fmt.Print(ansiClear)
	fmt.Println()

	// App name
	fmt.Printf("  %s%ssayumi%s\n", ansiBold, ansiCyan, ansiReset)
	fmt.Println()

	// Status indicator + URLs
	if sm.networkMode {
		fmt.Printf("  %s◉%s  %s%shttp://localhost:%d%s\n",
			ansiYellow, ansiReset, ansiBold, ansiYellow, sm.port, ansiReset)
		if ip := lanIP(); ip != "" {
			fmt.Printf("     %s%shttp://%s:%d%s\n",
				ansiBold, ansiYellow, ip, sm.port, ansiReset)
		}
	} else {
		fmt.Printf("  %s●%s  %s%shttp://localhost:%d%s\n",
			ansiGreen, ansiReset, ansiBold, ansiGreen, sm.port, ansiReset)
	}

	// Library path, indented under the URL
	fmt.Printf("     %s%s%s\n", ansiDim, shortenPath(sm.libraryPath), ansiReset)
	fmt.Println()

	// Divider
	fmt.Printf("  %s%s%s\n", ansiDim, sep, ansiReset)
	fmt.Println()

	// Actions — full English labels so they're immediately obvious
	if sm.networkMode {
		fmt.Printf("  %s[N]%s  Restrict to this device\n", ansiBold, ansiReset)
	} else {
		fmt.Printf("  %s[N]%s  Expose to network\n", ansiBold, ansiReset)
	}
	fmt.Printf("  %s[Q]%s  Quit\n", ansiBold, ansiReset)
	fmt.Println()
	fmt.Printf("  %s›%s ", ansiDim, ansiReset)
}

// resolveFontsDir determines the user-fonts directory. Precedence: --fonts
// flag, SAYUMI_FONTS env, then a "Fonts" folder beside the executable. The
// directory is created if missing (best-effort) so users can find where to
// drop font families; user fonts are optional, so failure is non-fatal.
func resolveFontsDir(flagValue string) string {
	dir := flagValue
	if dir == "" {
		if envPath := os.Getenv("SAYUMI_FONTS"); envPath != "" {
			dir = envPath
		} else if exe, err := os.Executable(); err == nil {
			dir = filepath.Join(filepath.Dir(exe), "Fonts")
		}
	}
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		slog.Warn("invalid fonts path; user fonts disabled", "path", dir, "err", err)
		return ""
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		slog.Warn("cannot create fonts directory; user fonts may be unavailable", "path", abs, "err", err)
	}
	return abs
}

func buildHandler(deps *api.Dependencies) http.Handler {
	fontHandler := http.StripPrefix("/fonts", fonts.Handler(deps.Fonts))

	distFS, err := fs.Sub(frontendDist, "dist")
	if err != nil {
		fatal("cannot access embedded frontend: %v", err)
	}
	fileServer := http.FileServer(http.FS(distFS))

	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath, ok := sanitizeStaticRequestPath(r.URL.Path)
		if !ok {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "invalid path\n")
			return
		}

		exists, err := staticPathExists(distFS, urlPath)
		if err != nil {
			slog.Error("stat static path failed", "path", urlPath, "err", err)
			writeInternalServerError(w, r)
			return
		}

		if !exists {
			if shouldServeAppShell(urlPath) {
				urlPath = "/"
			} else {
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.NotFound(w, r)
				return
			}
		}

		r.URL.Path = urlPath

		if strings.HasPrefix(urlPath, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")

		fileServer.ServeHTTP(w, r)
	})

	handler := api.NewHandler(deps, fontHandler, staticHandler)
	// A single middleware both recovers panics and access-logs the request,
	// sharing one statusWriter so each request pays for just one wrapper instead
	// of two. The deferred closure recovers first (writing a 500 if the handler
	// panicked before writing anything), then logs the request with the final
	// status — so panicking requests are still access-logged.
	handler = instrumentMiddleware(handler)
	return handler
}

func sanitizeStaticRequestPath(rawPath string) (string, bool) {
	if rawPath == "" || rawPath == "/" {
		return "/", true
	}
	if strings.Contains(rawPath, `\`) {
		return "", false
	}

	cleaned := path.Clean("/" + rawPath)
	if cleaned == "/" {
		return "/", true
	}
	return cleaned, true
}

func staticPathExists(staticFS fs.FS, urlPath string) (bool, error) {
	trimmed := strings.TrimPrefix(urlPath, "/")
	if trimmed == "" {
		return true, nil
	}

	_, err := fs.Stat(staticFS, trimmed)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func shouldServeAppShell(urlPath string) bool {
	if urlPath == "/" || strings.HasPrefix(urlPath, "/assets/") {
		return false
	}
	return path.Ext(urlPath) == ""
}

func lanIP() string {
	// Best-effort probe to discover the outbound interface IP (no packets are
	// sent for UDP). The 2s timeout keeps startup snappy if the network is down.
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	return addr.IP.String()
}

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}

	rel, err := filepath.Rel(home, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	if rel == "." {
		return "~"
	}
	return "~/" + filepath.ToSlash(rel)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(context.Background(), "cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.CommandContext(context.Background(), "open", url)
	default:
		cmd = exec.CommandContext(context.Background(), "xdg-open", url)
	}

	go func() {
		if err := cmd.Run(); err != nil && debugMode {
			slog.Debug("open browser failed", "err", err)
		}
	}()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\n  %serror: %s%s\n\n", ansiRed, fmt.Sprintf(format, args...), ansiReset)
	os.Exit(1)
}

func readInput(ch chan string) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if line == "" {
			continue
		}

		// Block until the main loop consumes the command. Console input is
		// low-rate, so backpressure here is harmless. The previous coalescing
		// select drained a full buffer with <-ch in its default branch, which
		// raced the main loop's own receive: if main consumed the buffered
		// command in that window, the drain blocked forever (this goroutine is
		// the channel's only sender), permanently wedging console input until
		// the process was signaled.
		ch <- line
	}

	if err := scanner.Err(); err != nil && debugMode {
		slog.Debug("stdin read error", "err", err)
	}
}

func closeReadCloserOnDone(ctx context.Context, closer io.Closer) {
	<-ctx.Done()
	_ = closer.Close()
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int
}

func (sw *statusWriter) markWritten(status int) {
	if sw.wrote {
		return
	}
	sw.status = status
	sw.wrote = true
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.markWritten(code)
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	sw.markWritten(http.StatusOK)
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

func (sw *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	sw.markWritten(http.StatusOK)
	if rf, ok := sw.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		sw.bytes += int(n)
		return n, err
	}
	n, err := io.Copy(sw.ResponseWriter, r)
	sw.bytes += int(n)
	return n, err
}

func (sw *statusWriter) Flush() {
	sw.markWritten(http.StatusOK)
	if flusher, ok := sw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (sw *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := sw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

// instrumentMiddleware recovers panics and access-logs every request through a
// single statusWriter. The request is logged at Debug level (so it only appears
// under --debug and never pollutes the terminal UI). Recovering and logging in
// one deferred closure means a panicking handler still gets a 500 written and a
// log line with the final status, without a second wrapper allocation.
func instrumentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))
				if !writer.wrote {
					writeInternalServerError(writer, r)
				}
			}

			// The access log is Debug-only (never emitted without --debug). Guard the
			// whole call so a normal run doesn't pay humanizeBytes' Sprintf allocation
			// plus the variadic any-boxing on every request just to discard the line.
			if slog.Default().Enabled(r.Context(), slog.LevelDebug) {
				slog.Log(
					r.Context(), slog.LevelDebug, "request",
					"method", r.Method,
					"path", r.URL.Path,
					"status", writer.status,
					"size", humanizeBytes(writer.bytes),
					"duration", time.Since(start),
				)
			}
		}()

		next.ServeHTTP(writer, r)
	})
}

// humanizeBytes renders a response body size in B/KB/MB for the debug
// access log so large payloads are easy to spot at a glance.
func humanizeBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

func writeInternalServerError(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"internal server error","code":"server_error"}`+"\n")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, "internal server error\n")
}
