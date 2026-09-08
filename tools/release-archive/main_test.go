package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"one"}, {"stage", "out", "-1"}, {"stage", "out", "1.5"}, {"stage", "out", "253402300800"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			if err := run(args); err == nil {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
}

func TestArchivePayloads(t *testing.T) {
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(osName+"/"+arch, func(t *testing.T) {
				t.Parallel()
				stage, output, files := payload(t, osName, arch)
				stamp := time.Unix(1700000001, 0).UTC() // ZIP's odd second must survive via its extended timestamp.
				if err := writeArchive(stage, output, stamp); err != nil {
					t.Fatal(err)
				}
				original := readBytes(t, output)
				got := readArchive(t, output, stamp)
				base := filepath.Base(stage) + "/"
				wantNames := make([]string, 0, 3+len(files))
				wantNames = append(wantNames, base, base+"Fonts/", base+"Fonts/Fixture/")
				for name, content := range files {
					wantNames = append(wantNames, base+name)
					if string(got[base+name]) != content {
						t.Errorf("%s content = %q, want %q", name, got[base+name], content)
					}
				}
				slices.Sort(wantNames)
				if len(got) != len(wantNames) {
					t.Fatalf("unexpected entries: %v", got)
				}
				for _, name := range wantNames {
					if _, ok := got[name]; !ok {
						t.Errorf("missing %s", name)
					}
				}
				// Change actual host metadata, not source contents. Archives must not
				// inherit execute bits from Git/NTFS or the checkout's wall-clock time.
				for name := range files {
					path := filepath.Join(stage, filepath.FromSlash(name))
					if err := os.Chmod(path, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, stamp.Add(time.Hour), stamp.Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
				}
				if err := writeArchive(stage, output, stamp); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(original, readBytes(t, output)) {
					t.Fatal("host metadata changed archive bytes")
				}
			})
		}
	}
}

func TestArchiveEpochs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		osName string
		epoch  int64
		valid  bool
	}{
		{"unix-zero", "linux", 0, true},
		{"zip-too-early", "windows", 315532799, false},
		{"zip-minimum", "windows", 315532800, true},
		{"zip-maximum", "windows", 4294967295, true},
		{"zip-overflow", "windows", 4294967296, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage, output, _ := payload(t, tc.osName, "amd64")
			stamp := time.Unix(tc.epoch, 0).UTC()
			err := writeArchive(stage, output, stamp)
			if (err == nil) != tc.valid {
				t.Fatalf("archive epoch: %v (valid=%t)", err, tc.valid)
			}
			if tc.valid {
				readArchive(t, output, stamp)
			} else if _, err := os.Stat(output); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("invalid epoch left output: %v", err)
			}
		})
	}
}

func TestArchiveRejectsIncompletePayload(t *testing.T) {
	for _, name := range []string{"sayumi", "README.txt", "Fonts/README.txt"} {
		t.Run(name, func(t *testing.T) {
			stage, output, _ := payload(t, "linux", "amd64")
			writeFixture(t, filepath.Join(stage, filepath.FromSlash(name)), "")
			if err := writeArchive(stage, output, time.Unix(1700000000, 0)); err == nil {
				t.Fatal("empty required file accepted")
			}
			if _, err := os.Stat(output); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("incomplete payload left output: %v", err)
			}
		})
	}
}

func TestArchiveRejectsSymlinks(t *testing.T) {
	for _, osName := range []string{"linux", "windows"} {
		t.Run(osName, func(t *testing.T) {
			stage, output, _ := payload(t, osName, "amd64")
			outside := filepath.Join(t.TempDir(), "private-font")
			writeFixture(t, outside, "must not be included")
			if err := os.Symlink(outside, filepath.Join(stage, "Fonts", "escape.woff2")); err != nil {
				t.Fatal(err) // Supported CI hosts must exercise this guard, not skip it.
			}
			if err := writeArchive(stage, output, time.Unix(1700000000, 0)); err == nil {
				t.Fatal("symlink accepted")
			}
			if _, err := os.Stat(output); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("symlink payload left output: %v", err)
			}
		})
	}
}

func TestWriteChecksums(t *testing.T) {
	dir := t.TempDir()
	names := []string{"sayumi-windows-arm64.zip", "sayumi-linux-amd64.tar.gz"}
	for _, name := range names {
		writeFixture(t, filepath.Join(dir, name), name)
	}
	if err := run(append([]string{"checksums", dir}, names...)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x *%s\n%x *%s\n", sha256.Sum256([]byte(names[1])), names[1], sha256.Sum256([]byte(names[0])), names[0])
	if got := string(readBytes(t, filepath.Join(dir, "SHA256SUMS"))); got != want {
		t.Fatalf("checksums = %q, want %q", got, want)
	}
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{"duplicate", []string{names[0], names[0]}},
		{"traversal", []string{"../" + names[0]}},
		{"wrong-format", []string{"sayumi-windows-arm64.tar.gz"}},
		{"missing", []string{"sayumi-linux-arm64.tar.gz"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := writeChecksums(dir, tc.names); err == nil {
				t.Fatal("invalid archive list accepted")
			}
			if string(readBytes(t, filepath.Join(dir, "SHA256SUMS"))) != want {
				t.Fatal("failed checksum generation replaced the prior manifest")
			}
		})
	}
	writeFixture(t, filepath.Join(dir, names[0]), "")
	if err := writeChecksums(dir, names); err == nil {
		t.Fatal("empty archive accepted")
	}
}

func TestAtomicWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive")
	writeFixture(t, path, "existing")
	failure := errors.New("simulated writer failure")
	err := atomicWrite(path, func(w io.Writer) error {
		if _, err := io.WriteString(w, "partial"); err != nil {
			t.Fatal(err)
		}
		return failure
	})
	if !errors.Is(err, failure) || string(readBytes(t, path)) != "existing" {
		t.Fatalf("failed write replaced output: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary output leaked: %v, %v", entries, err)
	}
}

func payload(t *testing.T, osName, arch string) (string, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	base := "sayumi-" + osName + "-" + arch
	stage := filepath.Join(dir, base)
	executable, ext := "sayumi", ".tar.gz"
	if osName == "windows" {
		executable, ext = "sayumi.exe", ".zip"
	}
	files := map[string]string{
		executable:                    "binary fixture\n",
		"README.txt":                  "portable reader\n",
		"Fonts/README.txt":            "font instructions\n",
		"Fonts/Fixture/Regular.woff2": "font fixture\n",
		"Fonts/Fixture/family.json":   "{\"label\":\"Fixture\"}\n",
	}
	for name, content := range files {
		writeFixture(t, filepath.Join(stage, filepath.FromSlash(name)), content)
	}
	return stage, filepath.Join(dir, base+ext), files
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readArchive(t *testing.T, path string, stamp time.Time) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	var order []string
	check := func(name string, mode fs.FileMode, modified time.Time) {
		t.Helper()
		if _, exists := files[name]; exists {
			t.Fatalf("duplicate entry: %s", name)
		}
		order = append(order, name)
		wantMode := fs.FileMode(0o644)
		if strings.HasSuffix(name, "/") || strings.HasSuffix(name, "/sayumi") || strings.HasSuffix(name, "/sayumi.exe") {
			wantMode = 0o755
		}
		if mode.Perm() != wantMode || !modified.Equal(stamp) {
			t.Errorf("%s mode=%o time=%s, want %o %s", name, mode.Perm(), modified, wantMode, stamp)
		}
		if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			t.Fatalf("nonportable entry: %s", name)
		}
	}
	data := readBytes(t, path)
	if strings.HasSuffix(path, ".zip") {
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range r.File {
			check(f.Name, f.Mode(), f.Modified)
			src, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(src)
			if err := errors.Join(readErr, src.Close()); err != nil {
				t.Fatal(err)
			}
			files[f.Name] = content
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := gz.Close(); err != nil {
				t.Error(err)
			}
		}()
		if !gz.ModTime.IsZero() || gz.Name != "" || gz.Comment != "" || gz.OS != 255 {
			t.Fatalf("gzip leaks host metadata: %+v", gz.Header)
		}
		r := tar.NewReader(gz)
		for {
			h, err := r.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			check(h.Name, h.FileInfo().Mode(), h.ModTime)
			if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || !h.AccessTime.IsZero() || !h.ChangeTime.IsZero() {
				t.Fatalf("tar leaks host metadata: %+v", h)
			}
			content, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			files[h.Name] = content
		}
		if _, err := io.Copy(io.Discard, gz); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.IsSorted(order) {
		t.Fatalf("unstable archive order: %v", order)
	}
	return files
}
