package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
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
