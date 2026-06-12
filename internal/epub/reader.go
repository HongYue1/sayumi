package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")
	lower := strings.ToLower(name)

	for _, f := range zr.File {
		fName := strings.TrimPrefix(f.Name, "/")
		if fName == name || strings.ToLower(fName) == lower {
			return readZipEntry(f)
		}
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", name)
}

func readZipFileIndexed(index map[string]*zip.File, name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "/")

	if f, ok := index[name]; ok {
		return readZipEntry(f)
	}
	if f, ok := index[strings.ToLower(name)]; ok {
		return readZipEntry(f)
	}
	return nil, fmt.Errorf("file not found in ZIP: %s", name)
}

func readZipEntry(f *zip.File) (data []byte, err error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer func() {
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close zip entry %s: %w", f.Name, cerr)
		}
	}()
	data, err = io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
	}
	return data, nil
}
