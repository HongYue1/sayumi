package epub

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

type zipBodyProbe struct {
	io.Reader
	closeErr error
	closes   int
}

func (r *zipBodyProbe) Close() error {
	r.closes++
	return r.closeErr
}

type zipReadFailure struct{ err error }

func (r zipReadFailure) Read([]byte) (int, error) { return 0, r.err }

func TestReadLimitedZipBodyOwnsCloseAndErrors(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failure")
	closeErr := errors.New("close failure")
	for _, tt := range []struct {
		name     string
		payload  string
		limit    int64
		readErr  error
		closeErr error
		wantErr  error
		wantText string
	}{
		{name: "success", payload: "abc", limit: 3},
		{name: "zero limit empty", limit: 0},
		{name: "close failure discards data", payload: "abc", limit: 3, closeErr: closeErr, wantErr: closeErr},
		{name: "read failure", payload: "abc", limit: 8, readErr: readErr, wantErr: readErr},
		{name: "read failure wins", payload: "abc", limit: 8, readErr: readErr, closeErr: closeErr, wantErr: readErr},
		{
			name: "oversize wins", payload: "abcd", limit: 3,
			closeErr: closeErr, wantText: "exceeds decompressed size limit",
		},
		{name: "zero limit nonempty", payload: "x", limit: 0, wantText: "exceeds decompressed size limit"},
		{name: "invalid negative limit", payload: "abc", limit: -1, wantText: "invalid zip entry size limit"},
		{
			name: "limit arithmetic overflow", payload: "abc", limit: math.MaxInt64,
			wantText: "invalid zip entry size limit",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var reader io.Reader = strings.NewReader(tt.payload)
			if tt.readErr != nil {
				reader = io.MultiReader(reader, zipReadFailure{err: tt.readErr})
			}
			rc := &zipBodyProbe{Reader: reader, closeErr: tt.closeErr}
			got, err := readLimitedZipBody("entry.txt", rc, tt.limit)
			if rc.closes != 1 {
				t.Errorf("Close called %d times; want 1", rc.closes)
			}
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("error = %v; want wrapped %v", err, tt.wantErr)
				}
			case tt.wantText != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantText) {
					t.Errorf("error = %v; want %q", err, tt.wantText)
				}
			case err != nil || string(got) != tt.payload:
				t.Errorf("body = %q, %v; want %q", got, err, tt.payload)
			}
			if err != nil && got != nil {
				t.Errorf("returned %q alongside error %v", got, err)
			}
		})
	}
}

func TestReadZipEntryArchiveIntegrity(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		mutate func(*zip.File)
		want   error
	}{
		{name: "valid"},
		{name: "checksum", mutate: func(f *zip.File) { f.CRC32 ^= 1 }, want: zip.ErrChecksum},
		{name: "understated size", mutate: func(f *zip.File) { f.UncompressedSize64-- }, want: zip.ErrFormat},
		{name: "overstated size", mutate: func(f *zip.File) { f.UncompressedSize64++ }, want: io.ErrUnexpectedEOF},
		{name: "unsupported method", mutate: func(f *zip.File) { f.Method = 99 }, want: zip.ErrAlgorithm},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := storedZipEntry(t, "content")
			if tt.mutate != nil {
				tt.mutate(f)
			}
			got, err := readZipEntry(f)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v; want %v", err, tt.want)
			}
			if err == nil && string(got) != "content" {
				t.Errorf("body = %q", got)
			}
			if err != nil && got != nil {
				t.Errorf("invalid archive returned data: %q", got)
			}
		})
	}
}

func storedZipEntry(t *testing.T, content string) *zip.File {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "entry.txt", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File[0]
}

func FuzzReadLimitedZipBody(f *testing.F) {
	f.Add([]byte("abc"), uint8(3))
	f.Add([]byte("abcd"), uint8(3))
	f.Add([]byte{}, uint8(0))
	f.Fuzz(func(t *testing.T, payload []byte, limit uint8) {
		rc := &zipBodyProbe{Reader: bytes.NewReader(payload)}
		got, err := readLimitedZipBody("entry.txt", rc, int64(limit))
		if rc.closes != 1 {
			t.Fatalf("Close called %d times", rc.closes)
		}
		if len(payload) > int(limit) {
			if err == nil || got != nil {
				t.Fatalf("oversized body: len=%d, err=%v", len(got), err)
			}
		} else if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("body = %q, %v; want %q", got, err, payload)
		}
	})
}
