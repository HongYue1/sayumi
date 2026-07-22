package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
