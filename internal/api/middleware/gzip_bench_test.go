package middleware

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Keep this harness fixed across CP16. It measures the complete middleware
// response, including recorder/header/body allocations and gzip finalization,
// not sockets, disk, browser decoding, or concurrently served requests.
func BenchmarkGzipCP16(b *testing.B) {
	var document bytes.Buffer
	document.WriteString(`{"books":[`)
	for i := range 128 {
		if i != 0 {
			document.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&document, `{"id":%d,"title":"Book %03d: a reader's journey","author":"Author %d","progress":%d}`, i, i, i%17, i%101)
	}
	document.WriteString(`]}`)
	large := document.Bytes()
	small := []byte(`{"ok":true,"count":128}`)
	b.Logf("fixture bytes=%d sha256=%x", len(large), sha256.Sum256(large))

	cases := []struct {
		name   string
		body   []byte
		stream bool
		sniff  bool
		chunks bool
		bypass bool
	}{
		{name: "SmallWrite", body: small},
		{name: "LargeWrite", body: large},
		{name: "ChunkedWrite", body: large, chunks: true},
		{name: "SmallReadFrom", body: small, stream: true},
		{name: "LargeReadFrom", body: large, stream: true},
		{name: "SniffReadFrom", body: large, stream: true, sniff: true},
		{name: "EmptyReadFrom", body: []byte{}, stream: true},
		{name: "NotAccepted", body: large, bypass: true},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			h := Gzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !tc.sniff {
					w.Header().Set("Content-Type", "application/json")
				}
				var err error
				switch {
				case tc.stream:
					_, err = io.Copy(w, benchmarkGzipReader{Reader: bytes.NewReader(tc.body)})
				case tc.chunks:
					_, err = w.Write(tc.body[:32])
					if err == nil {
						_, err = w.Write(tc.body[32:])
					}
				default:
					_, err = w.Write(tc.body)
				}
				if err != nil {
					b.Fatal(err)
				}
			}))
			req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
			if !tc.bypass {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			// Warm the pool and validate the complete stream outside timing. Small
			// streaming bodies intentionally need not use the same encoding before
			// and after the fix; decoded bytes must remain identical.
			check := httptest.NewRecorder()
			h.ServeHTTP(check, req)
			reader := io.Reader(bytes.NewReader(check.Body.Bytes()))
			res := check.Result()
			defer func() { _ = res.Body.Close() }()
			if res.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(reader)
				if err != nil {
					b.Fatal(err)
				}
				defer func() { _ = gz.Close() }()
				reader = gz
			}
			decoded, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(decoded, tc.body) {
				b.Fatalf("fixture round trip: bytes=%d err=%v", len(decoded), err)
			}
			b.ReportAllocs()
			var last *httptest.ResponseRecorder
			for b.Loop() {
				last = httptest.NewRecorder()
				h.ServeHTTP(last, req)
			}
			b.ReportMetric(float64(last.Body.Len()), "wire-B/op")
		})
	}
}

// Hide WriterTo so io.Copy exercises the destination's ReaderFrom contract.
type benchmarkGzipReader struct{ io.Reader }
