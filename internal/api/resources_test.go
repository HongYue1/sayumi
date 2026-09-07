package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sayumi/internal/epub"
	"sayumi/internal/storage"
)

const (
	resourceTestBookID = "resource-book"
	resourceTestHash   = "resource-hash"
	resourceTestSVG    = `<svg xmlns="http://www.w3.org/2000/svg"><text>book image</text></svg>`
)

func newResourceTestDeps(t *testing.T) (*Dependencies, *profileDeps, storage.BookRecord) {
	t.Helper()

	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	for name, body := range map[string]string{
		"OPS/picture.svg":   resourceTestSVG,
		"OPS/other.svg":     "other image",
		"OPS/style.css":     "body { color: red; }",
		"OPS/font.WOFF2":    "font bytes",
		"OPS/chapter.xhtml": "<html>chapter</html>",
		"OPS/script.js":     "alert('book script')",
		"OPS/data.xml":      "<data/>",
		"OPS/data.bin":      "binary data",
	} {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(filePath, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	pm := NewProfileManager(t.TempDir())
	sessions := newSessionStore(nil)
	for _, name := range []string{"reader", "other"} {
		books, err := storage.NewBookCache(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		pd := &profileDeps{Books: books, Store: epub.NewStore(2)}
		pd.lifetimeCond = sync.NewCond(&pd.lifetimeMu)
		pm.open[name] = pd
		sessions.data[name+"-session"] = session{
			profile: name, expiry: time.Now().Add(time.Hour), verifiedUntil: time.Now().Add(time.Hour),
		}
		t.Cleanup(func() {
			pd.lifetimeMu.Lock()
			refs := pd.refs
			pd.lifetimeMu.Unlock()
			if refs != 0 {
				t.Errorf("%s retained %d profile references", name, refs)
			}
			if pd.bookReplaceMu.TryLock() {
				pd.bookReplaceMu.Unlock()
			} else {
				t.Errorf("%s retained the replacement gate", name)
			}
			if !pd.Store.TryCloseForReplace(filePath) {
				t.Errorf("%s retained a resource reader", name)
			}
			pd.Store.Close()
		})
	}
	book := storage.BookRecord{
		ID: resourceTestBookID, Title: "Resource book", FilePath: filePath, FileHash: resourceTestHash,
	}
	pd := pm.open["reader"]
	pd.Books.Add(book)
	return &Dependencies{ProfileMgr: pm, sessions: sessions}, pd, book
}

func newResourceTestRequest(resourcePath, token, cookie string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/books/"+resourceTestBookID+"/resources/"+resourcePath, nil)
	req.SetPathValue("id", resourceTestBookID)
	req.SetPathValue("path", resourcePath)
	req.URL.RawQuery = url.Values{resourceTokenParam: {token}}.Encode()
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	return req
}

func TestResourceTokenAndETag(t *testing.T) {
	t.Parallel()

	if got := resourceTokenForBook(resourceTestHash); got != resourceTestHash {
		t.Fatalf("resource token = %q, want the book hash", got)
	}
	for _, tc := range []struct {
		name, hash, token string
		valid             bool
	}{
		{name: "valid", hash: "abc", token: "abc", valid: true},
		{name: "same length wrong token", hash: "abc", token: "abd"},
		{name: "different length", hash: "abc", token: "ab"},
		{name: "missing token", hash: "abc"},
		{name: "empty hash and token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validResourceToken(tc.hash, tc.token); got != tc.valid {
				t.Errorf("validResourceToken = %v, want %v", got, tc.valid)
			}
		})
	}
	etag := resourceETag(resourceTestHash, "OPS/picture.svg")
	if etag == resourceETag(resourceTestHash, "OPS/other.svg") || etag == resourceETag("new-hash", "OPS/picture.svg") {
		t.Error("validator does not distinguish resource paths and generations")
	}
	if got := resourceETag(resourceTestHash, `OPS/a"b.svg`); strings.Count(got, `"`) != 2 {
		t.Errorf("quoted resource path broke ETag grammar: %q", got)
	}
}

func TestResourceHandlerAuthentication(t *testing.T) {
	t.Parallel()
	deps, _, _ := newResourceTestDeps(t)

	for _, tc := range []struct {
		name, token, cookie string
		want                int
	}{
		{name: "bearer", token: resourceTestHash, want: http.StatusOK},
		{name: "missing bearer", want: http.StatusUnauthorized},
		{name: "wrong bearer", token: "wrong", want: http.StatusUnauthorized},
		{name: "session without bearer", cookie: "reader-session", want: http.StatusOK},
		{name: "session takes precedence", token: "wrong", cookie: "reader-session", want: http.StatusOK},
		{name: "session cannot cross profiles", token: resourceTestHash, cookie: "other-session", want: http.StatusNotFound},
		{name: "invalid cookie can use bearer", token: resourceTestHash, cookie: "invalid-session", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newResourceTestRequest("OPS/picture.svg", tc.token, tc.cookie)
			req.Header.Set("If-None-Match", `"unrelated"`)
			w := httptest.NewRecorder()
			getResourceHandler(deps)(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK && w.Body.String() != resourceTestSVG {
				t.Errorf("unexpected resource body: %q", w.Body.String())
			}
			if tc.want != http.StatusOK && w.Header().Get("ETag") != "" {
				t.Error("error response exposed a success validator")
			}
			if tc.cookie == "invalid-session" && w.Header().Get("Set-Cookie") == "" {
				t.Error("invalid session cookie was not cleared")
			}
		})
	}
}

func TestResourceHandlerConditionalResponses(t *testing.T) {
	t.Parallel()
	deps, _, _ := newResourceTestDeps(t)
	etag := resourceETag(resourceTestHash, "OPS/picture.svg")

	for _, tc := range []struct {
		name, validator string
		want            int
	}{
		{name: "unconditional", want: http.StatusOK},
		{name: "strong match", validator: etag, want: http.StatusNotModified},
		{name: "weak match", validator: "W/" + etag, want: http.StatusNotModified},
		{name: "wildcard", validator: "*", want: http.StatusNotModified},
		{name: "different resource", validator: resourceETag(resourceTestHash, "OPS/other.svg"), want: http.StatusOK},
		{name: "different generation", validator: resourceETag("old-hash", "OPS/picture.svg"), want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newResourceTestRequest("OPS/picture.svg", resourceTestHash, "")
			req.Header.Set("If-None-Match", tc.validator)
			w := httptest.NewRecorder()
			getResourceHandler(deps)(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if w.Header().Get("ETag") != etag || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" {
				t.Errorf("success cache metadata = %v", w.Header())
			}
			if tc.want == http.StatusNotModified && w.Body.Len() != 0 {
				t.Error("304 response has a body")
			}
			if tc.want == http.StatusOK {
				if w.Body.String() != resourceTestSVG || w.Header().Get("Content-Type") != "image/svg+xml" {
					t.Errorf("incorrect SVG response: headers %v, body %q", w.Header(), w.Body.String())
				}
				if w.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Error("raw SVG navigation protections missing")
				}
			}
		})
	}
}

func TestResourceHandlerConditionalErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path string
		missingZIP bool
		want       int
	}{
		{name: "missing asset", path: "OPS/missing.svg", want: http.StatusNotFound},
		{name: "missing archive", path: "OPS/picture.svg", missingZIP: true, want: http.StatusNotFound},
		{name: "raw chapter", path: "OPS/chapter.xhtml", want: http.StatusForbidden},
		{name: "script", path: "OPS/script.js", want: http.StatusForbidden},
		{name: "XML", path: "OPS/data.xml", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, pd, book := newResourceTestDeps(t)
			if tc.missingZIP {
				book.FilePath = filepath.Join(t.TempDir(), "missing.epub")
				pd.Books.Add(book)
			}
			for _, validator := range []string{"", "*", resourceETag(resourceTestHash, tc.path)} {
				req := newResourceTestRequest(tc.path, resourceTestHash, "")
				req.Header.Set("If-None-Match", validator)
				w := httptest.NewRecorder()
				getResourceHandler(deps)(w, req)
				if w.Code != tc.want {
					t.Errorf("If-None-Match %q: status = %d, want %d", validator, w.Code, tc.want)
				}
				if w.Header().Get("ETag") != "" || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
					t.Errorf("error carries successful-asset cache metadata: %v", w.Header())
				}
			}
		})
	}
}

func TestResourceHandlerStreamingLifetime(t *testing.T) {
	t.Parallel()
	for _, failWrite := range []bool{false, true} {
		name := "success"
		if failWrite {
			name = "disconnected client"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps, pd, book := newResourceTestDeps(t)
			w := &resourceLifetimeWriter{ResponseRecorder: httptest.NewRecorder(), t: t, pd: pd, filePath: book.FilePath, fail: failWrite}
			getResourceHandler(deps)(w, newResourceTestRequest("OPS/picture.svg", resourceTestHash, ""))
			if !w.wrote {
				t.Fatal("resource was not streamed")
			}
		})
	}
}

type resourceLifetimeWriter struct {
	*httptest.ResponseRecorder
	t        *testing.T
	pd       *profileDeps
	filePath string
	fail     bool
	wrote    bool
}

func (w *resourceLifetimeWriter) Write(p []byte) (int, error) {
	w.wrote = true
	if w.pd.bookReplaceMu.TryLock() {
		w.pd.bookReplaceMu.Unlock()
		w.t.Error("replacement gate released while streaming")
	}
	w.pd.lifetimeMu.Lock()
	refs := w.pd.refs
	w.pd.lifetimeMu.Unlock()
	if refs != 1 || w.pd.Store.TryCloseForReplace(w.filePath) {
		w.t.Error("profile or ZIP reference released while streaming")
	}
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func TestAuthorizeResourceRequestPinsGeneration(t *testing.T) {
	t.Parallel()
	for _, cookie := range []string{"", "reader-session"} {
		name := "bearer"
		if cookie != "" {
			name = "session"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			deps, pd, book := newResourceTestDeps(t)
			w := httptest.NewRecorder()
			access, ok := authorizeResourceRequest(deps, w, newResourceTestRequest("OPS/picture.svg", resourceTestHash, cookie), book.ID)
			if !ok || access.pd != pd {
				t.Fatalf("authorization failed: %d %s", w.Code, w.Body.String())
			}
			defer access.pd.release()
			// Check the boundary directly, without sleeps or a probabilistic race:
			// successful authorization must not leave a window for replacement.
			if pd.bookReplaceMu.TryLock() {
				pd.bookReplaceMu.Unlock()
				t.Fatal("book can be replaced after authorization, before its resource is opened")
			}
			defer pd.bookReplaceMu.RUnlock()
			if access.fileHash != book.FileHash || access.filePath != book.FilePath {
				t.Error("authorization returned the wrong generation")
			}
		})
	}
}

func TestResourceHandlerRejectsRetiredTokens(t *testing.T) {
	t.Parallel()
	deps, pd, book := newResourceTestDeps(t)
	pd.bookReplaceMu.Lock()
	book.FileHash = "replacement-hash"
	pd.Books.Add(book)
	pd.bookReplaceMu.Unlock()

	for _, token := range []string{resourceTestHash, book.FileHash} {
		w := httptest.NewRecorder()
		getResourceHandler(deps)(w, newResourceTestRequest("OPS/picture.svg", token, ""))
		want := http.StatusOK
		if token == resourceTestHash {
			want = http.StatusUnauthorized
		}
		if w.Code != want {
			t.Errorf("token %q: status = %d, want %d", token, w.Code, want)
		}
	}
	delete(deps.ProfileMgr.open, "reader")
	w := httptest.NewRecorder()
	getResourceHandler(deps)(w, newResourceTestRequest("OPS/picture.svg", book.FileHash, ""))
	if w.Code != http.StatusNotFound || len(deps.ProfileMgr.open) != 1 {
		t.Error("token request should not reopen an evicted profile")
	}
}

func TestResourceHandlerPreflight(t *testing.T) {
	t.Parallel()
	for _, resourcePath := range []string{"OPS/font.WOFF2", "OPS/picture.svg"} {
		t.Run(resourcePath, func(t *testing.T) {
			req := newResourceTestRequest(resourcePath, "", "")
			req.Method = http.MethodOptions
			w := httptest.NewRecorder()
			getResourceHandler(nil)(w, req)
			if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
				t.Errorf("preflight = %d %q", w.Code, w.Body.String())
			}
			wantOrigin := ""
			if strings.HasSuffix(resourcePath, ".WOFF2") {
				wantOrigin = "*"
			}
			if w.Header().Get("Access-Control-Allow-Origin") != wantOrigin {
				t.Errorf("CORS headers = %v", w.Header())
			}
		})
	}
}

func TestCleanResourcePath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, want string
	}{
		{name: "empty"},
		{name: "dot", path: "."},
		{name: "parent", path: "../secret"},
		{name: "absolute", path: "/secret"},
		{name: "backslash", path: `OPS\secret`},
		{name: "nested", path: "OPS/images/a.svg", want: "OPS/images/a.svg"},
		{name: "internal normalization", path: "OPS/images/../a.svg", want: "OPS/a.svg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cleanResourcePath(tc.path)
			if got != tc.want || ok != (tc.want != "") {
				t.Errorf("cleanResourcePath(%q) = %q, %v; want %q", tc.path, got, ok, tc.want)
			}
		})
	}
}
