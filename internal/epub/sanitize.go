package epub

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var stripEntirely = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Noscript: true,
	atom.Base:     true,
}

// isNonStylesheetLink returns true for <link> elements that are not CSS
// stylesheets. Sanitize runs before extractCSS, so real <link rel="stylesheet">
// elements are still present here: they return false and are kept for extractCSS
// to extract and inline afterward. Every other <link> — prefetch, preload, icon,
// etc., or a malformed/rel-less entry — returns true and is stripped to prevent
// unnecessary network fetches under the app origin.
func isNonStylesheetLink(n *html.Node) bool {
	if n.DataAtom != atom.Link {
		return false
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "rel") {
			return !hasToken(a.Val, "stylesheet")
		}
	}
	// No rel attribute — strip it; a bare <link> serves no purpose here.
	return true
}

var stripElement = map[atom.Atom]bool{
	atom.Iframe:   true,
	atom.Object:   true,
	atom.Embed:    true,
	atom.Applet:   true,
	atom.Form:     true,
	atom.Input:    true,
	atom.Textarea: true,
	atom.Select:   true,
	atom.Button:   true,
}

var dangerousURIPrefixes = []string{
	"javascript:",
	"vbscript:",
	"data:text/html",
	"data:application/xhtml+xml",
	"data:application/xml",
	"data:text/javascript",
	"data:application/javascript",
}

const maxSanitizeDepth = 500

func Sanitize(doc *html.Node) {
	sanitizeNode(doc, 0)
}

func sanitizeNode(n *html.Node, depth int) {
	if depth > maxSanitizeDepth {
		// Fail closed: drop the over-depth subtree instead of leaving it in the
		// tree unsanitized. Returning without pruning lets a <script>, on*
		// handler, or javascript:/data: URI nested deeper than maxSanitizeDepth
		// survive untouched, because the per-node element/attribute checks below
		// only run on nodes the recursion actually reaches. The body URL-rewrite
		// pass shares this depth budget, so an un-pruned subtree would also keep
		// its remote subresources un-neutralized.
		removeAllChildren(n)
		return
	}

	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling

		if c.Type == html.ElementNode {
			if stripEntirely[c.DataAtom] {
				n.RemoveChild(c)
				continue
			}

			if isNonStylesheetLink(c) {
				n.RemoveChild(c)
				continue
			}

			if stripElement[c.DataAtom] {
				// Capture the first grandchild before promotion so we can
				// resume the outer loop from the promoted children rather
				// than skipping them entirely. Without this, a <script>
				// inside a <form> would be promoted into the parent but
				// never visited by any sanitizer pass.
				firstGC := c.FirstChild
				for gc := firstGC; gc != nil; {
					nextGC := gc.NextSibling
					c.RemoveChild(gc)
					n.InsertBefore(gc, next)
					gc = nextGC
				}
				n.RemoveChild(c)
				if firstGC != nil {
					next = firstGC
				}
				continue
			}

			if c.DataAtom == atom.Meta {
				removed := false
				for _, a := range c.Attr {
					if strings.EqualFold(a.Key, "http-equiv") {
						n.RemoveChild(c)
						removed = true
						break
					}
				}
				if removed {
					continue
				}
			}

			if c.Data == "svg" || c.DataAtom == atom.Svg {
				// sanitizeSVG recurses through all SVG descendants and
				// also calls sanitizeAttributes on the root element, so
				// no further work is needed here.
				sanitizeSVG(c, depth+1)
				continue
			}

			sanitizeAttributes(c)
		}

		sanitizeNode(c, depth+1)
	}
}

// removeAllChildren detaches every child of n. The depth-limited sanitizer uses
// it to fail closed: once the recursion budget is exhausted the whole
// over-depth subtree is dropped rather than left in place unsanitized.
func removeAllChildren(n *html.Node) {
	for c := n.FirstChild; c != nil; c = n.FirstChild {
		n.RemoveChild(c)
	}
}

func sanitizeAttributes(n *html.Node) {
	clean := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)

		if strings.HasPrefix(key, "on") {
			continue
		}

		if key == "href" || key == "src" || key == "action" || key == "xlink:href" {
			val := normalizeURIForSafetyCheck(a.Val)
			dangerous := false
			for _, prefix := range dangerousURIPrefixes {
				if strings.HasPrefix(val, prefix) {
					dangerous = true
					break
				}
			}
			if dangerous {
				continue
			}
		}

		clean = append(clean, a)
	}
	n.Attr = clean
}

func normalizeURIForSafetyCheck(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

func sanitizeSVG(n *html.Node, depth int) {
	if depth > maxSanitizeDepth {
		// Fail closed, same rationale as sanitizeNode: a <script> or
		// <foreignObject> nested past the budget must not survive inside an
		// over-deep SVG.
		removeAllChildren(n)
		return
	}

	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling

		if c.Type == html.ElementNode {
			lower := strings.ToLower(c.Data)
			if lower == "script" || lower == "foreignobject" {
				n.RemoveChild(c)
				continue
			}
			sanitizeAttributes(c)
			sanitizeSVG(c, depth+1)
		}
	}
	sanitizeAttributes(n)
}
