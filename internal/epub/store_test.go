package epub

import (
	"io"
	"strings"
	"testing"
)

func TestNormalizeResourcePath(t *testing.T) {
	t.Parallel()

	okCases := map[string]string{
		"OEBPS/ch1.xhtml": "OEBPS/ch1.xhtml",
		"img/a.png":       "img/a.png",
		"./fonts/x.woff2": "fonts/x.woff2",
	}
	for in, want := range okCases {
		got, err := normalizeResourcePath(in)
		if err != nil || got != want {
			t.Fatalf("normalizeResourcePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	// Any raw ".." segment is rejected (even if path.Clean would stay in-tree).
	bad := []string{
		"",
		"   ",
		`OEBPS\win.xhtml`,
		"../evil",
		"a/../../evil",
		"a/b/../c/d.css",
		"/abs/path",
		"..",
	}
	for _, in := range bad {
		if _, err := normalizeResourcePath(in); err == nil {
			t.Fatalf("normalizeResourcePath(%q): want error", in)
		}
	}
}

func TestLRUCacheEvictAndDeleteFunc(t *testing.T) {
	t.Parallel()

	c := newLRUCache[string, int](2)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3) // evicts least-recent "a"
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have been evicted")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("b = %d,%v", v, ok)
	}
	// Get touches b; next put should evict c not b.
	c.Put("d", 4)
	if _, ok := c.Get("c"); ok {
		t.Fatal("c should have been evicted after b touch")
	}
	if v, ok := c.Get("b"); !ok || v != 2 {
		t.Fatalf("b kept = %d,%v", v, ok)
	}

	// keep(key)==true retains the entry; keep only "b".
	c.DeleteFunc(func(k string) bool { return k == "b" })
	if _, ok := c.Get("d"); ok {
		t.Fatal("d should be deleted")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b should remain")
	}
	c.Clear()
	if _, ok := c.Get("b"); ok {
		t.Fatal("clear should drop b")
	}
}

func TestTryCloseForReplaceAndOpenResource(t *testing.T) {
	t.Parallel()

	zipPath := writeTestEPUB(t, map[string]string{
		"OEBPS/ch.xhtml": "<html><body>ok</body></html>",
		"OEBPS/img.png":  "PNGDATA",
	})
	store := NewStore(4)
	t.Cleanup(func() { store.Close() })

	// Idle book can be closed for replace.
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("idle TryCloseForReplace want true")
	}

	// Hold a ref: replace must refuse.
	_, _, err := store.OpenIndexed(zipPath)
	if err != nil {
		t.Fatalf("OpenIndexed: %v", err)
	}
	if store.TryCloseForReplace(zipPath) {
		t.Fatal("in-use TryCloseForReplace want false")
	}
	store.Release(zipPath)
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("after release TryCloseForReplace want true")
	}

	// Path traversal rejected; store ref not leaked (subsequent open works).
	if _, err := store.OpenResource(zipPath, "../evil"); err == nil {
		t.Fatal("traversal: want error")
	}
	if _, err := store.OpenResource(zipPath, "missing.bin"); err == nil {
		t.Fatal("missing: want error")
	}

	rr, err := store.OpenResource(zipPath, "OEBPS/img.png")
	if err != nil {
		t.Fatalf("OpenResource: %v", err)
	}
	if rr.Size != -1 {
		t.Fatalf("Size = %d, want -1 (untrusted zip size)", rr.Size)
	}
	if !strings.HasPrefix(rr.ContentType, "image/png") {
		t.Fatalf("ContentType = %q", rr.ContentType)
	}
	body, err := io.ReadAll(rr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "PNGDATA" {
		t.Fatalf("body = %q", body)
	}
	if err := rr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Double-close must not panic or double-release into negative refs.
	if err := rr.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	// Cache seed + EvictBook clears derived text entries.
	store.SetText(zipPath, 0, "orig", "orig")
	if _, _, ok := store.GetText(zipPath, 0); !ok {
		t.Fatal("GetText miss after SetText")
	}
	store.EvictBook(zipPath)
	if _, _, ok := store.GetText(zipPath, 0); ok {
		t.Fatal("GetText hit after EvictBook")
	}
}
