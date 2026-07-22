package fonts

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDetectRoles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		files                 []string
		regular, italic, bold string
	}{
		{
			name:    "explicit names",
			files:   []string{"Foo-Regular.woff2", "Foo-Italic.woff2", "Foo-Bold.woff2"},
			regular: "Foo-Regular.woff2", italic: "Foo-Italic.woff2", bold: "Foo-Bold.woff2",
		},
		{
			name:    "oblique counts as italic, semibold as bold",
			files:   []string{"X-Roman.otf", "X-Oblique.otf", "X-SemiBold.otf"},
			regular: "X-Roman.otf", italic: "X-Oblique.otf", bold: "X-SemiBold.otf",
		},
		{
			name:    "regular falls back to first non-italic/bold file",
			files:   []string{"Plain.ttf", "Plain-Italic.ttf"},
			regular: "Plain.ttf", italic: "Plain-Italic.ttf", bold: "",
		},
		{
			name:    "single file becomes regular",
			files:   []string{"Only.woff2"},
			regular: "Only.woff2", italic: "", bold: "",
		},
		{
			name:    "weight tokens",
			files:   []string{"Mono-400.woff2", "Mono-700.woff2"},
			regular: "Mono-400.woff2", italic: "", bold: "Mono-700.woff2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := detectRoles(tc.files)
			if got.Regular != tc.regular || got.Italic != tc.italic || got.Bold != tc.bold {
				t.Errorf("detectRoles(%v) = %+v, want regular=%q italic=%q bold=%q",
					tc.files, got, tc.regular, tc.italic, tc.bold)
			}
		})
	}
}

func TestLooksVariable(t *testing.T) {
	t.Parallel()
	variable := [][]string{
		{"Lora-VariableFont_wght.woff2"},
		{"Lora-Italic-VariableFont_wght.woff2", "Lora-VariableFont_wght.woff2"},
		{"Foo[wght].woff2"},
		{"Bar-VF.woff2"},
	}
	for _, files := range variable {
		if !looksVariable(files) {
			t.Errorf("looksVariable(%v) = false, want true", files)
		}
	}
	static := [][]string{
		{"Bookerly-Regular.woff2", "Bookerly-Bold.woff2"},
		{"Minion Pro Regular.woff2", "Minion Pro Bold.woff2"},
	}
	for _, files := range static {
		if looksVariable(files) {
			t.Errorf("looksVariable(%v) = true, want false", files)
		}
	}
}

func TestApplyVariableRoles(t *testing.T) {
	t.Parallel()
	// Two-file variable family: bold mirrors regular, bold-italic mirrors italic.
	d := applyVariableRoles(DetectedRoles{Regular: "Regular.woff2", Italic: "Italic.woff2"})
	if d.Bold != "Regular.woff2" || d.BoldItalic != "Italic.woff2" {
		t.Errorf("mirror = %+v, want bold=Regular.woff2 boldItalic=Italic.woff2", d)
	}
	// Explicit bold / bold-italic files are preserved, not overwritten.
	d2 := applyVariableRoles(DetectedRoles{Regular: "R", Italic: "I", Bold: "B", BoldItalic: "BI"})
	if d2.Bold != "B" || d2.BoldItalic != "BI" {
		t.Errorf("explicit roles overwritten: %+v", d2)
	}
	// Regular-only variable family (e.g. Lexend Deca): bold mirrors regular,
	// italics stay empty (browser synthesizes the rare italic case).
	d3 := applyVariableRoles(DetectedRoles{Regular: "Regular.woff2"})
	if d3.Bold != "Regular.woff2" || d3.Italic != "" || d3.BoldItalic != "" {
		t.Errorf("regular-only mirror = %+v", d3)
	}
}

func TestParseUserFontPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path      string
		dir, file string
		ok        bool
	}{
		{"/user/Minion/Reg.woff2", "Minion", "Reg.woff2", true},
		{"/user/Minion/sub/Reg.woff2", "", "", false}, // nested
		{"/user/../etc/passwd", "", "", false},        // traversal
		{"/user/Minion/", "", "", false},              // no file
		{"/Spectral.woff2", "", "", false},            // embedded path, not user
		{`/user/Minion/..\x`, "", "", false},          // backslash
	}
	for _, tc := range tests {
		dir, file, ok := parseUserFontPath(tc.path)
		if ok != tc.ok || dir != tc.dir || file != tc.file {
			t.Errorf("parseUserFontPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.path, dir, file, ok, tc.dir, tc.file, tc.ok)
		}
	}
}

func writeFontTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func TestScannerEmptyAndMissingRoot(t *testing.T) {
	t.Parallel()

	empty := NewScanner("")
	if got := empty.Families(); got == nil || len(got) != 0 {
		t.Fatalf("empty dir Families = %#v", got)
	}
	if _, _, ok := empty.ReadUserFont("x", "y.woff2"); ok {
		t.Fatal("empty scanner ReadUserFont should fail")
	}

	missing := NewScanner(filepath.Join(t.TempDir(), "no-such-fonts"))
	if got := missing.Families(); got == nil || len(got) != 0 {
		t.Fatalf("missing root Families = %#v", got)
	}
}

func TestScannerDiscoverMetaReadStatRescan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFontTree(t, root, map[string]string{
		"Minion/Reg.woff2":     "REG",
		"Minion/It.woff2":      "IT",
		"Minion/notes.txt":     "ignore",
		"Minion/family.json":   `{"label":"Minion Pro","category":"serif","variable":false}`,
		"EmptyDir/.keep":       "",
		".hidden/Secret.woff2": "nope",
		"BareFile.woff2":       "not-a-family-dir",
	})
	// EmptyDir with only non-font: remove keep and leave empty subdir without fonts
	_ = os.Remove(filepath.Join(root, "EmptyDir", ".keep"))
	_ = os.MkdirAll(filepath.Join(root, "EmptyDir"), 0o755)

	s := NewScanner(root)
	fams := s.Families()
	if len(fams) != 1 {
		t.Fatalf("families = %d, want 1: %+v", len(fams), fams)
	}
	f := fams[0]
	if f.ID != "user:Minion" || f.Dir != "Minion" || f.Label != "Minion Pro" || f.Category != "serif" {
		t.Fatalf("family meta = %+v", f)
	}
	if f.Variable {
		t.Fatal("variable override false not applied")
	}
	if len(f.Files) != 2 || f.Files[0] != "It.woff2" || f.Files[1] != "Reg.woff2" {
		t.Fatalf("files = %v", f.Files)
	}

	// Known file: read + stat.
	data, etag, ok := s.ReadUserFont("Minion", "Reg.woff2")
	if !ok || string(data) != "REG" || etag == "" {
		t.Fatalf("ReadUserFont = %q %q %v", data, etag, ok)
	}
	size, setag, ok := s.StatUserFont("Minion", "Reg.woff2")
	if !ok || size != int64(len("REG")) || setag == "" {
		t.Fatalf("StatUserFont = %d %q %v", size, setag, ok)
	}

	// Unknown paths rejected.
	if _, _, ok := s.ReadUserFont("Minion", "notes.txt"); ok {
		t.Fatal("non-font must not serve")
	}
	if _, _, ok := s.ReadUserFont("Minion", "Missing.woff2"); ok {
		t.Fatal("missing file must not serve")
	}
	if _, _, ok := s.ReadUserFont("Other", "Reg.woff2"); ok {
		t.Fatal("unknown dir must not serve")
	}
	if _, _, ok := s.StatUserFont("Other", "Reg.woff2"); ok {
		t.Fatal("stat unknown dir")
	}

	// Rescan picks up a new family.
	writeFontTree(t, root, map[string]string{
		"Lexend/Lexend-VariableFont_wght.woff2": "VF",
		"Lexend/family.json":                    `{"label":"Lexend","category":"sans-serif"}`,
	})
	fams2 := s.Rescan()
	if len(fams2) != 2 {
		t.Fatalf("after rescan families = %d: %+v", len(fams2), fams2)
	}
	var lexend *Family
	for i := range fams2 {
		if fams2[i].Dir == "Lexend" {
			lexend = &fams2[i]
			break
		}
	}
	if lexend == nil {
		t.Fatal("Lexend not discovered")
	}
	if lexend.Label != "Lexend" || lexend.Category != "sans-serif" || !lexend.Variable {
		t.Fatalf("lexend = %+v", lexend)
	}
	if data, _, ok := s.ReadUserFont("Lexend", "Lexend-VariableFont_wght.woff2"); !ok || string(data) != "VF" {
		t.Fatalf("read lexend = %q %v", data, ok)
	}
}

func TestScannerConcurrentFirstFamilies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFontTree(t, root, map[string]string{
		"A/A-Regular.woff2": "a",
	})
	s := NewScanner(root)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	errs := make(chan string, n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			fams := s.Families()
			if len(fams) != 1 || fams[0].Dir != "A" {
				errs <- "bad families"
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestScannerConcurrentFamiliesAndRescan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFontTree(t, root, map[string]string{
		"A/A-Regular.woff2": "a",
	})
	s := NewScanner(root)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	start := make(chan struct{})
	errs := make(chan string, n)
	for i := range n {
		go func() {
			defer wg.Done()
			<-start
			var fams []Family
			if i%2 == 0 {
				fams = s.Families()
			} else {
				fams = s.Rescan()
			}
			if len(fams) != 1 || fams[0].Dir != "A" {
				errs <- "bad families"
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
