package api

import (
	"bytes"
	"context"
	"encoding/json"
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

// Tests replacing the package client stay sequential. Copy its policy so a
// transport double does not silently remove the production redirect guard.
func setGofileTestTransport(t *testing.T, transport gofileRoundTripFunc) {
	t.Helper()
	oldClient := gofileClient
	client := *oldClient
	client.Transport = transport
	gofileClient = &client
	t.Cleanup(func() { gofileClient = oldClient })
}

func TestGofileClientRejectsRedirects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("epub-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, stage := range []string{"servers", "upload"} {
		for _, status := range []int{
			http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
		} {
			for _, target := range []struct {
				name string
				url  string
			}{
				{name: "external host", url: "https://untrusted.example/collect"},
				{name: "loopback", url: "http://127.0.0.1/private"},
				{name: "same host", url: "/redirected"},
			} {
				t.Run(fmt.Sprintf("%s/%d/%s", stage, status, target.name), func(t *testing.T) {
					calls := 0
					setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
						calls++
						if req.Body != nil {
							defer func() { _ = req.Body.Close() }()
						}
						if calls > 1 {
							return nil, errors.New("redirect escaped the original request")
						}
						resp := gofileTestResponse(req, "")
						resp.StatusCode = status
						resp.Header.Set("Location", target.url)
						return resp, nil
					})

					var err error
					if stage == "servers" {
						_, err = pickGofileServer(t.Context())
					} else {
						_, err = uploadFileToGofile(t.Context(), "store1", path)
					}
					if err == nil {
						t.Error("redirect response accepted, want failure")
					}
					if calls != 1 {
						t.Errorf("outbound requests = %d, want only the original request", calls)
					}
				})
			}
		}
	}
}

type gofileTrackedBody struct {
	io.Reader
	readBytes int
	closed    bool
}

func (b *gofileTrackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}

func (b *gofileTrackedBody) Close() error {
	b.closed = true
	return nil
}

func TestPickGofileServerResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{name: "first server", body: `{"status":"ok","data":{"servers":[{"name":"Store-1"},{"name":"store2"}]}}`, want: "Store-1"},
		{name: "http failure", body: `{}`, status: http.StatusServiceUnavailable},
		{name: "invalid json", body: `{`},
		{name: "service failure", body: `{"status":"error","data":{"servers":[{"name":"store1"}]}}`},
		{name: "no servers", body: `{"status":"ok","data":{"servers":[]}}`},
		{name: "empty label", body: `{"status":"ok","data":{"servers":[{"name":""}]}}`},
		{name: "domain injection", body: `{"status":"ok","data":{"servers":[{"name":"evil.example"}]}}`},
		{name: "path injection", body: `{"status":"ok","data":{"servers":[{"name":"evil.example/path"}]}}`},
		{name: "credentials injection", body: `{"status":"ok","data":{"servers":[{"name":"user@evil.example"}]}}`},
		{name: "escape injection", body: `{"status":"ok","data":{"servers":[{"name":"store%2e1"}]}}`},
		{name: "response limit", body: strings.Repeat(" ", 1<<20) + `{"status":"ok","data":{"servers":[{"name":"store1"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &gofileTrackedBody{Reader: strings.NewReader(tc.body)}
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.String() != gofileServersURL {
					t.Errorf("servers request = %s %s", req.Method, req.URL)
				}
				resp := gofileTestResponse(req, "")
				resp.Body = body
				if tc.status != 0 {
					resp.StatusCode = tc.status
				}
				return resp, nil
			})
			server, err := pickGofileServer(t.Context())
			if server != tc.want || (err == nil) != (tc.want != "") {
				t.Errorf("server = (%q, %v), want %q with matching success", server, err, tc.want)
			}
			if !body.closed || body.readBytes > 1<<20 {
				t.Errorf("response lifecycle: closed=%t, bytes=%d", body.closed, body.readBytes)
			}
		})
	}
}

func TestUploadFileToGofileResponseFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, []byte("epub-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "http failure", body: `{}`, status: http.StatusServiceUnavailable},
		{name: "invalid json", body: `{`},
		{name: "service failure", body: `{"status":"error","data":{"downloadPage":"https://gofile.io/d/abc123"}}`},
		{name: "empty page", body: `{"status":"ok","data":{}}`},
		{name: "unsafe page", body: `{"status":"ok","data":{"downloadPage":"javascript:alert(1)"}}`},
		{name: "response limit", body: strings.Repeat(" ", 1<<20) + `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &gofileTrackedBody{Reader: strings.NewReader(tc.body)}
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				defer func() { _ = req.Body.Close() }()
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					return nil, err
				}
				resp := gofileTestResponse(req, "")
				resp.Body = body
				if tc.status != 0 {
					resp.StatusCode = tc.status
				}
				return resp, nil
			})
			if page, err := uploadFileToGofile(t.Context(), "store1", path); page != "" || err == nil {
				t.Errorf("failed response = (%q, %v), want no page and an error", page, err)
			}
			if !body.closed || body.readBytes > 1<<20 {
				t.Errorf("response lifecycle: closed=%t, bytes=%d", body.closed, body.readBytes)
			}
		})
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

func assertGofileHandlerError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	var result map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode handler error: %v", err)
	}
	if recorder.Code != status || len(result) != 2 || result["code"] != code || result["error"] != message {
		t.Errorf("handler error = %d %v, want %d %s: %s", recorder.Code, result, status, code, message)
	}
}

func TestUploadGofileHandlerPreflight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		code    string
		message string
	}{
		{name: "missing profile", status: http.StatusInternalServerError, code: "server_error", message: "profile not available"},
		{name: "missing book", status: http.StatusNotFound, code: "not_found", message: "book not found"},
		{name: "missing path", status: http.StatusNotFound, code: "no_file", message: "book has no file on disk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pd := newDownloadTestDeps(t, []byte("epub-content"), "gofile-hash")
			book, _ := pd.Books.Get(downloadTestBookID)
			book.FilePath = ""
			pd.Books.Add(book)
			if tc.name == "missing book" {
				pd.Books.Remove(downloadTestBookID)
			}
			setGofileTestTransport(t, func(*http.Request) (*http.Response, error) {
				t.Error("preflight failure made an outbound request")
				return nil, errors.New("unexpected outbound request")
			})
			req := httptest.NewRequest(http.MethodPost, "/api/books/download-book/gofile", nil)
			req.SetPathValue("id", downloadTestBookID)
			if tc.name != "missing profile" {
				req = withProfileDeps(req, pd)
			}
			recorder := httptest.NewRecorder()
			uploadGofileHandler(nil)(recorder, req)
			assertGofileHandlerError(t, recorder, tc.status, tc.code, tc.message)
		})
	}
}

func TestUploadGofileHandlerRefreshesSnapshot(t *testing.T) {
	for _, change := range []string{"replace", "remove", "clear path"} {
		t.Run(change, func(t *testing.T) {
			pd := newDownloadTestDeps(t, []byte("old-generation"), "old-hash")
			path := filepath.Join(t.TempDir(), "replacement.epub")
			if err := os.WriteFile(path, []byte("new-generation"), 0o644); err != nil {
				t.Fatal(err)
			}
			calls := 0
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() == gofileServersURL {
					// Lookup must not hold the generation gate. Model a completed
					// replacement/deletion before it returns to the handler.
					if !pd.bookReplaceMu.TryLock() {
						t.Error("generation gate held during server selection")
						return nil, errors.New("generation gate held")
					}
					defer pd.bookReplaceMu.Unlock()
					book, _ := pd.Books.Get(downloadTestBookID)
					switch change {
					case "replace":
						book.FilePath = path
						pd.Books.Add(book)
					case "remove":
						pd.Books.Remove(downloadTestBookID)
					case "clear path":
						book.FilePath = ""
						pd.Books.Add(book)
					}
					return gofileTestResponse(req, `{"status":"ok","data":{"servers":[{"name":"store1"}]}}`), nil
				}
				defer func() { _ = req.Body.Close() }()
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				if !bytes.Contains(body, []byte("new-generation")) || bytes.Contains(body, []byte("old-generation")) {
					t.Errorf("upload did not use the refreshed file: %q", body)
				}
				return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
			})
			req := httptest.NewRequest(http.MethodPost, "/api/books/download-book/gofile", nil)
			req.SetPathValue("id", downloadTestBookID)
			recorder := httptest.NewRecorder()
			uploadGofileHandler(nil)(recorder, withProfileDeps(req, pd))
			if change == "replace" {
				var result map[string]string
				if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if calls != 2 || recorder.Code != http.StatusOK || len(result) != 1 || result["downloadPage"] != "https://gofile.io/d/abc123" {
					t.Errorf("replacement response: calls=%d, status=%d, body=%v", calls, recorder.Code, result)
				}
			} else {
				if calls != 1 {
					t.Errorf("removed file triggered an upload: requests=%d", calls)
				}
				if change == "remove" {
					assertGofileHandlerError(t, recorder, http.StatusNotFound, "not_found", "book not found")
				} else {
					assertGofileHandlerError(t, recorder, http.StatusNotFound, "no_file", "book has no file on disk")
				}
			}
		})
	}
}

func TestUploadGofileHandlerRemoteFailures(t *testing.T) {
	for _, stage := range []string{"servers", "upload", "incomplete upload"} {
		t.Run(stage, func(t *testing.T) {
			pd := newDownloadTestDeps(t, []byte("epub-content"), "gofile-hash")
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.String() == gofileServersURL && stage != "servers" {
					return gofileTestResponse(req, `{"status":"ok","data":{"servers":[{"name":"store1"}]}}`), nil
				}
				if req.Body != nil {
					defer func() { _ = req.Body.Close() }()
				}
				if stage == "incomplete upload" {
					return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
				}
				return nil, errors.New("upstream private diagnostic")
			})
			req := httptest.NewRequest(http.MethodPost, "/api/books/download-book/gofile", nil)
			req.SetPathValue("id", downloadTestBookID)
			recorder := httptest.NewRecorder()
			uploadGofileHandler(nil)(recorder, withProfileDeps(req, pd))
			message := "upload to gofile failed"
			if stage == "servers" {
				message = "could not reach gofile"
			}
			assertGofileHandlerError(t, recorder, http.StatusBadGateway, "gofile_error", message)
		})
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
	setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
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
	})

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
	setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
		defer func() { _ = req.Body.Close() }()
		if req.Method != http.MethodPost || req.URL.String() != "https://store1.gofile.io/uploadFile" {
			return nil, fmt.Errorf("unexpected upload request: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			return nil, errors.New("anonymous upload carried credentials")
		}
		mr, err := req.MultipartReader()
		if err != nil {
			return nil, err
		}
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}
		defer func() { _ = part.Close() }()
		if part.FormName() != "file" || part.FileName() != "book.epub" {
			return nil, fmt.Errorf("unexpected file part: %q %q", part.FormName(), part.FileName())
		}
		content, err := io.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("read file part: %w", err)
		}
		if string(content) != "epub-content" {
			return nil, fmt.Errorf("upload bytes = %q, want epub-content", content)
		}
		// No account, token, or folder fields accompany an anonymous upload.
		extra, err := mr.NextPart()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("finish multipart parts: %w", err)
		}
		if extra != nil || err == nil {
			return nil, errors.New("anonymous upload contained an extra multipart part")
		}
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, fmt.Errorf("finish upload body: %w", err)
		}
		return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
	})

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

func TestUploadFileToGofileRejectsIncompleteBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		readPrefix bool
	}{
		{name: "unread body"},
		{name: "partial body", readPrefix: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "book.epub")
			if err := os.WriteFile(path, []byte("epub-content"), 0o644); err != nil {
				t.Fatal(err)
			}
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				defer func() { _ = req.Body.Close() }()
				if tc.readPrefix {
					var prefix [1]byte
					if _, err := io.ReadFull(req.Body, prefix[:]); err != nil {
						return nil, fmt.Errorf("read body prefix: %w", err)
					}
				}
				// A successful HTTP/JSON response cannot stand in for finishing
				// the multipart stream: this peer did not receive the book.
				return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
			})

			page, err := uploadFileToGofile(t.Context(), "store1", path)
			if page != "" || !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("incomplete upload = (%q, %v), want empty page and closed-pipe error", page, err)
			}
			if err := os.Remove(path); err != nil {
				t.Errorf("book remained open after upload returned: %v", err)
			}
		})
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
	setGofileTestTransport(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

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

func TestGofileRequestsPropagateCancellation(t *testing.T) {
	for _, stage := range []string{"servers", "upload"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			path := filepath.Join(t.TempDir(), "book.epub")
			if err := os.WriteFile(path, []byte("epub-content"), 0o644); err != nil {
				t.Fatal(err)
			}
			setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					// Cancel only once the pipe writer has started. Deliberately
					// leave Body open: the uploader must unblock and join it.
					var prefix [1]byte
					if _, err := io.ReadFull(req.Body, prefix[:]); err != nil {
						return nil, err
					}
				}
				cancel()
				return nil, req.Context().Err()
			})
			var value string
			var err error
			if stage == "servers" {
				value, err = pickGofileServer(ctx)
			} else {
				value, err = uploadFileToGofile(ctx, "store1", path)
			}
			if value != "" || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled request = (%q, %v), want empty value and context.Canceled", value, err)
			}
			if err := os.Remove(path); err != nil {
				t.Errorf("book remained open after cancellation: %v", err)
			}
		})
	}
}

func TestUploadFileToGofileReadFailure(t *testing.T) {
	var copyErr error
	setGofileTestTransport(t, func(req *http.Request) (*http.Response, error) {
		defer func() { _ = req.Body.Close() }()
		_, copyErr = io.Copy(io.Discard, req.Body)
		return gofileTestResponse(req, `{"status":"ok","data":{"downloadPage":"https://gofile.io/d/abc123"}}`), nil
	})
	// Opening a directory succeeds, but reading it as file bytes fails. Even
	// a peer that ignores that failure must not produce a successful share.
	page, err := uploadFileToGofile(t.Context(), "store1", t.TempDir())
	if copyErr == nil {
		t.Fatal("fixture did not exercise a file-read failure in the multipart stream")
	}
	if _, ok := errors.AsType[*os.PathError](err); page != "" || !ok {
		t.Errorf("read failure = (%q, %v), want empty page and the local file error", page, err)
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
	if err := writeMultipartBody(mw, pw, f, "book.epub"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write to closed pipe = %v, want io.ErrClosedPipe", err)
	}
	// Stat on a closed Windows file reports a raw invalid-handle error;
	// a second Close has the portable closed-file result we need to assert.
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("file close after writer returned = %v, want os.ErrClosed", err)
	}
}
