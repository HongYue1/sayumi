// release-archive is a build-time helper, not part of the distributed reader.
// Go owns archive metadata so NTFS permissions, BSD/GNU tar differences and
// ambient ZIPOPT/TAR_OPTIONS cannot change a release's layout or executable bit.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var targetName = regexp.MustCompile(`^sayumi-(linux|darwin|windows)-(amd64|arm64)$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release-archive:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) >= 3 && args[0] == "checksums" {
		return writeChecksums(args[1], args[2:])
	}
	if len(args) != 3 {
		return errors.New("usage: release-archive STAGE ARCHIVE EPOCH | checksums OUT ARCHIVE [ARCHIVE ...]")
	}
	epoch, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil || epoch < 0 || epoch > 253402300799 {
		return errors.New("epoch must be an integer between 0 and 253402300799")
	}
	return writeArchive(args[0], args[1], time.Unix(epoch, 0).UTC())
}

type entry struct {
	path string
	name string
	size int64
	mode fs.FileMode
}

func writeArchive(stage, output string, stamp time.Time) (err error) {
	base := filepath.Base(stage)
	if !targetName.MatchString(base) {
		return fmt.Errorf("unsupported payload directory %q", base)
	}
	isZIP := strings.HasPrefix(base, "sayumi-windows-")
	ext, executable := ".tar.gz", "sayumi"
	if isZIP {
		ext, executable = ".zip", "sayumi.exe"
		// ZIP has a DOS date plus a 32-bit unsigned Unix extended timestamp.
		// Reject unrepresentable epochs instead of wrapping or clamping them.
		if stamp.Unix() < 315532800 || stamp.Unix() > 4294967295 {
			return errors.New("ZIP epoch must be between 1980-01-01 and 2106-02-07T06:28:15Z")
		}
	}
	if filepath.Base(output) != base+ext {
		return fmt.Errorf("archive must be named %s%s", base, ext)
	}
	info, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("payload root must be a real directory")
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()

	var entries []entry
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("payload contains a link or special file: %s", path)
		}
		name := base + "/"
		if path != "." {
			name += path
		}
		mode := fs.FileMode(0o644)
		if info.IsDir() {
			mode = fs.ModeDir | 0o755
			if path != "." {
				name += "/"
			}
		} else if path == executable {
			mode = 0o755
		}
		entries = append(entries, entry{path: path, name: name, size: info.Size(), mode: mode})
		return nil
	})
	if err != nil {
		return err
	}
	for _, required := range []string{executable, "README.txt", "Fonts/README.txt"} {
		if !slices.ContainsFunc(entries, func(e entry) bool { return e.path == required && e.mode.IsRegular() && e.size > 0 }) {
			return fmt.Errorf("missing or empty payload file: %s", required)
		}
	}
	return atomicWrite(output, func(out io.Writer) error {
		if isZIP {
			return writeZIP(out, root, entries, stamp)
		}
		return writeTarGzip(out, root, entries, stamp)
	})
}

func writeZIP(out io.Writer, root *os.Root, entries []entry, stamp time.Time) (err error) {
	w := zip.NewWriter(out)
	defer func() { err = errors.Join(err, w.Close()) }()
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: stamp}
		h.SetMode(e.mode)
		if e.mode.IsDir() {
			h.Method = zip.Store
		}
		dst, err := w.CreateHeader(h)
		if err != nil {
			return err
		}
		if !e.mode.IsDir() {
			if err := copyEntry(dst, root, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeTarGzip(out io.Writer, root *os.Root, entries []entry, stamp time.Time) (err error) {
	gz, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	// The gzip header deliberately retains zero time, empty name and OS=255.
	w := tar.NewWriter(gz)
	defer func() { err = errors.Join(err, w.Close(), gz.Close()) }()
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: int64(e.mode.Perm()), Size: e.size, ModTime: stamp, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if e.mode.IsDir() {
			h.Typeflag, h.Size = tar.TypeDir, 0
		}
		if err := w.WriteHeader(h); err != nil {
			return err
		}
		if !e.mode.IsDir() {
			if err := copyEntry(w, root, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyEntry(dst io.Writer, root *os.Root, e entry) (err error) {
	// Root confines reads even if a path changes to an escaping symlink after
	// the walk. Only regular files are allowed, and their size must stay fixed.
	src, err := root.Open(e.path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, src.Close()) }()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != e.size {
		return fmt.Errorf("payload changed while archiving: %s", e.path)
	}
	n, err := io.Copy(dst, src)
	if err != nil {
		return err
	}
	if n != e.size {
		return fmt.Errorf("payload changed while reading: %s", e.path)
	}
	return nil
}

func writeChecksums(dir string, names []string) error {
	names = slices.Clone(names)
	slices.Sort(names)
	var sums strings.Builder
	for i, name := range names {
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".zip")
		ext := ".tar.gz"
		if strings.HasPrefix(base, "sayumi-windows-") {
			ext = ".zip"
		}
		if !targetName.MatchString(base) || name != base+ext || (i > 0 && name == names[i-1]) {
			return fmt.Errorf("invalid or duplicate archive: %q", name)
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("archive must be a nonempty regular file: %s", name)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, f)
		if err := errors.Join(readErr, f.Close()); err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x *%s\n", hash.Sum(nil), name)
	}
	return atomicWrite(filepath.Join(dir, "SHA256SUMS"), func(out io.Writer) error {
		_, err := io.WriteString(out, sums.String())
		return err
	})
}

func atomicWrite(path string, write func(io.Writer) error) error {
	out, err := os.CreateTemp(filepath.Dir(path), ".release-archive-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(out.Name()) }()
	if err := errors.Join(write(out), out.Close()); err != nil {
		return err
	}
	if err := os.Chmod(out.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(out.Name(), path)
}
