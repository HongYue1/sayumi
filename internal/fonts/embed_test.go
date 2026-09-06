package fonts

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSanitizeFontRequestPath(t *testing.T) {
	t.Parallel()

	okCases := map[string]string{
		"/Fraunces-VariableFont.woff2": "Fraunces-VariableFont.woff2",
		"Fraunces-VariableFont.woff2":  "Fraunces-VariableFont.woff2",
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
	if !etagMatches(`W/"abc123"`, tag) {
		t.Fatal("If-None-Match must use weak comparison")
	}
}

func TestHandlerEmbeddedFont(t *testing.T) {
	t.Parallel()

	const name = "Fraunces-VariableFont.woff2"
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
	if !bytes.Equal(body, data) {
		t.Fatal("embedded body differs from bundled file")
	}
	if ct := res.Header.Get("Content-Type"); ct != "font/woff2" {
		t.Fatalf("Content-Type = %q, want font/woff2", ct)
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

func TestFontConditionalRequests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family/Regular.woff2": "font"})
	s := NewScanner(root)
	_, userTag, ok := s.StatUserFont("Family", "Regular.woff2")
	if !ok {
		t.Fatal("user fixture missing")
	}
	for _, fixture := range []struct{ name, path, tag string }{
		{name: "embedded", path: "/Fraunces-VariableFont.woff2", tag: fontETags["Fraunces-VariableFont.woff2"]},
		{name: "user", path: "/user/Family/Regular.woff2", tag: userTag},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, form := range []string{"strong", "weak", "wildcard", "list", "multiple lines", "miss"} {
				t.Run(fixture.name+"/"+method+"/"+form, func(t *testing.T) {
					t.Parallel()
					req := httptest.NewRequest(method, fixture.path, nil)
					want := http.StatusNotModified
					switch form {
					case "strong":
						req.Header.Set("If-None-Match", fixture.tag)
					case "weak":
						req.Header.Set("If-None-Match", "W/"+fixture.tag)
					case "wildcard":
						req.Header.Set("If-None-Match", "*")
					case "list":
						req.Header.Set("If-None-Match", `"unrelated,opaque", W/`+fixture.tag)
					case "multiple lines":
						req.Header.Add("If-None-Match", `"different"`)
						req.Header.Add("If-None-Match", fixture.tag)
					case "miss":
						req.Header.Set("If-None-Match", `"different"`)
						want = http.StatusOK
					}
					w := httptest.NewRecorder()
					Handler(s).ServeHTTP(w, req)
					if w.Code != want || w.Header().Get("ETag") != fixture.tag {
						t.Fatalf("status/tag = %d/%q, want %d/%q", w.Code, w.Header().Get("ETag"), want, fixture.tag)
					}
					if (want == http.StatusNotModified || method == http.MethodHead) && w.Body.Len() != 0 {
						t.Fatal("body on a bodyless response")
					}
					if want == http.StatusOK && method == http.MethodGet && w.Body.Len() == 0 {
						t.Fatal("conditional miss lost the body")
					}
				})
			}
		}
	}
}

func TestFontLiteralDots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family..Name/Regular..face.woff2": "font"})
	s := NewScanner(root)
	if got := s.Families(); len(got) != 1 {
		t.Fatalf("family missing: %+v", got)
	}
	w := httptest.NewRecorder()
	Handler(s).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/user/Family..Name/Regular..face.woff2", nil))
	if w.Code != http.StatusOK || w.Body.String() != "font" {
		t.Fatalf("listed literal-dot filename is not addressable: %d %q", w.Code, w.Body.String())
	}
}

func TestFontHeadErrors(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/missing.woff2", "/user/Family/Regular.woff2", "/../secret.woff2"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			Handler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodHead, path, nil))
			if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
				t.Fatalf("HEAD error = %d with %d body bytes", w.Code, w.Body.Len())
			}
		})
	}
}

// The response's header boundary lets the test remove a font after conditional
// stat succeeds but before the body read, without sleeps or a production hook.
type disappearingFontWriter struct {
	*httptest.ResponseRecorder
	remove func()
	done   bool
}

func (w *disappearingFontWriter) Header() http.Header {
	h := w.ResponseRecorder.Header()
	if !w.done && h.Get("ETag") != "" {
		w.done = true
		w.remove()
	}
	return h
}

func TestFontDisappearsAfterConditionalStat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family/Regular.woff2": "long font body"})
	w := &disappearingFontWriter{
		ResponseRecorder: httptest.NewRecorder(),
		remove: func() {
			if err := os.Remove(filepath.Join(root, "Family", "Regular.woff2")); err != nil {
				t.Fatal(err)
			}
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/user/Family/Regular.woff2", nil)
	req.Header.Set("If-None-Match", `"different"`)
	Handler(NewScanner(root)).ServeHTTP(w, req)
	if !w.done || w.Code != http.StatusNotFound || w.Body.String() != "not found\n" {
		t.Fatalf("race fixture did not reach the failure: %v %d %q", w.done, w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("failed read inherited successful cache policy: %q", got)
	}
	if w.Header().Get("ETag") != "" || w.Header().Get("Content-Length") != "" {
		t.Fatal("failed read retained successful entity metadata")
	}
}

func TestEtagMatchesSyntax(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, header, tag string
		want              bool
	}{
		{name: "empty", tag: `"abc"`},
		{name: "empty list", header: " , \t, ", tag: `"abc"`},
		{name: "empty members", header: `, , W/"abc",`, tag: `"abc"`, want: true},
		{name: "weak representation", header: `"abc"`, tag: `W/"abc"`, want: true},
		{name: "opaque comma", header: `"a,b"`, tag: `"a,b"`, want: true},
		{name: "opaque comma is not a separator", header: `"a,b"`, tag: `"b"`},
		{name: "backslash is literal", header: `"a\b"`, tag: `"a\b"`, want: true},
		{name: "wildcard whitespace", header: "\t * \t", tag: `"abc"`, want: true},
		{name: "unquoted", header: `abc, "abc"`, tag: `"abc"`},
		{name: "lowercase weak marker", header: `w/"abc"`, tag: `"abc"`},
		{name: "bare weak marker", header: "W/", tag: `"abc"`},
		{name: "unterminated", header: `"abc`, tag: `"abc"`},
		{name: "invalid delimiter", header: `"abc"junk`, tag: `"abc"`},
		{name: "space inside opaque tag", header: `"bad tag", "abc"`, tag: `"abc"`},
		{name: "control inside opaque tag", header: "\"bad\x7ftag\", \"abc\"", tag: `"abc"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := etagMatches(tc.header, tc.tag); got != tc.want {
				t.Fatalf("etagMatches(%q, %q) = %v, want %v", tc.header, tc.tag, got, tc.want)
			}
		})
	}
}

func TestFontWildcardRequiresExistingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family/Regular.woff2": "font"})
	s := NewScanner(root)
	s.Families()
	if err := os.Remove(filepath.Join(root, "Family", "Regular.woff2")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/missing.woff2", "/user/Family/Regular.woff2"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+path, func(t *testing.T) {
				t.Parallel()
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("If-None-Match", "*")
				w := httptest.NewRecorder()
				Handler(s).ServeHTTP(w, r)
				if w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("missing representation = %d, headers %v", w.Code, w.Header())
				}
				if w.Header().Get("ETag") != "" || w.Header().Get("Content-Length") != "" {
					t.Fatal("missing representation retained entity metadata")
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatal("HEAD error included a body")
				}
			})
		}
	}
}

func TestFontEncodedSegments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const dir = "Family + #%'..雪"
	const file = "Regular%2f + #'.woff2"
	writeFontTree(t, root, map[string]string{dir + "/" + file: "font"})
	s := NewScanner(root)
	if got := s.Families(); len(got) != 1 || got[0].Files[0] != file {
		t.Fatalf("literal names lost during discovery: %+v", got)
	}
	w := httptest.NewRecorder()
	target := "/user/" + url.PathEscape(dir) + "/" + url.PathEscape(file)
	Handler(s).ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	if w.Code != http.StatusOK || w.Body.String() != "font" {
		t.Fatalf("encoded segments = %d %q", w.Code, w.Body.String())
	}
}

func FuzzFontETagWeakComparison(f *testing.F) {
	for _, seed := range []string{"abc", "", "with,comma", "slash\\tag", "\u00e9"} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, raw string, weak bool) {
		if len(raw) > 512 {
			t.Skip()
		}
		opaque := strings.Map(func(r rune) rune {
			if r < 0x21 || r == '"' || r == 0x7f {
				return -1
			}
			return r
		}, raw)
		tag := `"` + opaque + `"`
		requestTag := tag
		if weak {
			requestTag = "W/" + requestTag
		}
		if !etagMatches(requestTag, tag) || !etagMatches(`"not-this", `+requestTag, tag) {
			t.Fatal("valid entity tag did not weak-match")
		}
		if etagMatches(requestTag, `"different-`+opaque+`"`) {
			t.Fatal("different opaque tags matched")
		}
	})
}
