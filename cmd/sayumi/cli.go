package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type cliOptions struct {
	port, pprofPort                    int
	libraryPath, fontsPath             string
	cpuProfile, tracePath              string
	network, debug, showVersion, pprof bool
	noBrowser, noColor                 bool
}

func parseCLI(args []string, help io.Writer) (cliOptions, error) {
	var opts cliOptions
	flags := flag.NewFlagSet("sayumi", flag.ContinueOnError)
	// Report errors once in main; reserve the full usage text for -help.
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	flags.IntVar(&opts.port, "port", 8080, "Port to listen on (0 chooses an available port)")
	flags.StringVar(&opts.libraryPath, "library", "", "Path to the library root directory")
	flags.StringVar(&opts.fontsPath, "fonts", "", "Path to the user fonts directory")
	flags.BoolVar(&opts.network, "network", false, "Allow LAN access (bind to 0.0.0.0)")
	flags.BoolVar(&opts.debug, "debug", false, "Enable verbose debug logging")
	flags.BoolVar(&opts.showVersion, "version", false, "Print version and exit")
	flags.BoolVar(&opts.noBrowser, "no-browser", false, "Do not open a browser on startup")
	flags.BoolVar(&opts.noColor, "no-color", false, "Disable colors and screen clearing (also honors NO_COLOR)")
	flags.BoolVar(&opts.pprof, "pprof", false, "Expose localhost-only net/http/pprof diagnostics")
	flags.IntVar(&opts.pprofPort, "pprof-port", 6060, "Port for pprof (0 chooses an available port)")
	flags.StringVar(&opts.cpuProfile, "cpuprofile", "", "Write a CPU profile to this file (diagnostics)")
	flags.StringVar(&opts.tracePath, "trace", "", "Write an execution trace to this file (diagnostics)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(help)
			_, _ = fmt.Fprintln(help, "Usage: sayumi [options]")
			flags.PrintDefaults()
			_, _ = fmt.Fprintln(help, "\nEnvironment: SAYUMI_LIBRARY, SAYUMI_FONTS, NO_COLOR")
		}
		return opts, err
	}
	if flags.NArg() != 0 {
		return opts, fmt.Errorf("unexpected arguments: %q", flags.Args())
	}
	if opts.port < 0 || opts.port > 65535 {
		return opts, fmt.Errorf("-port must be between 0 and 65535, got %d", opts.port)
	}
	if opts.pprof && (opts.pprofPort < 0 || opts.pprofPort > 65535) {
		return opts, fmt.Errorf("-pprof-port must be between 0 and 65535, got %d", opts.pprofPort)
	}
	if opts.cpuProfile != "" && opts.tracePath != "" {
		cpuPath, err := filepath.Abs(opts.cpuProfile)
		if err != nil {
			return opts, fmt.Errorf("invalid CPU profile path: %w", err)
		}
		tracePath, err := filepath.Abs(opts.tracePath)
		if err != nil {
			return opts, fmt.Errorf("invalid trace path: %w", err)
		}
		// The outputs may not exist yet. Stat additionally catches existing
		// hard-link and symlink aliases; opening failures are handled at startup.
		cpuInfo, _ := os.Stat(cpuPath)
		traceInfo, _ := os.Stat(tracePath)
		sameName := cpuPath == tracePath || (runtime.GOOS == "windows" && strings.EqualFold(cpuPath, tracePath))
		if sameName || (cpuInfo != nil && traceInfo != nil && os.SameFile(cpuInfo, traceInfo)) {
			return opts, errors.New("-cpuprofile and -trace must use different output files")
		}
	}
	return opts, nil
}

func useANSI(file *os.File, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Strip only the console's own styles. Caller-controlled strings must still
// pass through escapeLogText before they reach either colored or plain output.
var consoleStyles = strings.NewReplacer(
	ansiReset, "", ansiBold, "", ansiDim, "", ansiRed, "",
	ansiGreen, "", ansiYellow, "", ansiCyan, "", ansiClear, "",
)

type plainConsoleWriter struct{ io.Writer }

func (w plainConsoleWriter) Write(p []byte) (int, error) {
	text := consoleStyles.Replace(string(p))
	n, err := io.WriteString(w.Writer, text)
	if err != nil {
		return 0, err
	}
	if n != len(text) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}

func consoleOutput(w io.Writer, color bool) io.Writer {
	if color {
		return w
	}
	return plainConsoleWriter{w}
}

func writeCLIError(w io.Writer, err error) {
	// Last-resort diagnostics have no further output channel on write failure.
	_, _ = fmt.Fprintf(w, "sayumi: %s\n", escapeLogText(err.Error()))
}
