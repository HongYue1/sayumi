package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// gofileServerNamePattern guards against SSRF: the server name returned by the
// gofile API is interpolated into the upload URL host, so it must be a bare DNS
// label (letters, digits, hyphens only) before we build
// https://<server>.gofile.io. A value containing ".", "/", "@", or "%" could
// otherwise redirect the upload to an attacker-controlled host.
var gofileServerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

// gofileClient is the dedicated HTTP client for outbound uploads to gofile.io —
// the only outbound network egress in an otherwise local-first app. The timeout
// is generous (a 100 MB book over a slow link) but finite so a hung remote
// cannot pin a goroutine and an open file handle indefinitely.
var gofileClient = &http.Client{Timeout: 30 * time.Minute}

const gofileServersURL = "https://api.gofile.io/servers"

type gofileServersResponse struct {
	Status string `json:"status"`
	Data   struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	} `json:"data"`
}

type gofileUploadResponse struct {
	Status string `json:"status"`
	Data   struct {
		DownloadPage string `json:"downloadPage"`
	} `json:"data"`
}

// uploadGofileHandler handles POST /api/books/{id}/gofile: streams the book's
// EPUB to gofile.io as an ANONYMOUS upload (no account/token, per design) and
// returns the public download page.
//
// This is Sayumi's only outbound network egress; the resulting link is public
// to anyone who holds it. The UI states this before the user triggers it.
func uploadGofileHandler(_ *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pd := requireProfileDeps(w, r)
		if pd == nil {
			return
		}

		id := r.PathValue("id")
		book, ok := pd.Books.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}
		if book.FilePath == "" {
			writeError(w, http.StatusNotFound, "no_file", "book has no file on disk")
			return
		}

		// A large upload over a slow link can outlast the server WriteTimeout armed
		// at header-read time; clear our write deadline for this handler.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			slog.Debug("clear gofile write deadline unsupported", "err", err)
		}

		server, err := pickGofileServer(r.Context())
		if err != nil {
			slog.Error("gofile pick server failed", "book", id, "err", err)
			writeError(w, http.StatusBadGateway, "gofile_error", "could not reach gofile")
			return
		}

		// Re-enter the EPUB generation gate after the remote server lookup and
		// refresh the cache snapshot inside it. This keeps the opened file alive
		// and generation-consistent for the whole stream without holding the lock
		// during the preliminary network request.
		pd.bookReplaceMu.RLock()
		book, ok = pd.Books.Get(id)
		if !ok {
			pd.bookReplaceMu.RUnlock()
			writeError(w, http.StatusNotFound, "not_found", "book not found")
			return
		}
		if book.FilePath == "" {
			pd.bookReplaceMu.RUnlock()
			writeError(w, http.StatusNotFound, "no_file", "book has no file on disk")
			return
		}
		downloadPage, err := uploadFileToGofile(r.Context(), server, book.FilePath)
		pd.bookReplaceMu.RUnlock()
		if err != nil {
			slog.Error("gofile upload failed", "book", id, "err", err)
			writeError(w, http.StatusBadGateway, "gofile_error", "upload to gofile failed")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"downloadPage": downloadPage})
	}
}

// validateGofileDownloadPage confines third-party response data to a public
// HTTPS page on gofile.io before the value is rendered as a clickable link.
func validateGofileDownloadPage(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("parse download page: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Port() != "" {
		return errors.New("download page must be credential-free HTTPS on the default port")
	}
	host := strings.ToLower(u.Hostname())
	if host == "gofile.io" {
		return nil
	}
	subdomain, ok := strings.CutSuffix(host, ".gofile.io")
	if !ok || subdomain == "" {
		return fmt.Errorf("download page host %q is not gofile.io", host)
	}
	for label := range strings.SplitSeq(subdomain, ".") {
		if !gofileServerNamePattern.MatchString(label) {
			return fmt.Errorf("download page host %q is not a valid gofile.io subdomain", host)
		}
	}
	return nil
}

// pickGofileServer asks the gofile API for an upload server and returns its
// name after validating it is a bare DNS label (SSRF defense).
func pickGofileServer(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gofileServersURL, nil)
	if err != nil {
		return "", fmt.Errorf("build servers request: %w", err)
	}
	resp, err := gofileClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get gofile servers: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gofile servers status %d", resp.StatusCode)
	}

	var parsed gofileServersResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode gofile servers: %w", err)
	}
	if parsed.Status != "ok" || len(parsed.Data.Servers) == 0 {
		return "", fmt.Errorf("gofile servers unavailable (status %q)", parsed.Status)
	}

	server := parsed.Data.Servers[0].Name
	if !gofileServerNamePattern.MatchString(server) {
		return "", fmt.Errorf("gofile server name %q is not a bare label", server)
	}
	return server, nil
}

// uploadFileToGofile streams filePath to https://<server>.gofile.io/uploadFile
// as an anonymous multipart upload and returns the download page URL. The file
// is streamed through an io.Pipe so a large EPUB is never fully buffered in
// memory. No token/folderId is sent (anonymous upload).
func uploadFileToGofile(ctx context.Context, server, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open book file: %w", err)
	}
	defer func() { _ = file.Close() }()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Copy the file into the multipart body on a goroutine; the request reads
	// from the pipe as it sends, so the body is never fully buffered. Any write
	// failure is propagated to the reader via CloseWithError so Do() returns an
	// error instead of hanging.
	go func() {
		part, err := mw.CreateFormFile("file", filepath.Base(filePath))
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("copy book to gofile: %w", err))
			return
		}
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("close multipart writer: %w", err))
			return
		}
		_ = pw.Close()
	}()

	uploadURL := fmt.Sprintf("https://%s.gofile.io/uploadFile", server)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		return "", fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := gofileClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post to gofile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gofile upload status %d", resp.StatusCode)
	}

	var parsed gofileUploadResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode gofile upload: %w", err)
	}
	if parsed.Status != "ok" || parsed.Data.DownloadPage == "" {
		return "", fmt.Errorf("gofile upload rejected (status %q)", parsed.Status)
	}
	if err := validateGofileDownloadPage(parsed.Data.DownloadPage); err != nil {
		return "", fmt.Errorf("invalid gofile download page: %w", err)
	}
	return parsed.Data.DownloadPage, nil
}
