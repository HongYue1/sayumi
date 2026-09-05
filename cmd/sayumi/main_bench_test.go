package main

import "testing"

func BenchmarkHumanizeBytes(b *testing.B) {
	for _, tc := range []struct {
		name string
		n    int64
	}{
		{"empty", 0},
		{"bytes", 512},
		{"kilobytes", 1536},
		{"megabytes", 5 << 20},
		{"gigabytes", 3 << 30},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = humanizeBytes(tc.n)
			}
		})
	}
}
