package epub

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// BenchmarkSanitizeParse measures a fresh parse plus in-place sanitization, not
// repeated passes over an already-clean tree. Parsing and allocation are part of
// this boundary; this is not an isolated walker or end-to-end chapter benchmark.
// Fixtures and output validation stay outside timing. Keep the whole harness
// unchanged across paired executables, including its private helpers.
func BenchmarkSanitizeParse(b *testing.B) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{
			name: "Plain",
			input: `<html><body>` + strings.Repeat(
				`<p class="book">A quiet page with <a href="Text/ch.xhtml#part">a link</a>.</p>`, 128,
			) + `</body></html>`,
		},
		{
			name: "SVG",
			input: `<html><body><svg viewBox="0 0 640 480">` + strings.Repeat(
				`<g class="diagram"><a href="Text/Chapter.xhtml#Part"><text x="10" y="20">Label</text></a>`+
					`<rect width="10" height="20"><animate attributeName="width" values="10;20"></animate></rect>`+
					`<image xlink:href="Images/Picture.PNG"></image></g>`, 64,
			) + `</svg></body></html>`,
		},
		{
			name: "Hostile",
			input: `<html><body>` + strings.Repeat(
				`<form>before<button><span onclick="drop()">keep</span><script>drop()</script></button>after</form>`+
					`<svg onload="drop()"><a href="java&#10;script:drop()">`+
					`<set attributeName="href" to="javascript:drop()"></set>link</a>`+
					`<foreignObject>drop</foreignObject></svg>`, 64,
			) + `</body></html>`,
		},
		{
			name: "DepthBoundary",
			// Document, html and body put the last div at depth 500 and
			// its SVG child at 501. Keep the literal fixture size frozen.
			input: `<html><body>` + strings.Repeat(`<div>`, 498) +
				`<svg id="edge" onload="drop()"><g>prune</g></svg>` +
				strings.Repeat(`</div>`, 498) + `<p>outside</p></body></html>`,
		},
	} {
		b.Run(tc.name, func(b *testing.B) {
			warm, err := html.Parse(strings.NewReader(tc.input))
			if err != nil {
				b.Fatal(err)
			}
			Sanitize(warm)
			want := renderSanitizeBenchmark(b, warm)
			var got *html.Node
			b.SetBytes(int64(len(tc.input)))
			b.ReportAllocs()
			for b.Loop() {
				got, err = html.Parse(strings.NewReader(tc.input))
				if err != nil {
					b.Fatal(err)
				}
				Sanitize(got)
			}
			// The regression tests, not this stability check, define safety.
			// Before/after may intentionally serialize the boundary differently.
			if out := renderSanitizeBenchmark(b, got); out != want {
				b.Fatal("sanitized output changed across iterations")
			}
		})
	}
}

func renderSanitizeBenchmark(b *testing.B, doc *html.Node) string {
	b.Helper()
	var out strings.Builder
	if err := html.Render(&out, doc); err != nil {
		b.Fatal(err)
	}
	return out.String()
}
