package fonts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSanitizeFontRequestPath(t *testing.T) {
	t.Parallel()

	okCases := map[string]string{
		"/Satoshi-Variable.woff2": "Satoshi-Variable.woff2",
		"Satoshi-Variable.woff2":  "Satoshi-Variable.woff2",
	}
	for in, want := range okCases {
		got, ok := sanitizeFontRequestPath(in)
		if !ok || got != want {
			t.Fatalf("sanitizeFontRequestPath(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}

	bad := []string{
		"",
		"/",
		"/../etc/passwd",
		"/foo/bar.woff2",
		`\evil.woff2`,
		"/foo\\bar.woff2",
		"/..",
		".",
	}
	for _, in := range bad {
		if _, ok := sanitizeFontRequestPath(in); ok {
			t.Fatalf("sanitizeFontRequestPath(%q): want reject", in)
		}
	}
}

func TestEtagMatches(t *testing.T) {
	t.Parallel()

	tag := `"abc123"`
	if etagMatches("", tag) {
		t.Fatal("empty header must not match")
	}
	if !etagMatches(tag, tag) {
		t.Fatal("exact match")
	}
	if !etagMatches(` "other" , "abc123" `, tag) {
		t.Fatal("list match with spaces")
	}
	if etagMatches(`"other", "nope"`, tag) {
		t.Fatal("non-match list")
	}
	if etagMatches(`W/"abc123"`, tag) {
		t.Fatal("weak tag should not equal strong quoted tag")
	}
}

func TestHandlerEmbeddedFont(t *testing.T) {
	t.Parallel()

	const name = "Satoshi-Variable.woff2"
	data, ok := fontData[name]
	if !ok || len(data) == 0 {
		t.Fatalf("embedded font %q missing from fontData", name)
	}
	etag, ok := fontETags[name]
	if !ok || etag == "" {
		t.Fatalf("missing etag for %s", name)
	}

	h := Handler(nil)

	// GET
	req := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	rrw := httptest.NewRecorder()
	h.ServeHTTP(rrw, req)
	res := rrw.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != len(data) {
		t.Fatalf("body len = %d, want %d", len(body), len(data))
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "font/woff2") && ct != "application/octet-stream" {
		// ContentTypeByExt returns font/woff2 for .woff2
		if ct == "" {
			t.Fatal("missing Content-Type")
		}
	}
	if res.Header.Get("Content-Length") != strconv.Itoa(len(data)) {
		t.Fatalf("Content-Length = %q", res.Header.Get("Content-Length"))
	}
	if !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	if res.Header.Get("ETag") != etag {
		t.Fatalf("ETag = %q, want %q", res.Header.Get("ETag"), etag)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS ACAO")
	}

	// HEAD
	headReq := httptest.NewRequest(http.MethodHead, "/"+name, nil)
	headRR := httptest.NewRecorder()
	h.ServeHTTP(headRR, headReq)
	headRes := headRR.Result()
	defer func() { _ = headRes.Body.Close() }()
	if headRes.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", headRes.StatusCode)
	}
	headBody, _ := io.ReadAll(headRes.Body)
	if len(headBody) != 0 {
		t.Fatalf("HEAD body len = %d", len(headBody))
	}
	if headRes.Header.Get("Content-Length") != strconv.Itoa(len(data)) {
		t.Fatalf("HEAD Content-Length = %q", headRes.Header.Get("Content-Length"))
	}

	// If-None-Match → 304
	cond := httptest.NewRequest(http.MethodGet, "/"+name, nil)
	cond.Header.Set("If-None-Match", etag)
	condRR := httptest.NewRecorder()
	h.ServeHTTP(condRR, cond)
	condRes := condRR.Result()
	defer func() { _ = condRes.Body.Close() }()
	if condRes.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d", condRes.StatusCode)
	}

	// bad path
	bad := httptest.NewRequest(http.MethodGet, "/../secret.woff2", nil)
	badRR := httptest.NewRecorder()
	h.ServeHTTP(badRR, bad)
	if badRR.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d", badRR.Code)
	}

	// missing font
	miss := httptest.NewRequest(http.MethodGet, "/NoSuchFont.woff2", nil)
	missRR := httptest.NewRecorder()
	h.ServeHTTP(missRR, miss)
	if missRR.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", missRR.Code)
	}

	// POST → 405
	post := httptest.NewRequest(http.MethodPost, "/"+name, nil)
	postRR := httptest.NewRecorder()
	h.ServeHTTP(postRR, post)
	if postRR.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", postRR.Code)
	}

	// OPTIONS → 204
	opt := httptest.NewRequest(http.MethodOptions, "/"+name, nil)
	optRR := httptest.NewRecorder()
	h.ServeHTTP(optRR, opt)
	if optRR.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d", optRR.Code)
	}

	// user path with nil scanner → 404
	user := httptest.NewRequest(http.MethodGet, "/user/Family/Reg.woff2", nil)
	userRR := httptest.NewRecorder()
	h.ServeHTTP(userRR, user)
	if userRR.Code != http.StatusNotFound {
		t.Fatalf("user nil scanner status = %d", userRR.Code)
	}
}
