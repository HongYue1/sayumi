// In-frame search-match highlighting.
//
// Extracted from frame.ts as the first stateful module of the #3 frame split.
// Builds a flattened, whitespace-collapsed character index of the chapter body
// so a (charOffset, matchLen) pair from the parent search can be mapped back to
// a DOM Range, wraps that range in a <mark>, and scrolls or pages it into view.
// Falls back to a direct case-insensitive substring search when the indexed
// mapping fails.
//
// This module owns no frame state of its own (only the <mark> nodes live in the
// document). Cross-cutting frame concerns — the content element, paged mode and
// pagination — are injected via SearchHighlightDeps so the module stays
// decoupled from frame.ts's module-level variables.
//
// OFFSET CONTRACT. This index must agree code point for code point with the
// backend extractor in internal/epub/search.go, which produces every charOffset
// consumed here; one extra or missing character silently highlights the wrong
// words from that point on. The boundary list and whitespace rules below are a
// single contract maintained on both sides. It also has to absorb one
// asymmetry: the backend extracts from the raw chapter HTML while this module
// indexes the sanitized DOM, so an element internal/epub/sanitize.go unwraps
// must not be a boundary on either side (see TEXT_BOUNDARY_TAGS).

import { prefersReducedMotion } from "./reduceMotion";

export interface SearchHighlightDeps {
  /** The live chapter content element (#content); null before the shell exists. */
  getContentEl: () => HTMLElement | null;
  /** Whether a chapter is currently loaded and ready to highlight. */
  isContentReady: () => boolean;
  /** Whether the reader is in a paged (vs scroll) layout. */
  isPagedMode: () => boolean;
  /** Navigate to the page containing the given element (paged mode). */
  goToPageInternal: (page: number, animated: boolean) => void;
  /** The page index that contains the given element (paged mode). */
  getElementPageIndex: (el: Element) => number;
}

export interface SearchHighlighter {
  highlightSearchMatch: (
    charOffset: number,
    matchLen: number,
    query: string,
  ) => void;
  clearSearchHighlights: () => void;
}

// Mirrors isTextBoundaryElement in internal/epub/search.go. Two absences are
// deliberate and load-bearing:
//   FORM   — sanitize.go unwraps <form>, promoting its children, before the
//            chapter reaches this frame, so no form element survives here to
//            be scored. The backend list drops it for the same reason.
//   SVG /  — Go's parser resolves both to real atoms (atom.Svg, atom.Math)
//   MATH     that its switch does not list, so the backend scores no boundary;
//            browsers report tagName "svg"/"math" from their own namespaces,
//            which this Set never matches either. A case for them would create
//            the drift it looks like it prevents. (happy-dom reports "MATH"
//            uppercase, so a MathML fixture must assert through localName.)
const TEXT_BOUNDARY_TAGS = new Set([
  "ADDRESS",
  "ARTICLE",
  "ASIDE",
  "BLOCKQUOTE",
  "CAPTION",
  "DD",
  "DIV",
  "DL",
  "DT",
  "FIGCAPTION",
  "FIGURE",
  "FOOTER",
  "H1",
  "H2",
  "H3",
  "H4",
  "H5",
  "H6",
  "HEADER",
  "HR",
  "LI",
  "MAIN",
  "NAV",
  "OL",
  "P",
  "PRE",
  "SECTION",
  "TABLE",
  "TBODY",
  "TD",
  "TFOOT",
  "TH",
  "THEAD",
  "TR",
  "UL",
]);

export interface DOMSegment {
  node: Text;
  start: number;
  end: number;
}

export interface SearchTextIndex {
  segments: Array<DOMSegment | null>;
  foldedChars: string[];
  length: number;
}

const UNICODE_SPACE_RE = /\p{White_Space}/u;
const ASCII_LOWER = "abcdefghijklmnopqrstuvwxyz";
const SEARCH_MARK_ATTR = "data-search-mark";
const SEARCH_MARK_SELECTOR = "mark[data-search-mark]";

function isSpaceLike(char: string): boolean {
  // Go's unicode.IsSpace follows the Unicode White_Space property. JavaScript
  // \s differs for NEL (U+0085) and BOM (U+FEFF), which would shift every
  // backend-provided character offset after one of those code points.
  const code = char.charCodeAt(0);
  // Below U+0080 the White_Space set is exactly SP plus HT/LF/VT/FF/CR, so
  // ordinary prose never reaches the regex engine.
  if (code < 0x80) return code === 0x20 || (code >= 0x09 && code <= 0x0d);
  return UNICODE_SPACE_RE.test(char);
}

function isAllSpaceLike(text: string): boolean {
  for (const char of text) {
    if (!isSpaceLike(char)) return false;
  }
  return true;
}

function foldCodePoint(char: string): string {
  const code = char.charCodeAt(0);
  // ASCII fast path: A-Z is the only case mapping below U+0080 and it is one
  // code point wide, so ordinary prose skips both allocations below.
  if (code < 0x80) {
    return code >= 0x41 && code <= 0x5a
      ? ASCII_LOWER.charAt(code - 0x41)
      : char;
  }
  const lowered = char.toLowerCase();
  // String#toLowerCase applies full mappings and can expand one code point
  // (notably İ -> i + combining dot). The backend deliberately uses Go's
  // one-rune unicode.ToLower mapping so offsets stay one-to-one. Keep only the
  // corresponding first code point when JavaScript returns an expansion.
  return Array.from(lowered)[0] ?? char;
}

export function foldQuery(query: string): string[] {
  const trimmed = query.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
  return Array.from(trimmed, foldCodePoint);
}

function isTextBoundaryElement(node: Element): boolean {
  return TEXT_BOUNDARY_TAGS.has(node.tagName);
}

/**
 * Flattens `root` into the whitespace-collapsed character sequence the backend
 * search index produces, keeping one DOM segment per character so a character
 * range can be mapped back to Ranges. Collapsed whitespace gets an index entry
 * with a null segment.
 *
 * `limit` stops the walk once that many characters exist. Search navigation
 * rebuilds this index on every "next match" and only needs it up to the end of
 * the match; the fallback scan is the only caller that needs the whole chapter.
 */
export function buildSearchTextIndex(
  root: HTMLElement,
  limit = Number.POSITIVE_INFINITY,
): SearchTextIndex {
  const segments: Array<DOMSegment | null> = [];
  const foldedChars: string[] = [];
  let length = 0;
  let pendingSpace = false;

  function queueBoundary(): void {
    if (length === 0) return;
    pendingSpace = true;
  }

  function emitPendingSpace(): void {
    if (!pendingSpace || length === 0) return;
    // A collapsed whitespace run has no single safe DOM wrapper when it spans
    // nodes or block boundaries, so it is searchable but unwrapped here.
    // wrapIndexRangeInMarks re-absorbs the ones that sit inside a single Text
    // node, which is what keeps a multi-word match visually contiguous.
    segments[length] = null;
    foldedChars[length] = " ";
    length += 1;
    pendingSpace = false;
  }

  function emitTextNode(node: Text): void {
    let utf16Offset = 0;

    for (const char of node.data) {
      if (length >= limit) return;
      const startOffset = utf16Offset;
      utf16Offset += char.length;
      const endOffset = utf16Offset;

      if (isSpaceLike(char)) {
        if (length > 0) pendingSpace = true;
        continue;
      }

      emitPendingSpace();
      if (length >= limit) return;
      segments[length] = { node, start: startOffset, end: endOffset };
      foldedChars[length] = foldCodePoint(char);
      length += 1;
    }
  }

  function walk(node: Node): void {
    if (length >= limit) return;

    if (node.nodeType === Node.TEXT_NODE) {
      emitTextNode(node as Text);
      return;
    }

    if (node.nodeType !== Node.ELEMENT_NODE) return;

    const element = node as Element;
    const tag = element.tagName;

    if (
      tag === "HEAD" ||
      tag === "SCRIPT" ||
      tag === "STYLE" ||
      tag === "NOSCRIPT"
    )
      return;
    if (tag === "BR") {
      queueBoundary();
      return;
    }

    const boundary = isTextBoundaryElement(element);
    if (boundary) queueBoundary();

    for (let child = node.firstChild; child; child = child.nextSibling) {
      walk(child);
    }

    if (boundary) queueBoundary();
  }

  walk(root);
  return { segments, foldedChars, length };
}

function createSearchMark(): HTMLElement {
  const mark = document.createElement("mark");
  mark.className = "search-highlight";
  // Book content may ship its own <mark class="search-highlight">. Ours are
  // identified by attribute so clearing never unwraps — and normalize()s — a
  // piece of the chapter that was authored that way.
  mark.setAttribute(SEARCH_MARK_ATTR, "");
  return mark;
}

function unwrapSearchMarks(marks: Iterable<Element>): void {
  const parents = new Set<Node>();
  for (const mark of marks) {
    const parent = mark.parentNode;
    if (!parent) continue;
    parents.add(parent);
    mark.replaceWith(...Array.from(mark.childNodes));
  }
  for (const parent of parents) parent.normalize();
}

function wrapIndexRangeInMarks(
  index: SearchTextIndex,
  start: number,
  end: number,
): HTMLElement | null {
  const runs: DOMSegment[] = [];

  for (let i = start; i < end; i += 1) {
    const segment = index.segments[i];
    if (!segment) continue;

    const previous = runs.at(-1);
    if (previous?.node === segment.node && previous.end <= segment.start) {
      // Extend across characters the index collapsed away: the space in
      // "hello world" is an index entry with no segment of its own, and
      // without this every multi-word match renders as separate marks with
      // visibly unhighlighted gaps. Only whitespace may be swallowed — any
      // other text between two runs lies outside the match.
      if (
        previous.end === segment.start ||
        isAllSpaceLike(segment.node.data.slice(previous.end, segment.start))
      ) {
        previous.end = segment.end;
        continue;
      }
    }

    runs.push({ ...segment });
  }

  if (runs.length === 0) return null;

  // Wrap from the end so splitting a later Text node cannot invalidate an
  // earlier run. Every Range stays within one Text node, preserving all inline
  // and block ancestors even when the match crosses their boundaries.
  const marks: HTMLElement[] = [];
  for (let i = runs.length - 1; i >= 0; i -= 1) {
    const run = runs[i];
    const mark = createSearchMark();
    try {
      const range = document.createRange();
      range.setStart(run.node, run.start);
      range.setEnd(run.node, run.end);
      range.surroundContents(mark);
      marks.push(mark);
    } catch {
      // Unreachable while every run stays inside one Text node, but an index
      // built against a since-mutated DOM throws from setStart, and the frame
      // message handler has no other error path: roll back to the pre-call DOM
      // and let the caller fall back to a query scan.
      unwrapSearchMarks(marks);
      return null;
    }
  }

  // Wrapping back to front leaves marks in reverse document order, so the last
  // entry is the first mark of the match: the one worth revealing.
  return marks.at(-1) ?? null;
}

export function matchesFoldedAt(
  haystack: string[],
  start: number,
  needle: string[],
): boolean {
  if (start < 0 || start + needle.length > haystack.length) return false;
  for (let i = 0; i < needle.length; i += 1) {
    if (haystack[start + i] !== needle[i]) return false;
  }
  return true;
}

export function findFoldedMatch(haystack: string[], needle: string[]): number {
  if (needle.length === 0 || needle.length > haystack.length) return -1;
  const first = needle[0];
  const limit = haystack.length - needle.length;
  // Screen candidates on the first character before the full compare; this
  // scans a whole chapter whenever backend offsets and the DOM disagree.
  for (let start = 0; start <= limit; start += 1) {
    if (haystack[start] !== first) continue;
    if (matchesFoldedAt(haystack, start, needle)) return start;
  }
  return -1;
}

export function createSearchHighlight(
  deps: SearchHighlightDeps,
): SearchHighlighter {
  function clearSearchHighlights(): void {
    // Scope to the chapter: a stray mark elsewhere in the shell is not ours,
    // and an unscoped document query would also reach into UI chrome.
    const root: ParentNode = deps.getContentEl() ?? document;
    unwrapSearchMarks(root.querySelectorAll(SEARCH_MARK_SELECTOR));
  }

  function revealHighlight(mark: HTMLElement): void {
    if (deps.isPagedMode()) {
      deps.goToPageInternal(deps.getElementPageIndex(mark), false);
      return;
    }
    mark.scrollIntoView({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
      block: "center",
    });
  }

  function fallbackHighlight(query: string, content: HTMLElement): void {
    // Always re-index. This path is reached either because the backend offsets
    // did not fit the DOM, or because a failed wrap rolled back and normalized
    // the very Text nodes an earlier index pointed at.
    const index = buildSearchTextIndex(content);
    const foldedQuery = foldQuery(query);
    const start = findFoldedMatch(index.foldedChars, foldedQuery);
    if (start === -1) return;

    const mark = wrapIndexRangeInMarks(
      index,
      start,
      start + foldedQuery.length,
    );
    if (mark) revealHighlight(mark);
  }

  function highlightSearchMatch(
    charOffset: number,
    matchLen: number,
    query: string,
  ): void {
    const content = deps.getContentEl();
    if (
      !deps.isContentReady() ||
      !content ||
      !Number.isSafeInteger(charOffset) ||
      !Number.isSafeInteger(matchLen) ||
      charOffset < 0 ||
      matchLen <= 0
    ) {
      return;
    }

    const matchEnd = charOffset + matchLen;
    if (!Number.isSafeInteger(matchEnd)) return;

    // Clearing first is structural, not just tidy: the index records Text nodes
    // and offsets, so it has to be built against a mark-free DOM. That does
    // mean a match the checks below reject leaves no highlight standing — the
    // right outcome, since the surviving one would belong to a previous result
    // and would read as if search had jumped to the wrong place.
    clearSearchHighlights();

    // Index only as far as this match needs; the verified path is the one
    // "next match" hits on every keystroke-driven navigation.
    const index = buildSearchTextIndex(content, matchEnd);
    if (index.length < matchEnd) {
      if (query) fallbackHighlight(query, content);
      return;
    }

    // Offsets come from a separately extracted copy of the chapter, so verify
    // the query actually sits where the backend says before trusting them.
    const foldedQuery = foldQuery(query);
    if (
      foldedQuery.length > 0 &&
      (foldedQuery.length !== matchLen ||
        !matchesFoldedAt(index.foldedChars, charOffset, foldedQuery))
    ) {
      fallbackHighlight(query, content);
      return;
    }

    const mark = wrapIndexRangeInMarks(index, charOffset, matchEnd);
    if (!mark) {
      if (query) fallbackHighlight(query, content);
      return;
    }

    revealHighlight(mark);
  }

  return { highlightSearchMatch, clearSearchHighlights };
}
