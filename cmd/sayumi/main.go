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
	"strconv"
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
	opts, err := parseCLI(os.Args[1:], os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		writeCLIError(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Run sayumi -help for usage.")
		os.Exit(2)
	}
	if opts.showVersion {
		fmt.Printf("sayumi %s (built %s, %s)\n", version, buildDate, runtime.Version())
		return
	}
	// Only main exits the process, after run's deferred cleanup has completed.
	if err := run(opts); err != nil {
		writeCLIError(os.Stderr, err)
		os.Exit(1)
	}
}

func run(opts cliOptions) error {
	debugMode = opts.debug
	level := slog.LevelWarn
	if debugMode {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(newPrettyHandler(consoleOutput(os.Stderr, useANSI(os.Stderr, opts.noColor)), level)))
	if !debugMode {
		log.SetOutput(io.Discard)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	libRoot := opts.libraryPath
	if libRoot == "" {
		if envPath := os.Getenv("SAYUMI_LIBRARY"); envPath != "" {
			libRoot = envPath
		} else {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("cannot determine executable path: %w", err)
			}
			libRoot = filepath.Join(filepath.Dir(exe), "Library")
		}
	}

	absLibRoot, err := filepath.Abs(libRoot)
	if err != nil {
		return fmt.Errorf("invalid library path %q: %w", libRoot, err)
	}
	if err := os.MkdirAll(absLibRoot, 0o755); err != nil {
		return fmt.Errorf("cannot create library directory %q: %w", absLibRoot, err)
	}

	stopProfiling := startProfiling(opts.cpuProfile, opts.tracePath)
	defer stopProfiling()
	stopDebugServer := startDebugServer(opts.pprof, opts.pprofPort)
	defer stopDebugServer()

	profilesDB, err := storage.OpenProfilesDB(absLibRoot)
	if err != nil {
		return fmt.Errorf("cannot open profiles database: %w", err)
	}
	defer func() {
		if err := profilesDB.Close(); err != nil {
			slog.Error("profiles db close error", "err", err)
		}
	}()

	profileMgr := api.NewProfileManager(absLibRoot)
	defer profileMgr.CloseAll()

	fontScanner := fonts.NewScanner(resolveFontsDir(opts.fontsPath))

	deps, err := api.NewDependencies(profilesDB, profileMgr, absLibRoot, fontScanner)
	if err != nil {
		return fmt.Errorf("cannot initialize server dependencies: %w", err)
	}
	// Hand the linker-stamped build metadata over once, before any request can
	// reach it: GET /api/version is what lets the About sheet name the binary it
	// is talking to, and -buildvcs=false keeps that out of debug.BuildInfo.
	deps.Build = api.BuildInfo{Version: version, BuildDate: buildDate}

	// Rehydrate "remember me" sessions persisted by a previous run so restarting
	// the server doesn't sign everyone out. Non-fatal: on failure the server
	// still runs, users just have to log in again.
	if err := deps.RestoreSessions(); err != nil {
		slog.Error("restore sessions", "err", err)
	}

	handler, err := buildHandler(deps)
	if err != nil {
		return err
	}

	manager := &serverManager{
		handler:     handler,
		port:        opts.port,
		networkMode: opts.network,
		libraryPath: absLibRoot,
		serverErrs:  make(chan error, 1),
	}

	if err := manager.start(); err != nil {
		return err
	}
	defer manager.stop()
	color := useANSI(os.Stdout, opts.noColor)
	output := consoleOutput(os.Stdout, color)
	redraw := color && !debugMode
	manager.render(output, redraw)
	if !opts.noBrowser {
		// Use the listener's assigned port when -port 0 was requested.
		openBrowser(fmt.Sprintf("http://localhost:%d", manager.port))
	}

	backgroundDone := make(chan struct{})
	go func() {
		defer close(backgroundDone)
		deps.StartBackgroundTasks(ctx)
	}()
	defer func() {
		// Maintenance must stop before its databases are closed.
		stop()
		<-backgroundDone
	}()
	go closeReadCloserOnDone(ctx, os.Stdin)
	inputCh := make(chan string, 1)
	go readInput(ctx, os.Stdin, inputCh)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-manager.Errors():
			return fmt.Errorf("server error: %w", err)
		case cmd := <-inputCh:
			switch cmd {
			case "n":
				manager.toggleNetwork()
				manager.render(output, redraw)
			case "q", "quit", "exit":
				return nil
			default:
				_, _ = fmt.Fprintln(output, "Unknown command. Enter n to toggle network access or q to quit.")
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
	return net.JoinHostPort(sm.bindHost(), strconv.Itoa(sm.port))
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

	// With -port 0, the banner and browser need the port assigned by the OS.
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		sm.port = tcpAddr.Port
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
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sm.srv.Shutdown(ctx); err != nil {
		// An open browser tab can hold a connection active past the grace
		// window (a mid-flight request, or a large chapter/cover/font transfer
		// still streaming). That's expected on quit, so force-close the
		// stragglers and log it quietly instead of as an error.
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Info("forced shutdown after grace period", "err", err)
			_ = sm.srv.Close()
		} else {
			slog.Error("shutdown error", "err", err)
		}
	}
	sm.srv = nil
}

func (sm *serverManager) toggleNetwork() {
	sm.stop()

	sm.mu.Lock()
	sm.networkMode = !sm.networkMode
	sm.mu.Unlock()

	if err := sm.start(); err != nil {
		writeCLIError(os.Stderr, err)

		sm.mu.Lock()
		sm.networkMode = !sm.networkMode
		sm.mu.Unlock()

		if restoreErr := sm.start(); restoreErr != nil {
			writeCLIError(os.Stderr, fmt.Errorf("could not restore server: %w", restoreErr))
			// Both listen attempts failed; signal the main loop to exit cleanly
			// rather than leaving the process alive with no TCP listener.
			sm.reportServeError(fmt.Errorf("server failed and could not be restored: %w", restoreErr))
		}
	}
}

func (sm *serverManager) render(out io.Writer, redraw bool) {
	const sep = "────────────────────────────────────────"

	// This banner is best-effort informational output, not a server operation.
	// Preserve diagnostics and redirected output instead of erasing the screen.
	if redraw {
		_, _ = fmt.Fprint(out, ansiClear)
	}
	_, _ = fmt.Fprintf(out, "\n  %s%ssayumi%s\n\n", ansiBold, ansiCyan, ansiReset)
	if sm.networkMode {
		_, _ = fmt.Fprintf(out, "  %s◉%s  %s%shttp://localhost:%d%s\n",
			ansiYellow, ansiReset, ansiBold, ansiYellow, sm.port, ansiReset)
		if ip := lanIP(); ip != "" {
			_, _ = fmt.Fprintf(out, "     %s%shttp://%s:%d%s\n",
				ansiBold, ansiYellow, ip, sm.port, ansiReset)
		}
	} else {
		_, _ = fmt.Fprintf(out, "  %s●%s  %s%shttp://localhost:%d%s\n",
			ansiGreen, ansiReset, ansiBold, ansiGreen, sm.port, ansiReset)
	}
	_, _ = fmt.Fprintf(out, "     %s%s%s\n\n", ansiDim, escapeLogText(shortenPath(sm.libraryPath)), ansiReset)
	_, _ = fmt.Fprintf(out, "  %s%s%s\n\n", ansiDim, sep, ansiReset)
	if sm.networkMode {
		_, _ = fmt.Fprintf(out, "  %s[N]%s  Restrict to this device\n", ansiBold, ansiReset)
	} else {
		_, _ = fmt.Fprintf(out, "  %s[N]%s  Expose to network\n", ansiBold, ansiReset)
	}
	_, _ = fmt.Fprintf(out, "  %s[Q]%s  Quit\n\n", ansiBold, ansiReset)
	_, _ = fmt.Fprintln(out, "  Enter a command and press Enter; Ctrl+C also quits.")
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

func buildHandler(deps *api.Dependencies) (http.Handler, error) {
	fontHandler := http.StripPrefix("/fonts", fonts.Handler(deps.Fonts))

	distFS, err := fs.Sub(frontendDist, "dist")
	if err != nil {
		return nil, fmt.Errorf("cannot access embedded frontend: %w", err)
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
	return instrumentMiddleware(handler), nil
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

// staticPathExists reports whether urlPath names a real file in the embedded SPA
// build. This stat and http.FileServer's own are both embed.FS map lookups, and
// the pair is what lets an unknown extensionless route fall back to the app
// shell instead of 404ing.
//
// A path index built once at startup was tried and rejected: it only sped up
// misses, while making real asset hits, the app-shell fallback, and handler
// construction slower.
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
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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

func readInput(ctx context.Context, input io.Reader, ch chan<- string) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if line == "" {
			continue
		}

		// Preserve command order, but do not strand a sender when main exits.
		select {
		case ch <- line:
		case <-ctx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil && debugMode && ctx.Err() == nil {
		slog.Debug("stdin read error", "err", err)
	}
}

func closeReadCloserOnDone(ctx context.Context, closer io.Closer) {
	<-ctx.Done()
	_ = closer.Close()
}

type responseTracker struct {
	http.ResponseWriter
	wrote bool
}

func (w *responseTracker) markWritten() {
	if w.wrote {
		return
	}
	w.wrote = true
}

func (w *responseTracker) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	// Informational responses do not commit the final response. A protocol
	// switch is the exception, matching net/http's 101 handling.
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.markWritten()
	}
}

func (w *responseTracker) Write(p []byte) (int, error) {
	w.markWritten()
	return w.ResponseWriter.Write(p)
}

func (w *responseTracker) ReadFrom(r io.Reader) (int64, error) {
	w.markWritten()
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(w.ResponseWriter, r)
}

func (w *responseTracker) Flush() {
	w.markWritten()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		// Once ownership transfers, recovery must not write an HTTP error body.
		w.markWritten()
	}
	return conn, rw, err
}

func (w *responseTracker) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type statusWriter struct {
	responseTracker
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	wasWritten := w.wrote
	w.responseTracker.WriteHeader(code)
	if !wasWritten && w.wrote {
		w.status = code
	}
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.status = http.StatusOK
	}
	n, err := w.responseTracker.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wrote {
		w.status = http.StatusOK
	}
	w.markWritten()
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Flush() {
	if !w.wrote {
		w.status = http.StatusOK
	}
	w.responseTracker.Flush()
}

// instrumentMiddleware specializes once at construction time. Normal runs use
// only the response state needed for panic recovery; debug runs additionally
// collect status, size, and duration for the access log. This keeps diagnostics
// off the release hot path without weakening recovery or optional interfaces.
func instrumentMiddleware(next http.Handler) http.Handler {
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return debugInstrumentMiddleware(next)
	}
	return recoveryMiddleware(next)
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &responseTracker{ResponseWriter: w}

		defer func() {
			if rec := recover(); rec != nil {
				// Let net/http abort the connection without logging a stack trace.
				if rec == http.ErrAbortHandler { //nolint:errorlint // net/http only suppresses the exact sentinel, not wrapped errors.
					panic(rec)
				}
				slog.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))
				if !writer.wrote {
					writeInternalServerError(writer, r)
				}
			}
		}()

		next.ServeHTTP(writer, r)
	})
}

func debugInstrumentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		writer := &statusWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		defer func() {
			if rec := recover(); rec != nil {
				// Let net/http abort the connection without logging a stack trace.
				if rec == http.ErrAbortHandler { //nolint:errorlint // net/http only suppresses the exact sentinel, not wrapped errors.
					panic(rec)
				}
				slog.Error("panic recovered", "panic", rec, "stack", string(debug.Stack()))
				if !writer.wrote {
					writeInternalServerError(writer, r)
				}
			}

			slog.Log(
				r.Context(), slog.LevelDebug, "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", writer.status,
				"size", humanizeBytes(writer.bytes),
				"duration", time.Since(start),
			)
		}()

		next.ServeHTTP(writer, r)
	})
}

// humanizeBytes renders a response body size in B/KB/MB for the debug
// access log so large payloads are easy to spot at a glance. The size is
// int64 because ReadFrom reports int64 and a >2 GiB body must not wrap on
// 32-bit builds.
func humanizeBytes(n int64) string {
	switch {
	case n < 1024:
		return strconv.FormatInt(n, 10) + "B"
	case n < 1024*1024:
		return strconv.FormatFloat(float64(n)/1024, 'f', 1, 64) + "KB"
	default:
		return strconv.FormatFloat(float64(n)/(1024*1024), 'f', 1, 64) + "MB"
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
