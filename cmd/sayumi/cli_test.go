package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCLI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"defaults", nil, false},
		{"ports zero", []string{"-port=0", "-pprof", "-pprof-port=0"}, false},
		{"port upper bound", []string{"-port=65535"}, false},
		{"negative port", []string{"-port=-1"}, true},
		{"oversized port", []string{"-port=65536"}, true},
		{"invalid pprof port", []string{"-pprof", "-pprof-port=65536"}, true},
		{"unused pprof port", []string{"-pprof-port=-1"}, false},
		{"positional argument", []string{"library.epub"}, true},
		{"unknown flag", []string{"-typo"}, true},
		{"missing value", []string{"-library"}, true},
		{"no browser or color", []string{"-no-browser", "-no-color"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			opts, err := parseCLI(tc.args, &output)
			if (err != nil) != tc.wantError {
				t.Fatalf("parseCLI error = %v, want error %v", err, tc.wantError)
			}
			if output.Len() != 0 {
				t.Errorf("non-help invocation printed usage: %q", output.String())
			}
			if tc.name == "defaults" && (opts.port != 8080 || opts.pprofPort != 6060 || opts.noBrowser || opts.noColor) {
				t.Errorf("unexpected defaults: %+v", opts)
			}
			if tc.name == "no browser or color" && (!opts.noBrowser || !opts.noColor) {
				t.Errorf("flags not applied: %+v", opts)
			}
		})
	}
}

func TestParseCLIHelp(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	_, err := parseCLI([]string{"-help"}, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"Usage:", "-no-browser", "-no-color", "SAYUMI_LIBRARY", "SAYUMI_FONTS", "NO_COLOR"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestParseCLIConflictingProfiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.out")
	aliases := []string{path, dir + string(filepath.Separator) + "." + string(filepath.Separator) + "profile.out"}
	if runtime.GOOS == "windows" {
		aliases = append(aliases, strings.ToUpper(path))
	}
	for _, alias := range aliases {
		if _, err := parseCLI([]string{"-cpuprofile", path, "-trace", alias}, io.Discard); err == nil {
			t.Errorf("accepted conflicting profile paths %q and %q", path, alias)
		}
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "hardlink.out")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := parseCLI([]string{"-cpuprofile", path, "-trace", alias}, io.Discard); err == nil {
		t.Error("accepted hard-linked profile outputs")
	}
}

func TestUseANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	file, err := os.CreateTemp(t.TempDir(), "redirected")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if useANSI(file, false) {
		t.Error("redirected output should not use ANSI")
	}
	if useANSI(os.Stdout, true) {
		t.Error("-no-color must disable ANSI")
	}
	t.Setenv("NO_COLOR", "1")
	if useANSI(os.Stdout, false) {
		t.Error("NO_COLOR must disable ANSI")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if useANSI(os.Stdout, false) {
		t.Error("TERM=dumb must disable ANSI")
	}
}

func TestConsoleOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	text := ansiClear + ansiBold + "sayumi" + ansiReset
	writer := consoleOutput(&output, false)
	if n, err := io.WriteString(writer, text); err != nil || n != len(text) {
		t.Fatalf("Write = (%d, %v), want %d bytes", n, err, len(text))
	}
	if got := output.String(); got != "sayumi" {
		t.Errorf("plain output = %q", got)
	}
	output.Reset()
	if _, err := io.WriteString(consoleOutput(&output, true), text); err != nil {
		t.Fatal(err)
	}
	if output.String() != text {
		t.Errorf("colored output changed: %q", output.String())
	}
	want := errors.New("writer failed")
	if _, err := io.WriteString(consoleOutput(failingLogWriter{want}, false), text); !errors.Is(err, want) {
		t.Errorf("writer error = %v, want %v", err, want)
	}
}

type shortConsoleWriter struct{}

func (shortConsoleWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestConsoleOutputShortWrite(t *testing.T) {
	t.Parallel()
	writer := consoleOutput(shortConsoleWriter{}, false)
	if _, err := io.WriteString(writer, ansiBold+"hello"+ansiReset); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("error = %v, want io.ErrShortWrite", err)
	}
}

func TestCLIErrorEscapesControls(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	writeCLIError(&output, errors.New("invalid\n\x1b[2J"))
	if got, want := output.String(), "sayumi: invalid\\n\\x1b[2J\n"; got != want {
		t.Errorf("error output = %q, want %q", got, want)
	}
}

func TestConsoleRender(t *testing.T) {
	t.Parallel()
	manager := &serverManager{port: 12345, libraryPath: "Library\n\x1b[2J"}
	var output bytes.Buffer
	manager.render(consoleOutput(&output, false), false)
	got := output.String()
	if strings.Contains(got, "\x1b") || !strings.Contains(got, `Library\n\x1b[2J`) {
		t.Errorf("unsafe or colored plain output: %q", got)
	}
	for _, want := range []string{"http://localhost:12345", "[N]", "[Q]", "press Enter"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q", want)
		}
	}
	output.Reset()
	manager.render(&output, false)
	if strings.Contains(output.String(), ansiClear) {
		t.Error("non-redraw render erased diagnostics")
	}
	output.Reset()
	manager.render(&output, true)
	if !strings.HasPrefix(output.String(), ansiClear) {
		t.Error("interactive redraw did not clear the screen")
	}
}

func TestReadInput(t *testing.T) {
	t.Parallel()
	ch := make(chan string, 3)
	readInput(t.Context(), strings.NewReader(" \n N \r\nQuit\nEXIT\n"), ch)
	if len(ch) != 3 {
		t.Fatalf("received %d commands, want 3", len(ch))
	}
	got := []string{<-ch, <-ch, <-ch}
	if want := []string{"n", "quit", "exit"}; !slices.Equal(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}
}

func TestReadInputCancellationUnblocksSend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(done)
		readInput(ctx, strings.NewReader("n\nq\n"), ch)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readInput remained blocked sending a command")
	}
}

func TestShortenPathDotDotPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ input, want string }{
		{home, "~"},
		{filepath.Join(home, "..notes"), "~/..notes"},
		{filepath.Join(home, "Library"), "~/Library"},
		{filepath.Dir(home), filepath.Dir(home)},
	} {
		if got := shortenPath(tc.input); got != tc.want {
			t.Errorf("shortenPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
