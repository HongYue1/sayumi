package fonts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
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

	mu     sync.RWMutex
	loaded bool
	cache  []Family
}

// Family is a user font family discovered under ./Fonts/<Dir>/.
type Family struct {
	ID       string        `json:"id"`       // "user:<dir>"
	Label    string        `json:"label"`    // display name (family.json or dir name)
	Category string        `json:"category"` // "serif" | "sans-serif"
	Dir      string        `json:"-"`        // on-disk directory name (URL path segment)
	Files    []string      `json:"files"`    // font file names, sorted
	Detected DetectedRoles `json:"detected"` // best-effort role guess for UI pre-fill
}

// DetectedRoles is a filename-heuristic guess of which file fits each role.
// Empty strings mean "no obvious match".
type DetectedRoles struct {
	Regular string `json:"regular"`
	Italic  string `json:"italic"`
	Bold    string `json:"bold"`
}

// familyMeta is the optional ./Fonts/<Dir>/family.json schema.
type familyMeta struct {
	Label    string `json:"label"`
	Category string `json:"category"`
}

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

// Families returns the cached families, scanning lazily on first use.
func (s *Scanner) Families() []Family {
	s.mu.RLock()
	if s.loaded {
		defer s.mu.RUnlock()
		return s.cache
	}
	s.mu.RUnlock()
	return s.Rescan()
}

// Rescan re-reads the fonts directory and replaces the cache.
func (s *Scanner) Rescan() []Family {
	families := s.scan()

	s.mu.Lock()
	s.cache = families
	s.loaded = true
	s.mu.Unlock()

	return families
}

// lookupDir returns the family for an on-disk directory name, if known.
func (s *Scanner) lookupDir(dirName string) (Family, bool) {
	for _, f := range s.Families() {
		if f.Dir == dirName {
			return f, true
		}
	}
	return Family{}, false
}

// ReadUserFont reads a single font file belonging to a discovered family,
// validating both the directory and file against the current scan so that no
// path outside a known family can be served. Returns the bytes and a cheap
// ETag (derived from size + modtime).
func (s *Scanner) ReadUserFont(dirName, file string) (data []byte, etag string, ok bool) {
	if s.dir == "" {
		return nil, "", false
	}
	fam, found := s.lookupDir(dirName)
	if !found {
		return nil, "", false
	}
	known := slices.Contains(fam.Files, file)
	if !known {
		return nil, "", false
	}

	path := filepath.Join(s.dir, dirName, file)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", false
	}
	etag = `"u` + strconv.FormatInt(info.Size(), 16) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 16) + `"`
	return b, etag, true
}

func (s *Scanner) scan() []Family {
	if s.dir == "" {
		return []Family{}
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		// Missing dir is the normal "no user fonts" case; any other error is
		// also treated as best-effort — report no families rather than failing.
		return []Family{}
	}

	families := make([]Family, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		if strings.HasPrefix(dirName, ".") {
			continue
		}
		fam, ok := s.scanFamily(dirName)
		if ok {
			families = append(families, fam)
		}
	}

	sort.Slice(families, func(i, j int) bool {
		return strings.ToLower(families[i].Label) < strings.ToLower(families[j].Label)
	})
	return families
}

func (s *Scanner) scanFamily(dirName string) (Family, bool) {
	famDir := filepath.Join(s.dir, dirName)
	dirEntries, err := os.ReadDir(famDir)
	if err != nil {
		return Family{}, false
	}

	files := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if fontExts[strings.ToLower(filepath.Ext(name))] {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		return Family{}, false
	}
	sort.Strings(files)

	label := dirName
	category := "serif"
	if meta, ok := readFamilyMeta(famDir); ok {
		if strings.TrimSpace(meta.Label) != "" {
			label = strings.TrimSpace(meta.Label)
		}
		if meta.Category == "serif" || meta.Category == "sans-serif" {
			category = meta.Category
		}
	}

	return Family{
		ID:       "user:" + dirName,
		Label:    label,
		Category: category,
		Dir:      dirName,
		Files:    files,
		Detected: detectRoles(files),
	}, true
}

func readFamilyMeta(famDir string) (familyMeta, bool) {
	data, err := os.ReadFile(filepath.Join(famDir, "family.json"))
	if err != nil {
		return familyMeta{}, false
	}
	var meta familyMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return familyMeta{}, false
	}
	return meta, true
}

// detectRoles guesses role→file from filename tokens. Bold-italic and other
// combos are intentionally ignored; the three primary roles are enough to
// pre-fill the UI, which the user can then correct.
func detectRoles(files []string) DetectedRoles {
	var d DetectedRoles
	for _, f := range files {
		lower := strings.ToLower(f)
		isItalic := strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
		isBold := strings.Contains(lower, "bold") || strings.Contains(lower, "-700") || strings.Contains(lower, "semibold")

		switch {
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
			if !strings.Contains(lower, "italic") && !strings.Contains(lower, "oblique") && !strings.Contains(lower, "bold") {
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
