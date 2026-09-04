package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type gofileRoundTripFunc func(*http.Request) (*http.Response, error)

func (f gofileRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func gofileTestResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestValidateGofileDownloadPage(t *testing.T) {
	t.Parallel()

	valid := []string{
		"https://gofile.io/d/abc123",
		"https://store1.gofile.io/download/abc123",
	}
	for _, raw := range valid {
		if err := validateGofileDownloadPage(raw); err != nil {
			t.Errorf("validateGofileDownloadPage(%q): %v", raw, err)
		}
	}

	invalid := []string{
		"javascript:alert(1)",
		"data:text/html,unsafe",
		"http://gofile.io/d/abc123",
		"https://user@gofile.io/d/abc123",
		"https://gofile.io:444/d/abc123",
		"https://.gofile.io/d/abc123",
		"https://bad..gofile.io/d/abc123",
		"https://gofile.io.evil.example/d/abc123",
		"https://evil.example/d/abc123",
	}
	for _, raw := range invalid {
		if err := validateGofileDownloadPage(raw); err == nil {
			t.Errorf("validateGofileDownloadPage(%q) succeeded, want error", raw)
		}
	}
}

func TestUploadGofileHandlerHoldsReplacementReadLock(t *testing.T) {
	pd := newDownloadTestDeps(t, []byte("epub-content"), "gofile-hash")
	req := httptest.NewRequest(http.MethodPost, "/api/books/download-book/gofile", nil)
	req.SetPathValue("id", downloadTestBookID)
	req = withProfileDeps(req, pd)
	recorder := httptest.NewRecorder()

	uploadStarted := make(chan struct{})
	releaseUpload := make(chan struct{})
	oldClient := gofileClient
	gofileClient = &http.Client{Transport: gofileRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case gofileServersURL:
			return gofileTestResponse(req, `{"status":"ok","data":{"servers":[{"name":"store1"}]}}`), nil
		case "https://store1.gofile.io/uploadFile":
			close(uploadStarted)
			<-releaseUpload
			defer func() { _ = req.Body.Close() }()
			if _, err := io.Copy(io.Discard, req.Body); err != nil {
				return nil, fmt.Errorf("read upload body: %w", err)
			}
			return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected request URL %q", req.URL)
		}
	})}
	t.Cleanup(func() { gofileClient = oldClient })

	done := make(chan struct{})
	go func() {
		uploadGofileHandler(nil)(recorder, req)
		close(done)
	}()

	<-uploadStarted
	if pd.bookReplaceMu.TryLock() {
		pd.bookReplaceMu.Unlock()
		close(releaseUpload)
		<-done
		t.Fatal("replacement write lock acquired during active gofile upload")
	}

	close(releaseUpload)
	<-done
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !pd.bookReplaceMu.TryLock() {
		t.Fatal("replacement write lock remained held after gofile upload completed")
	}
	pd.bookReplaceMu.Unlock()
}

func TestUploadFileToGofileSuccess(t *testing.T) {
	oldClient := gofileClient
	gofileClient = &http.Client{Transport: gofileRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		defer func() { _ = req.Body.Close() }()
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, fmt.Errorf("read upload body: %w", err)
		}
		if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data;") {
			return nil, fmt.Errorf("content-type = %q, want multipart", ct)
		}
		return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
	})}
	t.Cleanup(func() { gofileClient = oldClient })

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("epub-content"), 0o644); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	page, err := uploadFileToGofile(t.Context(), "store1", path)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if page != "https://gofile.io/d/abc123" {
		t.Fatalf("page = %q, want download page", page)
	}
}

// TestUploadFileToGofileTransportErrorReleasesWriter pins the streaming
// goroutine's lifecycle: when the transport fails without reading or closing
// the request body, the upload must still return and leave no goroutine
// behind. (The RoundTripper contract asks transports to close the body, but
// the uploader owns its pipe cleanup instead of relying on it.)
//
// This test is intentionally not parallel: it counts process goroutines, and
// parallel siblings only run once sequential tests like this one finish.
func TestUploadFileToGofileTransportErrorReleasesWriter(t *testing.T) {
	oldClient := gofileClient
	gofileClient = &http.Client{Transport: gofileRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	t.Cleanup(func() { gofileClient = oldClient })

	// Large enough that the first pipe write blocks until a reader arrives
	// (io.Pipe is unbuffered), pinning a leaked writer in place.
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o644); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	before := runtime.NumGoroutine()
	if _, err := uploadFileToGofile(t.Context(), "store1", path); err == nil {
		t.Fatal("upload with failing transport succeeded, want error")
	}
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("streaming goroutine leaked: %d goroutines, want %d", runtime.NumGoroutine(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWriteMultipartBodyClosedPipe(t *testing.T) {
	t.Parallel()

	pr, pw := io.Pipe()
	// No reader: the first write fails, exercising the CloseWithError path.
	_ = pr.Close()

	file := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(file, []byte("epub-content"), 0o644); err != nil {
		t.Fatalf("seed book: %v", err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("open book: %v", err)
	}

	mw := multipart.NewWriter(pw)
	if err := writeMultipartBody(mw, pw, f, "book.epub"); err == nil {
		t.Fatal("write to closed pipe succeeded, want error")
	}
}
