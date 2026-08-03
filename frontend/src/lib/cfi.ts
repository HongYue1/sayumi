// Simplified EPUB CFI utilities for position tracking.
//
// We encode only element paths within the chapter body using a compact
// custom format: "cfi:N/N/N" where each N is the 1-based index of the element
// among its element siblings at that depth, starting from <body>. Transient
// search marks are excluded from that count (see SEARCH MARK CONTRACT below).
//
// Text offsets are intentionally omitted. We find the target element and
// scroll it into view. Percent-based position is always the reliable
// fallback when CFI resolution fails.
//
// The reader frame (src/iframe/frame.ts) is the only runtime consumer. The app
// side never builds or resolves a path: it stores whatever the frame reports
// and hands it back opaquely (Read.tsx, ChapterFrame.tsx). The frame is bundled
// (Bun.build, iife) and can import at runtime, so the copy once inlined in
// frame.ts is gone. This module stays under src/lib because vite.config.ts's
// frame-graph watch list names it there, and it must not import from src/iframe.
//
// SEARCH MARK CONTRACT. Search highlights wrap matched text in
// <mark data-search-mark> nodes (iframe/searchHighlight.ts owns the attribute)
// that appear and vanish under a chapter that is otherwise unchanged. Counting
// them as siblings shifts every element after the marked text by one, so a path
// minted under a live highlight resolves to a DIFFERENT REAL ELEMENT once the
// highlight is cleared — silently, because a wrong-but-existing element yields
// no null for the caller to fall back from. Both directions below therefore
// index as if no search mark were present. Book-authored <mark> elements carry
// no such attribute and are permanent chapter structure, so they still count;
// searchHighlight.ts relies on the same distinction when it unwraps. The
// attribute is duplicated rather than imported to keep src/lib free of
// src/iframe, and searchHighlight.ts carries the matching note.
//
// Paths are rooted at <body>, so they also encode the frame shell skeleton
// (#paged-clip > #content > #content-inner). Every dynamic body mutation in the
// frame appends (boundary.ts, pagination.ts), which leaves stored paths valid;
// inserting a body child before #paged-clip would invalidate all of them.

const SEARCH_MARK_SELECTOR = "mark[data-search-mark]";

function isSearchMark(el: Element): boolean {
  return el.matches(SEARCH_MARK_SELECTOR);
}

/** The index-th element child, counting as if search marks were not there. */
function nthContentChild(parent: Element, index: number): Element | null {
  let seen = 0;
  for (let i = 0; i < parent.children.length; i++) {
    const child = parent.children[i];
    if (isSearchMark(child)) continue;
    seen++;
    if (seen === index) return child;
  }
  return null;
}

/**
 * Generates a CFI string for the given element within document.body.
 * Returns null if the element is not inside body or the path cannot be built.
 */
export function generateCFI(el: Element, doc: Document): string | null {
  const body = doc.body;
  if (!body || !body.contains(el) || el === body) return null;

  const path: number[] = [];
  let current: Element | null = el;

  while (current && current !== body) {
    const parent: Element | null = current.parentElement;
    if (!parent) return null;

    let index = 0;
    let found = false;
    for (let i = 0; i < parent.children.length; i++) {
      const child = parent.children[i];
      if (isSearchMark(child)) continue;
      index++;
      if (child === current) {
        found = true;
        break;
      }
    }
    // Unfound means `el` is itself a search mark: a transient wrapper that must
    // never be addressed. Fail closed so the caller takes the percent path.
    if (!found) return null;

    path.unshift(index);
    current = parent;
  }

  if (path.length === 0) return null;
  return "cfi:" + path.join("/");
}

/**
 * Resolves a CFI string back to an element within document.body.
 * Returns null if the CFI is malformed or the element no longer exists.
 */
export function resolveCFI(cfi: string, doc: Document): Element | null {
  if (!cfi.startsWith("cfi:")) return null;
  const body = doc.body;
  if (!body) return null;

  const parts = cfi.slice(4).split("/");
  let current: Element = body;

  for (const part of parts) {
    // Strict integer parse: a malformed/foreign segment (e.g. "3x", "", "1.5")
    // must fail to null so callers fall back to percent, rather than parseInt
    // leniently coercing it to a wrong-but-valid index. The inlined
    // resolveCFILocal this once mirrored is deleted; frame.ts calls this.
    if (!/^\d+$/.test(part)) return null;
    const index = parseInt(part, 10);
    if (index < 1) return null;
    const child = nthContentChild(current, index);
    if (!child) return null;
    current = child;
  }

  return current;
}
