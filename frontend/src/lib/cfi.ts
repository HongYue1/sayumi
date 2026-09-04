// Simplified EPUB CFI utilities for position tracking.
//
// We encode element paths within the chapter body using a compact custom
// format: "cfi:N/N/N" where each N is the 1-based index of the element among
// its element siblings at that depth, starting from <body>. Transient search
// marks are excluded from that count (see SEARCH MARK CONTRACT below).
//
// An anchor may carry a text offset suffix — "cfi:N/N:C" — where C counts
// characters in the anchor element's concatenated descendant text (every text
// node in tree order, search-mark contents included: marks wrap the original
// text in place, so the count is identical with the highlight on or off).
// The offset makes the anchor intra-block precise (Foliate-shaped single
// identity); the element path alone stays a valid coarse anchor, so values
// minted before offsets existed keep resolving and callers that only need
// the element keep working. Percent-based position is always the reliable
// fallback when CFI resolution fails.
//
// The reader frame (src/iframe/frame.ts) is the only runtime consumer. The app
// side never builds or resolves a path: it stores whatever the frame reports
// and hands it back opaquely (Read.tsx, ChapterFrame.tsx). The frame is bundled
// (Bun.build, iife) and imports lib modules at runtime. This module stays under
// src/lib because vite.config.ts's frame-graph watch list names it there, and
// it must not import from src/iframe.
//
// SEARCH MARK CONTRACT. Search highlights wrap matched text in
// <mark data-search-mark="sayumi"> nodes that appear and vanish under a
// chapter that is otherwise unchanged. Their identity lives in searchMarks.ts.
// Counting them as siblings shifts every element after the marked text by one,
// so a path minted under a live highlight resolves to a DIFFERENT REAL ELEMENT
// once the highlight is cleared — silently, because a wrong-but-existing
// element yields
// no null for the caller to fall back from. Both directions below therefore
// index as if no search mark were present. Book-authored <mark> elements carry
// no such attribute and are permanent chapter structure, so they still count;
// searchHighlight.ts relies on the same distinction when it unwraps.
// searchMarks.ts also strips authored copies before a chapter becomes ready.
//
import { SEARCH_MARK_SELECTOR } from "~/lib/searchMarks";

// Paths are rooted at <body>, so they also encode the frame shell skeleton
// (#paged-clip > #content > #content-inner). Every dynamic body mutation in the
// frame appends (boundary.ts, pagination.ts), which leaves stored paths valid;
// inserting a body child before #paged-clip would invalidate all of them.

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
 *
 * With `charOffset`, appends the intra-element text suffix (`:C`). The
 * offset counts characters in the element's concatenated descendant text
 * and is clamped into range; a non-finite offset degrades to the plain
 * element path rather than failing, since the caller measured position and
 * the coarse anchor is still the honest fallback.
 */
export function generateCFI(
  el: Element,
  doc: Document,
  charOffset?: number,
): string | null {
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
  const base = "cfi:" + path.join("/");
  if (charOffset === undefined || !Number.isFinite(charOffset)) return base;
  const total = elementTextLength(el);
  const clamped = Math.min(total, Math.max(0, Math.floor(charOffset)));
  return `${base}:${clamped}`;
}

/**
 * Concatenated descendant text length of an element, in the same text space
 * the `:C` suffix addresses: every text node in tree order, including text
 * inside search marks (marks preserve the original text, so the count is
 * highlight-stable by construction).
 */
export function elementTextLength(el: Element): number {
  let total = 0;
  const walker = el.ownerDocument.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let node = walker.nextNode();
  while (node) {
    total += node.nodeValue?.length ?? 0;
    node = walker.nextNode();
  }
  return total;
}

/**
 * Maps a `:C` offset back to the text node and node-relative offset holding
 * it. Offsets past the end clamp to the end of the last text node; an
 * element with no text at all yields null so the caller falls back to the
 * element (or percent) instead of fabricating a point.
 */
export function textNodeAtOffset(
  el: Element,
  charOffset: number,
): { node: Text; offset: number } | null {
  const total = elementTextLength(el);
  if (total <= 0) return null;
  let remaining = Math.min(total, Math.max(0, Math.floor(charOffset)));
  const walker = el.ownerDocument.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let candidate: Text | null = null;
  let node = walker.nextNode();
  while (node) {
    const text = node as Text;
    const len = text.nodeValue?.length ?? 0;
    candidate = text;
    if (remaining < len || (remaining === len && len > 0)) {
      return { node: text, offset: remaining };
    }
    // Zero-length nodes cannot hold a caret: skip without consuming, so an
    // offset of 0 still lands in real text rather than an empty node.
    if (len > 0) remaining -= len;
    node = walker.nextNode();
  }
  // Offset ran past the end (clamped above): the end of the last text node.
  if (candidate)
    return { node: candidate, offset: candidate.nodeValue?.length ?? 0 };
  return null;
}

/** A parsed path segment: element index plus optional text-offset suffix. */
interface CfiSegment {
  index: number;
  offset?: number;
}

/**
 * Splits one path segment, accepting the `:C` suffix on any segment but only
 * honoring it on the last (offsets address text inside the anchor element).
 * Malformed segments fail closed to null.
 */
function parseSegment(part: string): CfiSegment | null {
  const match = /^(\d+)(?::(\d+))?$/.exec(part);
  if (!match) return null;
  const index = parseInt(match[1], 10);
  if (index < 1) return null;
  if (match[2] === undefined) return { index };
  return { index, offset: parseInt(match[2], 10) };
}

/** Element path of a CFI, dropping any `:C` suffix. */
function parseElementPath(cfi: string): number[] | null {
  if (!cfi.startsWith("cfi:")) return null;
  const parts = cfi.slice(4).split("/");
  const path: number[] = [];
  for (const part of parts) {
    const segment = parseSegment(part);
    if (!segment) return null;
    path.push(segment.index);
  }
  return path;
}

/** Text offset of a CFI's last segment, if it carries the `:C` suffix. */
function parseOffsetSuffix(cfi: string): number | null {
  if (!cfi.startsWith("cfi:")) return null;
  const parts = cfi.slice(4).split("/");
  const last = parts[parts.length - 1];
  // Strict integer parse: a malformed/foreign suffix (e.g. "3x", "", "1.5")
  // must fail to null so callers fall back, rather than parseInt leniently
  // coercing it to a wrong-but-valid offset.
  if (last === undefined) return null;
  const segment = parseSegment(last);
  if (!segment) return null;
  return segment.offset ?? null;
}

/**
 * Resolves a CFI string back to an element within document.body.
 * Returns null if the CFI is malformed or the element no longer exists.
 * A `:C` suffix is accepted and ignored: element-only callers (bookmarks,
 * same-chapter jumps) keep working on offset-carrying values.
 */
export function resolveCFI(cfi: string, doc: Document): Element | null {
  const path = parseElementPath(cfi);
  const body = doc.body;
  if (!path || !body) return null;

  let current: Element = body;
  for (const index of path) {
    const child = nthContentChild(current, index);
    if (!child) return null;
    current = child;
  }

  return current;
}

/**
 * Resolves a CFI string to a collapsed Range at the addressed point within
 * document.body: the `:C` text offset when present and mappable, else the
 * start of the resolved element. Returns null when even the element is gone
 * so the caller falls back to percent.
 */
export function resolveCFIRange(cfi: string, doc: Document): Range | null {
  const element = resolveCFI(cfi, doc);
  if (!element) return null;
  const range = doc.createRange();
  const offset = parseOffsetSuffix(cfi);
  if (offset === null) {
    range.selectNodeContents(element);
    range.collapse(true);
    return range;
  }
  const point = textNodeAtOffset(element, offset);
  if (!point) {
    range.selectNodeContents(element);
    range.collapse(true);
    return range;
  }
  range.setStart(point.node, point.offset);
  range.collapse(true);
  return range;
}

/**
 * Viewport rect of a (usually collapsed) range, or null when unmeasurable.
 * Collapsed caret ranges report zero-area but positioned rects — that is the
 * usable case. All-zero means the content isn't laid out (e.g. a display:none
 * subtree): callers fall back to the element, which resolves to the same
 * place for a legit caret at the document origin anyway.
 */
export function rectForCollapsedRange(
  range: Range | null,
): { left: number; top: number; right: number; bottom: number } | null {
  if (!range) return null;
  const rect = range.getBoundingClientRect();
  if (!rect) return null;
  const { left, top, right, bottom } = rect;
  if (![left, top, right, bottom].every(Number.isFinite)) return null;
  if (left === 0 && top === 0 && right === 0 && bottom === 0) return null;
  return { left, top, right, bottom };
}
