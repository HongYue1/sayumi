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
// The reader frame (src/iframe/frame.ts) is the only consumer of the DOM
// directions here. The app side never builds or resolves a path: it stores
// whatever the frame reports and hands it back opaquely (Read.tsx,
// ChapterFrame.tsx), and compares two stored anchors by block through the
// pure cfiElementPath below (lib/progress.ts). The frame is bundled
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
import { SEARCH_MARK_ATTRIBUTE, SEARCH_MARK_VALUE } from "~/lib/searchMarks";

// Paths are rooted at <body>, so they also encode the frame shell skeleton
// (#paged-clip > #content > #content-inner). Every dynamic body mutation in the
// frame appends (boundary.ts, pagination.ts), which leaves stored paths valid;
// inserting a body child before #paged-clip would invalidate all of them.

// Asked about every element sibling at every depth, in both directions below,
// so it compares the identity itself rather than paying a selector match per
// candidate. This is SEARCH_MARK_SELECTOR by construction — that selector is
// composed from these two constants — and searchMarks.test.ts pins the two
// forms against each other so they cannot drift apart.
function isSearchMark(el: Element): boolean {
  return (
    el.localName === "mark" &&
    el.getAttribute(SEARCH_MARK_ATTRIBUTE) === SEARCH_MARK_VALUE
  );
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
  // ONE walk, not two. The old pre-pass measured the whole element only to
  // clamp the offset into range, but an overshoot already lands on `candidate`
  // below, and keeping only NON-EMPTY nodes as candidates reproduces the
  // "element with no text yields null" rule the total used to enforce. This
  // runs on every anchor restore, so the second walk was pure cost.
  let remaining = Math.max(0, Math.floor(charOffset));
  const walker = el.ownerDocument.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let candidate: Text | null = null;
  let node = walker.nextNode();
  while (node) {
    const text = node as Text;
    const len = text.nodeValue?.length ?? 0;
    // Zero-length nodes cannot hold a caret: never a candidate, and skipped
    // without consuming, so an offset of 0 still lands in real text.
    if (len > 0) {
      candidate = text;
      if (remaining <= len) return { node: text, offset: remaining };
      remaining -= len;
    }
    node = walker.nextNode();
  }
  // Offset ran past the end: the end of the last node that holds text.
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
 * Splits one path segment: the element index plus an optional `:C` suffix.
 * Whether a suffix is legal on *this* segment is the caller's rule -- see
 * parseElementPath, which honours it only on the last. Malformed segments
 * fail closed to null.
 */
function parseSegment(part: string): CfiSegment | null {
  const match = /^(\d+)(?::(\d+))?$/.exec(part);
  if (!match) return null;
  const index = parseInt(match[1], 10);
  if (index < 1) return null;
  if (match[2] === undefined) return { index };
  return { index, offset: parseInt(match[2], 10) };
}

/**
 * Parses a CFI into its element path plus the optional `:C` text offset.
 *
 * Only the last segment may carry an offset: generateCFI appends one to the
 * joined path, and an offset addresses text *inside* the anchor element. A
 * mid-path offset is therefore a value this grammar never mints, so honouring
 * the surrounding indices anyway would resolve a corrupt or foreign anchor to
 * a real-but-unintended element -- the silent wrong answer the rest of this
 * module refuses. Fail closed instead and let the caller fall back to percent.
 *
 * Path and offset are read together because every resolver needs both. Reading
 * them in separate passes meant the offset reader had to restate the path
 * reader's strictness, which is exactly how the two could drift apart.
 */
function parseCfi(
  cfi: string,
): { path: number[]; offset: number | null } | null {
  if (!cfi.startsWith("cfi:")) return null;
  const parts = cfi.slice(4).split("/");
  const lastIndex = parts.length - 1;
  const path: number[] = [];
  let offset: number | null = null;
  for (let i = 0; i <= lastIndex; i++) {
    // Strict integer parse: a malformed or foreign segment (e.g. "3x", "",
    // "1.5") fails to null so callers fall back, rather than parseInt
    // leniently coercing it to a wrong-but-valid index or offset.
    const segment = parseSegment(parts[i]);
    if (!segment) return null;
    if (segment.offset !== undefined) {
      if (i !== lastIndex) return null;
      offset = segment.offset;
    }
    path.push(segment.index);
  }
  return { path, offset };
}

/**
 * The element path of a CFI as a string: the same value with a trailing `:C`
 * text offset removed and nothing else changed. Suffix-less values pass
 * through untouched, which is why this parses the last segment instead of
 * trimming a trailing `:N` -- in a body-level anchor ("cfi:3") that number IS
 * the element index, and trimming it would collapse every top-level block onto
 * one path. Malformed input is returned as-is so comparisons fail closed
 * (different strings, no match) instead of agreeing on a fabricated path.
 *
 * Bookmark identity compares anchors by block (lib/progress.ts), so the rule
 * lives with the grammar that mints the suffix rather than being re-derived.
 */
export function cfiElementPath(cfi: string): string {
  if (!cfi.startsWith("cfi:")) return cfi;
  const parts = cfi.slice(4).split("/");
  const last = parts[parts.length - 1];
  if (last === undefined) return cfi;
  const segment = parseSegment(last);
  if (!segment || segment.offset === undefined) return cfi;
  parts[parts.length - 1] = String(segment.index);
  return `cfi:${parts.join("/")}`;
}

/**
 * Resolves a CFI string back to an element within document.body.
 * Returns null if the CFI is malformed or the element no longer exists.
 * A `:C` suffix is accepted and ignored: element-only callers (bookmarks,
 * same-chapter jumps) keep working on offset-carrying values.
 */
export function resolveCFI(cfi: string, doc: Document): Element | null {
  const parsed = parseCfi(cfi);
  const body = doc.body;
  if (!parsed || !body) return null;
  return elementAtPath(parsed.path, body);
}

/** Walks a parsed element path down from <body>, search marks excluded. */
function elementAtPath(path: number[], body: Element): Element | null {
  let current: Element = body;
  for (const index of path) {
    const child = nthContentChild(current, index);
    if (!child) return null;
    current = child;
  }
  return current;
}

/**
 * Collapsed Range at an element's `:C` offset, or at the element's own start
 * when there is no offset or it maps nowhere (an element with no text).
 */
function collapsedPointIn(
  element: Element,
  offset: number | null,
  doc: Document,
): Range {
  const range = doc.createRange();
  const point = offset === null ? null : textNodeAtOffset(element, offset);
  if (point) range.setStart(point.node, point.offset);
  else range.selectNodeContents(element);
  range.collapse(true);
  return range;
}

/**
 * Resolves a CFI to the addressed element together with a collapsed Range at
 * the point inside it: the `:C` text offset when present and mappable, else
 * the element's start. Returns null when the element is gone, so the caller
 * falls back to percent.
 *
 * Both come from ONE parse and ONE path walk. The restore paths in
 * iframe/frame.ts need both for the same anchor -- the range to land the exact
 * point, the element as the fallback when the range has no measurable rect --
 * and resolving them separately walked the same path two or three times per
 * restore, on the one code path a reader notices as a slow chapter open.
 */
export function resolveCFIPoint(
  cfi: string,
  doc: Document,
): { element: Element; range: Range } | null {
  const parsed = parseCfi(cfi);
  const body = doc.body;
  if (!parsed || !body) return null;
  const element = elementAtPath(parsed.path, body);
  if (!element) return null;
  return { element, range: collapsedPointIn(element, parsed.offset, doc) };
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
