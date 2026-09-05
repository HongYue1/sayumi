package epub

import (
	"io"
	"testing"
)

func TestNormalizeResourcePath(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "chapter", input: "OEBPS/ch1.xhtml", want: "OEBPS/ch1.xhtml"},
		{name: "image", input: "img/a.png", want: "img/a.png"},
		{name: "dot prefix", input: "./fonts/x.woff2", want: "fonts/x.woff2"},
		{name: "clean separators", input: "  img//./a.png  ", want: "img/a.png"},
		{name: "literal URI escape", input: "img/a%23b.png", want: "img/a%23b.png"},
		{name: "empty", wantErr: true},
		{name: "whitespace", input: "   ", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "backslash", input: `OEBPS\win.xhtml`, wantErr: true},
		{name: "parent", input: "../evil", wantErr: true},
		{name: "escaping parent", input: "a/../../evil", wantErr: true},
		// Reject raw parent segments even when cleaning would stay in-tree.
		{name: "in-tree parent", input: "a/b/../c/d.css", wantErr: true},
		{name: "absolute", input: "/abs/path", wantErr: true},
		{name: "parent only", input: "..", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeResourcePath(tt.input)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("normalizeResourcePath(%q) = %q, %v; want %q, error=%v", tt.input, got, err, tt.want, tt.wantErr)
			}
		})
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
	t.Cleanup(store.Close)

	// Idle book can be closed for replace.
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("idle TryCloseForReplace want true")
	}

	// Hold a ref: replace must refuse.
	_, _, release := openStoreTestEntry(t, store, zipPath)
	if store.TryCloseForReplace(zipPath) {
		t.Fatal("in-use TryCloseForReplace want false")
	}
	release()
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("after release TryCloseForReplace want true")
	}

	// Invalid and missing resources must leave no reference blocking replacement.
	if _, err := store.OpenResource(zipPath, "../evil"); err == nil {
		t.Fatal("traversal: want error")
	}
	if _, err := store.OpenResource(zipPath, "missing.bin"); err == nil {
		t.Fatal("missing: want error")
	}
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("failed resource opens leaked a reference")
	}

	rr, err := store.OpenResource(zipPath, "OEBPS/img.png")
	if err != nil {
		t.Fatalf("OpenResource: %v", err)
	}
	t.Cleanup(func() {
		if err := rr.Close(); err != nil {
			t.Errorf("cleanup resource: %v", err)
		}
	})
	if rr.Size != -1 {
		t.Fatalf("Size = %d, want -1 (untrusted zip size)", rr.Size)
	}
	if rr.ContentType != "image/png" {
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
	if !store.TryCloseForReplace(zipPath) {
		t.Fatal("closed resource still pins the book")
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
