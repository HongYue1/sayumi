package fonts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	start := make(chan struct{})
	errs := make(chan string, n)
	for range n {
		wg.Go(func() {
			<-start
			fams := s.Families()
			if len(fams) != 1 || fams[0].Dir != "A" {
				errs <- "bad families"
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestDetectRolesNumericBoldFallback(t *testing.T) {
	t.Parallel()
	files := []string{"Family-700.woff2", "Family-Medium.woff2"}
	got := detectRoles(files)
	if got.Regular != files[1] || got.Bold != files[0] {
		t.Fatalf("numeric bold must not win the non-bold fallback: %+v", got)
	}
}

func TestScannerSymlinkConfinement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"metadata escape", "font escape", "in-root links"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "Fonts")
			face := string(fontData["AtkinsonHyperlegibleNext-VariableFont.woff2"])
			if face == "" {
				t.Fatal("embedded fixture missing")
			}
			writeFontTree(t, parent, map[string]string{
				"outside.woff2":              face,
				"outside.json":               `{"label":"Outside metadata"}`,
				"Fonts/Family/Regular.woff2": face,
				"Fonts/.shared/face.woff2":   face,
				"Fonts/.shared/meta.json":    `{"label":"Inside metadata"}`,
			})
			link := func(target, name string) {
				t.Helper()
				if err := os.Symlink(target, filepath.Join(root, "Family", name)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			switch kind {
			case "metadata escape":
				link("../../outside.json", "family.json")
			case "font escape":
				if err := os.Remove(filepath.Join(root, "Family", "Regular.woff2")); err != nil {
					t.Fatal(err)
				}
				link("../../outside.woff2", "Regular.woff2")
			case "in-root links":
				if err := os.Remove(filepath.Join(root, "Family", "Regular.woff2")); err != nil {
					t.Fatal(err)
				}
				link("../.shared/face.woff2", "Regular.woff2")
				link("../.shared/meta.json", "family.json")
			}
			s := NewScanner(root)
			families := s.Families()
			if kind == "font escape" {
				if len(families) != 0 {
					t.Fatalf("unservable escaping font advertised: %+v", families)
				}
				if _, _, ok := s.ReadUserFont("Family", "Regular.woff2"); ok {
					t.Fatal("escaping font served")
				}
				return
			}
			if len(families) != 1 || families[0].Metrics == nil {
				t.Fatalf("usable in-root family lost: %+v", families)
			}
			wantLabel := "Family"
			if kind == "in-root links" {
				wantLabel = "Inside metadata"
			}
			if families[0].Label != wantLabel {
				t.Fatalf("label = %q, want %q", families[0].Label, wantLabel)
			}
			if data, _, ok := s.ReadUserFont("Family", "Regular.woff2"); !ok || string(data) != face {
				t.Fatal("in-root font must remain readable")
			}
		})
	}
}

func TestScannerMetadataBound(t *testing.T) {
	t.Parallel()
	// A tiny optional config must not read an arbitrarily large dropped file.
	// Use literal boundary sizes so this test constrains the policy itself.
	for _, size := range []int{64 << 10, (64 << 10) + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			meta := `{"label":"Configured"}`
			writeFontTree(t, root, map[string]string{
				"Family/Regular.woff2": "font",
				"Family/family.json":   meta + strings.Repeat(" ", size-len(meta)),
			})
			families := NewScanner(root).Families()
			if len(families) != 1 {
				t.Fatalf("bad metadata must not hide the family: %+v", families)
			}
			want := "Configured"
			if size > 64<<10 {
				want = "Family"
			}
			if families[0].Label != want {
				t.Fatalf("label = %q, want %q", families[0].Label, want)
			}
		})
	}
}

func TestScannerSnapshotAndMembership(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family/Regular.ttf": "old"})
	s := NewScanner(root)
	old := s.Families()
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	writeFontTree(t, root, map[string]string{
		"Family/New.otf":     "new",
		"Family/family.json": `{"label":"Changed"}`,
	})
	if err := os.Remove(filepath.Join(root, "Family", "Regular.ttf")); err != nil {
		t.Fatal(err)
	}
	s.Rescan()
	if _, _, ok := s.ReadUserFont("Family", "Regular.ttf"); ok {
		t.Fatal("removed face stayed in membership")
	}
	if data, _, ok := s.ReadUserFont("Family", "New.otf"); !ok || string(data) != "new" {
		t.Fatal("new face missing from membership")
	}
	now, err := json.Marshal(old)
	if err != nil || string(now) != string(encoded) {
		t.Fatalf("rescan mutated a borrowed snapshot: %s -> %s, %v", encoded, now, err)
	}
	// Readers and explicit rescans share immutable snapshots and lookup maps.
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			for range 12 {
				if i%3 == 0 {
					s.Rescan()
				} else if i%3 == 1 {
					if _, err := json.Marshal(s.Families()); err != nil {
						t.Error(err)
					}
				} else if _, _, ok := s.ReadUserFont("Family", "New.otf"); !ok {
					t.Error("stable face lost during rescan")
				}
			}
		})
	}
	wg.Wait()
}

func TestScannerCachedPathReplacement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "directory", "directory link", "escaping file link", "escaping family link"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "Fonts")
			writeFontTree(t, parent, map[string]string{
				"Fonts/Family/Regular.woff2": "inside",
				"Outside/Regular.woff2":      "outside",
			})
			s := NewScanner(root)
			if len(s.Families()) != 1 {
				t.Fatal("fixture family missing")
			}
			name := filepath.Join(root, "Family", "Regular.woff2")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(name, 0o755); err != nil {
					t.Fatal(err)
				}
			case "directory link":
				if err := os.Symlink(".", name); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "escaping file link":
				if err := os.Symlink("../../Outside/Regular.woff2", name); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "escaping family link":
				family := filepath.Join(root, "Family")
				if err := os.Remove(family); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../Outside", family); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			// No rescan: the old membership remains authorized, but each disk
			// lookup must independently reject a now-unservable representation.
			if _, _, ok := s.StatUserFont("Family", "Regular.woff2"); ok {
				t.Fatal("stat accepted a missing, non-regular, or escaping file")
			}
			if _, _, ok := s.ReadUserFont("Family", "Regular.woff2"); ok {
				t.Fatal("read accepted a missing, non-regular, or escaping file")
			}
		})
	}
}

func TestScannerIgnoresUnservableLinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFontTree(t, root, map[string]string{"Family/Regular.woff2": "font"})
	for _, name := range []string{"Directory.woff2", "Dangling.woff2", "family.json"} {
		target := "."
		if name == "Dangling.woff2" {
			target = "missing.woff2"
		}
		if err := os.Symlink(target, filepath.Join(root, "Family", name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	families := NewScanner(root).Families()
	if len(families) != 1 || len(families[0].Files) != 1 || families[0].Files[0] != "Regular.woff2" {
		t.Fatalf("unservable links advertised: %+v", families)
	}
}

func TestRootPathPanicBoundary(t *testing.T) {
	t.Parallel()
	t.Run("bounds failure becomes an error", func(t *testing.T) {
		t.Parallel()
		causeBoundsPanic := func(values []string) (err error) {
			defer rejectRootPathPanic(&err)
			_ = values[0]
			return nil
		}
		if err := causeBoundsPanic(nil); err == nil {
			t.Fatal("bounds panic was not returned as an error")
		}
	})
	t.Run("unrelated panics are preserved", func(t *testing.T) {
		t.Parallel()
		const marker = "unexpected panic"
		defer func() {
			if p := recover(); p != marker {
				t.Fatalf("panic = %v, want %q", p, marker)
			}
		}()
		_ = func() (err error) {
			defer rejectRootPathPanic(&err)
			panic(marker)
		}()
	})
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
	start := make(chan struct{})
	errs := make(chan string, n)
	for i := range n {
		wg.Go(func() {
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
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
