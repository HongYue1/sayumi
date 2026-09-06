package fonts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Scanner discovers user-supplied font families dropped into a sibling
// ./Fonts/ directory. Each immediate subdirectory is one family; the font
// files inside it are its faces. An optional family.json supplies a display
// label and category. The scan result is cached and refreshed on demand.
//
// Role assignment (which file is regular/italic/bold) is NOT decided here —
// that is a per-profile preference stored in settings. The scanner only
// reports the available files plus a best-effort detection the UI can use to
// pre-fill the role pickers.
type Scanner struct {
	dir string

	loadMu sync.Mutex // serializes the lazy initial scan (see Families)
	// scanMu serializes every scan-and-publish cycle, including explicit
	// rescans racing lazy initialization. Without it an older-started scan can
	// finish last and overwrite a newer snapshot.
	scanMu sync.Mutex

	mu     sync.RWMutex
	loaded bool
	cache  []Family
	byDir  map[string]familyIndex // dir name -> set of font file names in that family
}

// Family is a user font family discovered under ./Fonts/<Dir>/.
type Family struct {
	ID       string        `json:"id"`       // "user:<dir>"
	Label    string        `json:"label"`    // display name (family.json or dir name)
	Category string        `json:"category"` // "serif" | "sans-serif"
	Dir      string        `json:"-"`        // on-disk directory name (URL path segment)
	Files    []string      `json:"files"`    // font file names, sorted
	Variable bool          `json:"variable"` // variable family: one upright file covers regular+bold, one italic file covers italic+boldItalic
	Detected DetectedRoles `json:"detected"` // best-effort role guess for UI pre-fill
	// Metrics describe the default regular face, from bounded extraction or
	// explicit family.json metadata. Automatic extraction omits missing or
	// implausible x-height; the client can still calibrate rendered glyphs.
	// Nil metadata does not force the family to render at its natural size.
	Metrics *Metrics `json:"metrics,omitempty"`
}

// DetectedRoles is a filename-heuristic guess of which file fits each role.
// Empty strings mean "no obvious match".
type DetectedRoles struct {
	Regular    string `json:"regular"`
	Italic     string `json:"italic"`
	Bold       string `json:"bold"`
	BoldItalic string `json:"boldItalic"`
}

// familyMeta is the optional ./Fonts/<Dir>/family.json schema.
type familyMeta struct {
	Label    string `json:"label"`
	Category string `json:"category"`
	// Variable, when present in family.json, authoritatively marks the family
	// as variable (overriding the looksVariable filename heuristic either way).
	Variable *bool `json:"variable"`
	// Metrics, when present in family.json, replaces what was read out of the
	// font file, the same way Variable overrides the filename heuristic. It is
	// the escape hatch for a face whose OS/2 table stops before the x-height
	// (version 0 and 1 do) or reports one that disagrees with what renders.
	Metrics *Metrics `json:"metrics"`
}

// familyIndex is the set of font file names in one family, keyed for O(1)
// membership. The scanner keeps one per family dir (see Scanner.byDir) so a
// user-font request can validate <dir>/<file> without a linear scan over every
// family and its files. It is rebuilt alongside the cache on each scan.
type familyIndex map[string]struct{}

var fontExts = map[string]bool{
	".woff2": true,
	".woff":  true,
	".ttf":   true,
	".otf":   true,
}

// NewScanner returns a Scanner rooted at dir. dir may be "" (no user fonts).
func NewScanner(dir string) *Scanner {
	return &Scanner{dir: dir}
}

// Families returns the cached families, scanning lazily on first use. The
// returned snapshot, including its slices and metrics, is shared and read-only.
func (s *Scanner) Families() []Family {
	s.mu.RLock()
	if s.loaded {
		cache := s.cache
		s.mu.RUnlock()
		return cache
	}
	s.mu.RUnlock()
	return s.ensureLoaded()
}

// ensureLoaded performs the lazy initial scan at most once even under
// concurrent first requests: loadMu serializes would-be scanners, and the
// double-check means only the first runs Rescan while the rest return the
// freshly populated cache. Explicit Rescan calls bypass loadMu and always
// re-read the directory, while scanMu serializes every scan-and-publish cycle.
func (s *Scanner) ensureLoaded() []Family {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()

	s.mu.RLock()
	if s.loaded {
		cache := s.cache
		s.mu.RUnlock()
		return cache
	}
	s.mu.RUnlock()

	return s.Rescan()
}

// Rescan re-reads the fonts directory and replaces the cache (and its lookup
// index) with a fresh snapshot.
func (s *Scanner) Rescan() []Family {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	families := s.scan()

	byDir := make(map[string]familyIndex, len(families))
	for _, fam := range families {
		fileSet := make(familyIndex, len(fam.Files))
		for _, name := range fam.Files {
			fileSet[name] = struct{}{}
		}
		byDir[fam.Dir] = fileSet
	}

	s.mu.Lock()
	s.cache = families
	s.byDir = byDir
	s.loaded = true
	s.mu.Unlock()

	return families
}

// resolveUserFont validates that dirName/file names a known font file in a
// discovered family and returns the path relative to s.dir. It consults only
// the cached scan via the O(1) byDir index and does not touch the disk.
func (s *Scanner) resolveUserFont(dirName, file string) (relPath string, ok bool) {
	if s.dir == "" {
		return "", false
	}
	s.Families() // ensure the cache and its index are populated (lazy first scan)

	s.mu.RLock()
	files, found := s.byDir[dirName]
	s.mu.RUnlock()
	if !found {
		return "", false
	}
	if _, isFont := files[file]; !isFont {
		return "", false
	}
	return filepath.Join(dirName, file), true
}

// StatUserFont validates the request and returns the size and a cheap ETag
// (size + modtime) without reading the file body, so conditional (If-None-Match)
// and HEAD requests can be served without loading the font. The lookup is
// confined to s.dir via os.Root, so a symlink escaping the fonts directory is
// rejected rather than followed.
func (s *Scanner) StatUserFont(dirName, file string) (size int64, etag string, ok bool) {
	relPath, valid := s.resolveUserFont(dirName, file)
	if !valid {
		return 0, "", false
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = root.Close() }()

	info, err := statFontPath(root, relPath)
	if err != nil || !info.Mode().IsRegular() {
		return 0, "", false
	}
	return info.Size(), userFontETag(info), true
}

// ReadUserFont reads a single font file belonging to a discovered family,
// validating both the directory and file against the current scan so that no
// path outside a known family can be served. The read is confined to s.dir via
// os.Root (symlinks escaping the directory are rejected). Returns the bytes and
// a cheap ETag (derived from size + modtime).
func (s *Scanner) ReadUserFont(dirName, file string) (data []byte, etag string, ok bool) {
	relPath, valid := s.resolveUserFont(dirName, file)
	if !valid {
		return nil, "", false
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, "", false
	}
	defer func() { _ = root.Close() }()

	f, err := openFontPath(root, relPath)
	if err != nil {
		return nil, "", false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", false
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, "", false
	}
	return b, userFontETag(info), true
}

// Go 1.27.0's rooted path resolver can panic on a nested link to ".".
// Treat that bounds failure as an unavailable path at the two os.Root I/O
// boundaries; never retry with an unconfined path or suppress other panics.
func rejectRootPathPanic(err *error) {
	if p := recover(); p != nil {
		e, ok := p.(runtime.Error)
		if !ok || !strings.HasPrefix(e.Error(), "runtime error: index out of range") {
			panic(p)
		}
		*err = fmt.Errorf("resolve font path: %w", e)
	}
}

func statFontPath(root *os.Root, name string) (info os.FileInfo, err error) {
	defer rejectRootPathPanic(&err)
	return root.Stat(name)
}

func openFontPath(root *os.Root, name string) (f *os.File, err error) {
	defer rejectRootPathPanic(&err)
	return root.Open(name)
}

func readFontDir(root *os.Root, name string) ([]fs.DirEntry, error) {
	f, err := openFontPath(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	// Preserve fs.ReadDir's deterministic order, including tied family labels.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, err
}

// userFontETag derives a stable ETag for a user font from its size and
// modification time. Quoted per RFC 7232.
func userFontETag(info os.FileInfo) string {
	return `"u` + strconv.FormatInt(info.Size(), 16) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 16) + `"`
}

func (s *Scanner) scan() []Family {
	if s.dir == "" {
		return []Family{}
	}

	// Discovery must obey the same root boundary as serving: ordinary path
	// reads could disclose metadata or measure fonts through escaping links.
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		// A missing directory is the normal "no user fonts" case and stays
		// silent. Any other error (e.g. permissions) is still best-effort —
		// report no families rather than failing — but log it so it is
		// diagnosable instead of vanishing.
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("scan user fonts dir", "dir", s.dir, "err", err)
		}
		return []Family{}
	}
	defer func() { _ = root.Close() }()
	entries, err := readFontDir(root, ".")
	if err != nil {
		slog.Warn("scan user fonts dir", "dir", s.dir, "err", err)
		return []Family{}
	}

	families := make([]Family, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		if strings.HasPrefix(dirName, ".") || !validFontPathSegment(dirName) {
			continue
		}
		fam, ok := scanFamily(root, dirName)
		if ok {
			families = append(families, fam)
		}
	}

	slices.SortFunc(families, func(a, b Family) int {
		return strings.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label))
	})
	return families
}

func scanFamily(root *os.Root, dirName string) (Family, bool) {
	dirEntries, err := readFontDir(root, dirName)
	if err != nil {
		return Family{}, false
	}

	files := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !validFontPathSegment(name) || !fontExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		if de.Type()&fs.ModeSymlink != 0 {
			// Relative links inside the fonts root remain supported, including
			// links into sibling directories. Never advertise an escaping link.
			info, err := statFontPath(root, filepath.Join(dirName, name))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
		} else if !de.Type().IsRegular() {
			continue
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		return Family{}, false
	}
	slices.Sort(files)

	label := dirName
	category := "serif"
	// A variable family is guessed from filenames; an explicit "variable" in
	// family.json overrides that heuristic either way.
	variable := looksVariable(files)
	var metricsOverride *Metrics
	if meta, ok := readFamilyMeta(root, dirName); ok {
		if strings.TrimSpace(meta.Label) != "" {
			label = strings.TrimSpace(meta.Label)
		}
		if meta.Category == "serif" || meta.Category == "sans-serif" {
			category = meta.Category
		}
		if meta.Variable != nil {
			variable = *meta.Variable
		}
		if meta.Metrics != nil {
			metricsOverride = meta.Metrics
		}
	}

	detected := detectRoles(files)
	if variable {
		detected = applyVariableRoles(detected)
	}

	// Measured from the regular face, because that is the one running text is
	// set in. An explicit "metrics" in family.json wins, as with label,
	// category and variable.
	metrics := metricsOverride
	if metrics == nil {
		metrics = familyMetrics(root, dirName, detected.Regular)
	}

	return Family{
		ID:       "user:" + dirName,
		Label:    label,
		Category: category,
		Dir:      dirName,
		Files:    files,
		Variable: variable,
		Detected: detected,
		Metrics:  metrics,
	}, true
}

// maxFamilyMeta bounds optional JSON independently of font serving and metric
// extraction. Oversized or unreadable metadata falls back to directory defaults.
const maxFamilyMeta = 64 << 10

func readFamilyMeta(root *os.Root, dirName string) (familyMeta, bool) {
	name := filepath.Join(dirName, "family.json")
	info, err := statFontPath(root, name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFamilyMeta {
		return familyMeta{}, false
	}
	f, err := openFontPath(root, name)
	if err != nil {
		return familyMeta{}, false
	}
	defer func() { _ = f.Close() }()

	// Keep the read bounded even if the file grows after stat.
	data, err := io.ReadAll(io.LimitReader(f, maxFamilyMeta+1))
	if err != nil || len(data) > maxFamilyMeta {
		return familyMeta{}, false
	}
	var meta familyMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return familyMeta{}, false
	}
	return meta, true
}

// maxFontFile caps optional metric extraction, not serving. Text faces
// run to tens or hundreds of kilobytes; the cap is what keeps a mistaken drop
// in ./Fonts/ (a video named .woff2) from being pulled into memory whole.
const maxFontFile = 8 << 20

// familyMetrics measures one face when bounded server-side extraction succeeds.
// Failure omits metadata, not the family or its font. The reader can still
// attempt rendered-glyph calibration.
func familyMetrics(root *os.Root, dirName, file string) *Metrics {
	if file == "" {
		return nil
	}
	f, err := openFontPath(root, filepath.Join(dirName, file))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(f, maxFontFile+1))
	if err != nil {
		return nil
	}
	if len(data) > maxFontFile {
		slog.Warn("font too large to measure", "dir", dirName, "file", file, "limit", maxFontFile)
		return nil
	}

	metrics, err := ReadMetrics(data)
	if err != nil {
		// Expected for a file that is not a font, and for containers this does
		// not read (a .ttc holds several faces). Debug, not Warn: dropping a
		// stray file into a family directory is a user habit, not a fault.
		slog.Debug("read font metrics", "dir", dirName, "file", file, "err", err)
		return nil
	}
	if !metrics.Normalizable() {
		slog.Debug("font has no usable x-height", "dir", dirName, "file", file)
		return nil
	}
	return &metrics
}

// looksVariable reports whether the family's filenames look like variable
// fonts (Google Fonts "VariableFont"/"[wght]"/"_wght" naming, or a "-VF"
// suffix). It is only a fallback: an explicit "variable" in family.json wins.
func looksVariable(files []string) bool {
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "variablefont") ||
			strings.Contains(lower, "[wght]") ||
			strings.Contains(lower, "_wght") ||
			strings.Contains(lower, "-vf.") {
			return true
		}
	}
	return false
}

// applyVariableRoles mirrors a variable family's single upright/italic files
// into the bold slots when the user hasn't split them out. A variable file
// carries the whole weight axis, so the upright file also serves bold and the
// italic file also serves bold-italic — this is what lets the reader render a
// real bold instead of synthesizing a faux (fake) bold for a two-file family.
func applyVariableRoles(d DetectedRoles) DetectedRoles {
	if d.Bold == "" {
		d.Bold = d.Regular
	}
	if d.BoldItalic == "" {
		d.BoldItalic = d.Italic
	}
	return d
}

// detectRoles guesses role→file from filename tokens. The four roles
// (regular, italic, bold, bold-italic) are enough to pre-fill the UI, which
// the user can then correct.
func detectRoles(files []string) DetectedRoles {
	var d DetectedRoles
	for _, f := range files {
		lower := strings.ToLower(f)
		isItalic := strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
		isBold := strings.Contains(lower, "bold") || strings.Contains(lower, "-700") || strings.Contains(lower, "semibold")

		switch {
		case isItalic && isBold:
			if d.BoldItalic == "" {
				d.BoldItalic = f
			}
		case isItalic && !isBold:
			if d.Italic == "" {
				d.Italic = f
			}
		case isBold && !isItalic:
			if d.Bold == "" {
				d.Bold = f
			}
		case !isItalic && !isBold:
			if d.Regular == "" && (strings.Contains(lower, "regular") || strings.Contains(lower, "-400") || strings.Contains(lower, "roman") || strings.Contains(lower, "book")) {
				d.Regular = f
			}
		}
	}
	// Fallback: if nothing matched "regular", use the first non-italic/bold file,
	// else the first file overall.
	if d.Regular == "" {
		for _, f := range files {
			lower := strings.ToLower(f)
			if !strings.Contains(lower, "italic") && !strings.Contains(lower, "oblique") &&
				!strings.Contains(lower, "bold") && !strings.Contains(lower, "-700") {
				d.Regular = f
				break
			}
		}
	}
	if d.Regular == "" && len(files) > 0 {
		d.Regular = files[0]
	}
	return d
}
