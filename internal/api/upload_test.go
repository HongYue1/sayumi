package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func multipartUploadBody(tb testing.TB, filename string, content []byte) ([]byte, string) {
	tb.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("note", "metadata"); err != nil {
		tb.Fatalf("write form field: %v", err)
	}
	part, err := writer.CreateFormFile("epub", filename)
	if err != nil {
		tb.Fatalf("create epub part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		tb.Fatalf("write epub part: %v", err)
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func TestStageMultipartEPUBStreamsFile(t *testing.T) {
	t.Parallel()

	content := []byte("epub bytes")
	body, contentType := multipartUploadBody(t, "Book.EPUB", content)
	req := httptest.NewRequest(http.MethodPost, "/api/books/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	dir := t.TempDir()

	path, filename, ok := stageMultipartEPUB(recorder, req, dir, int64(len(content)))
	if !ok {
		t.Fatalf("stageMultipartEPUB failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove staged file: %v", err)
		}
	})
	if filename != "Book.EPUB" {
		t.Fatalf("filename = %q, want Book.EPUB", filename)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("staged content = %q, want %q", got, content)
	}
}

func TestStageMultipartEPUBRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	body, contentType := multipartUploadBody(t, "book.epub", []byte("123456789"))
	req := httptest.NewRequest(http.MethodPost, "/api/books/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	dir := t.TempDir()

	if path, _, ok := stageMultipartEPUB(recorder, req, dir, 8); ok || path != "" {
		t.Fatalf("oversized upload accepted with path %q", path)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read staging dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized upload left %d staging files", len(entries))
	}
}

// A complete EPUB part is not a complete request. Trailing fields, the final
// boundary, and the epilogue still belong to the same bounded upload.
func TestStageMultipartEPUBValidatesWholeRequest(t *testing.T) {
	t.Parallel()

	const boundary = "sayumi-upload-test"
	const filePart = "--" + boundary + "\r\nContent-Disposition: form-data; name=\"epub\"; filename=\"book.epub\"\r\n\r\nbook\r\n"
	const notePart = "--" + boundary + "\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\n"
	const end = "--" + boundary + "--\r\n"
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "trailing field", body: filePart + notePart + "metadata\r\n" + end},
		{name: "epilogue", body: filePart + end + "ignored epilogue"},
		{name: "unfinished following part", body: filePart + notePart + "truncated", status: http.StatusBadRequest},
		{name: "unfinished file", body: strings.TrimSuffix(filePart, "\r\n"), status: http.StatusBadRequest},
		{name: "oversized following field", body: filePart + notePart + strings.Repeat("x", maxMultipartOverhead) + "\r\n" + end, status: http.StatusRequestEntityTooLarge},
		{name: "oversized epilogue", body: filePart + end + strings.Repeat("x", maxMultipartOverhead), status: http.StatusRequestEntityTooLarge},
		{name: "oversized preceding field", body: notePart + strings.Repeat("x", maxMultipartOverhead) + "\r\n" + filePart + end, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/books/upload", strings.NewReader(tc.body))
			// Exercise the streaming limit, not a Content-Length pre-check.
			req.ContentLength = -1
			req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			recorder := httptest.NewRecorder()
			dir := t.TempDir()
			path, filename, ok := stageMultipartEPUB(recorder, req, dir, 4)
			if tc.status == 0 {
				if !ok || filename != "book.epub" {
					t.Fatalf("valid request rejected: path=%q filename=%q body=%s", path, filename, recorder.Body.String())
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "book" {
					t.Fatalf("staged content = %q, %v", got, err)
				}
				return
			}
			if ok || path != "" || filename != "" || recorder.Code != tc.status {
				t.Errorf("stage = (%q, %q, %v), status = %d, want %d; body=%s", path, filename, ok, recorder.Code, tc.status, recorder.Body.String())
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Errorf("failed upload left staging files: %v, %v", entries, err)
			}
		})
	}
}

func TestStageMultipartEPUBCanceled(t *testing.T) {
	t.Parallel()

	body, contentType := multipartUploadBody(t, "book.epub", []byte("book"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/books/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	dir := t.TempDir()
	path, _, ok := stageMultipartEPUB(recorder, req, dir, 4)
	if ok || path != "" || recorder.Body.Len() != 0 {
		t.Errorf("canceled staging = (%q, %v), response = %s", path, ok, recorder.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("canceled upload left staging files: %v, %v", entries, err)
	}
}

func TestStageMultipartEPUBRejectsInvalidForm(t *testing.T) {
	t.Parallel()

	const boundary = "invalid-upload"
	const prefix = "--" + boundary + "\r\nContent-Disposition: form-data; "
	const suffix = "\r\n\r\nbook\r\n--" + boundary + "--\r\n"
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "wrong content type", contentType: "application/json", body: "{}"},
		{name: "missing boundary", contentType: "multipart/form-data"},
		{name: "missing epub field", body: prefix + "name=\"other\"; filename=\"book.epub\"" + suffix},
		{name: "missing filename", body: prefix + "name=\"epub\"" + suffix},
		{name: "wrong extension", body: prefix + "name=\"epub\"; filename=\"book.txt\"" + suffix},
		{name: "malformed headers", body: "--" + boundary + "\r\nnot a header\r\n\r\nbook"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentType := tc.contentType
			if contentType == "" {
				contentType = "multipart/form-data; boundary=" + boundary
			}
			req := httptest.NewRequest(http.MethodPost, "/api/books/upload", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", contentType)
			recorder := httptest.NewRecorder()
			dir := t.TempDir()
			path, _, ok := stageMultipartEPUB(recorder, req, dir, 4)
			if ok || path != "" || recorder.Code != http.StatusBadRequest {
				t.Errorf("stage = (%q, %v), status=%d body=%s", path, ok, recorder.Code, recorder.Body.String())
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Errorf("invalid form left staging files: %v, %v", entries, err)
			}
		})
	}
}

type uploadCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r uploadCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.cancel()
		return n, context.Canceled
	}
	return n, err
}

func TestStageMultipartEPUBCanceledDuringRead(t *testing.T) {
	t.Parallel()

	body, contentType := multipartUploadBody(t, "book.epub", []byte("book"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Truncate the envelope so cancellation occurs while the staged file is
	// still provisional, not merely after the handler has already returned.
	reader := uploadCancelReader{Reader: bytes.NewReader(body[:len(body)-16]), cancel: cancel}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/books/upload", reader)
	req.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	dir := t.TempDir()
	path, _, ok := stageMultipartEPUB(recorder, req, dir, 4)
	if ok || path != "" || recorder.Body.Len() != 0 || ctx.Err() == nil {
		t.Errorf("canceled staging = (%q, %v), response=%s, context=%v", path, ok, recorder.Body.String(), ctx.Err())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("canceled upload left staging files: %v, %v", entries, err)
	}
}

func TestSanitizeFilenamePortable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "Book.EPUB", want: "Book.epub"},
		{name: "../../Book.epub", want: "Book.epub"},
		{name: "ab:c*?\"<>|\x01.epub", want: "ab_c_______.epub"},
		{name: ".epub", want: "book.epub"},
		{name: "NUL.epub", want: "_NUL.epub"},
		{name: "con.backup.epub", want: "_con.backup.epub"},
		{name: "COM1.epub", want: "_COM1.epub"},
		{name: "...Hidden.epub", want: "Hidden.epub"},
		{name: ".sayumi-upload-owned.epub", want: "sayumi-upload-owned.epub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFilename(tc.name); got != tc.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestExclusiveUploadInstall(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		install func(string, string) error
	}{
		{name: "link or copy", install: linkOrCopyExclusive},
		{name: "copy fallback", install: copyFileExclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.epub")
			other := filepath.Join(dir, "other.epub")
			dest := filepath.Join(dir, "book.epub")
			if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(other, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tc.install(source, dest); err != nil {
				t.Fatal(err)
			}
			if err := tc.install(other, dest); !errors.Is(err, os.ErrExist) {
				t.Errorf("collision = %v, want os.ErrExist", err)
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "original" {
				t.Errorf("existing destination overwritten: %q, %v", got, err)
			}
		})
	}
}

func TestCopyFileExclusiveRemovesFailedCopy(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "partial.epub")
	// Opening a directory may succeed, but reading its bytes must not leave
	// an empty O_EXCL destination behind when the copy fails.
	if err := copyFileExclusive(t.TempDir(), dest); err == nil {
		t.Fatal("copying a directory succeeded")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed copy left a destination: %v", err)
	}
}

func BenchmarkMultipartUploadStaging(b *testing.B) {
	tests := []struct {
		name string
		size int
	}{
		{name: "memory_1MiB", size: 1 << 20},
		{name: "spill_40MiB", size: 40 << 20},
	}
	for _, tc := range tests {
		content := bytes.Repeat([]byte{'x'}, tc.size)
		body, contentType := multipartUploadBody(b, "book.epub", content)

		b.Run(tc.name+"/before_parse_form", func(b *testing.B) {
			benchmarkLegacyMultipartStaging(b, body, contentType, tc.size)
		})
		b.Run(tc.name+"/after_stream_direct", func(b *testing.B) {
			benchmarkStreamingMultipartStaging(b, body, contentType, tc.size)
		})
	}
}

func benchmarkLegacyMultipartStaging(b *testing.B, body []byte, contentType string, size int) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(size))
	dir := b.TempDir()
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/books/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if err := req.ParseMultipartForm(32 << 20); err != nil {
			b.Fatalf("parse multipart form: %v", err)
		}
		file, _, err := req.FormFile("epub")
		if err != nil {
			b.Fatalf("open parsed epub: %v", err)
		}
		out, err := os.CreateTemp(dir, ".legacy-upload-*.epub")
		if err != nil {
			b.Fatalf("create staging file: %v", err)
		}
		_, copyErr := io.Copy(out, file)
		fileCloseErr := file.Close()
		outCloseErr := out.Close()
		removeErr := os.Remove(out.Name())
		formRemoveErr := req.MultipartForm.RemoveAll()
		if copyErr != nil || fileCloseErr != nil || outCloseErr != nil || removeErr != nil || formRemoveErr != nil {
			b.Fatalf(
				"legacy stage errors: copy=%v file_close=%v out_close=%v remove=%v form_remove=%v",
				copyErr,
				fileCloseErr,
				outCloseErr,
				removeErr,
				formRemoveErr,
			)
		}
	}
}

func benchmarkStreamingMultipartStaging(b *testing.B, body []byte, contentType string, size int) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(size))
	dir := b.TempDir()
	b.ResetTimer()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/books/upload", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		recorder := httptest.NewRecorder()
		path, _, ok := stageMultipartEPUB(recorder, req, dir, int64(size))
		if !ok {
			b.Fatalf("stream stage failed: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if err := os.Remove(path); err != nil {
			b.Fatalf("remove streamed staging file: %v", err)
		}
	}
}
