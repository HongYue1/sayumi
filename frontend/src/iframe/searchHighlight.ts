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

export interface SearchHighlightDeps {
  /** The live chapter content element (#content). */
  getContentEl: () => HTMLElement;
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

// Matches the guarded + lazily-cached MediaQueryList form used by the two other
// frame-side helpers (frame.ts, pagination.ts). The MQL is live, so a cached
// instance still reflects the current OS setting on every read; caching avoids
// allocating a fresh MediaQueryList per highlight scroll, and the typeof guard
// keeps it safe where window.matchMedia is absent (e.g. the test DOM).
let reduceMotionQuery: MediaQueryList | null = null;
function prefersReducedMotion(): boolean {
  if (typeof window.matchMedia !== "function") return false;
  reduceMotionQuery ??= window.matchMedia("(prefers-reduced-motion: reduce)");
  return reduceMotionQuery.matches;
}

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
  "FORM",
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

interface DOMSegment {
  node: Text;
  start: number;
  end: number;
}

interface SearchTextIndex {
  segments: Array<DOMSegment | null>;
  foldedChars: string[];
  length: number;
}

const UNICODE_SPACE_RE = /\p{White_Space}/u;

function isSpaceLike(char: string): boolean {
  // Go's unicode.IsSpace follows the Unicode White_Space property. JavaScript
  // \s differs for NEL (U+0085) and BOM (U+FEFF), which would shift every
  // backend-provided character offset after one of those code points.
  return UNICODE_SPACE_RE.test(char);
}

function foldCodePoint(char: string): string {
  const lowered = char.toLowerCase();
  // String#toLowerCase applies full mappings and can expand one code point
  // (notably İ -> i + combining dot). The backend deliberately uses Go's
  // one-rune unicode.ToLower mapping so offsets stay one-to-one. Keep only the
  // corresponding first code point when JavaScript returns an expansion.
  return Array.from(lowered)[0] ?? char;
}

function foldQuery(query: string): string[] {
  const trimmed = query.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
  return Array.from(trimmed, foldCodePoint);
}

function isTextBoundaryElement(node: Element): boolean {
  return (
    TEXT_BOUNDARY_TAGS.has(node.tagName) ||
    node.tagName === "SVG" ||
    node.tagName === "MATH"
  );
}

function buildSearchTextIndex(root: HTMLElement): SearchTextIndex {
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
    // nodes or block boundaries. Keep it searchable but leave it unwrapped;
    // the adjacent readable characters still receive visible marks.
    segments[length] = null;
    foldedChars[length] = " ";
    length += 1;
    pendingSpace = false;
  }

  function emitTextNode(node: Text): void {
    let utf16Offset = 0;

    for (const char of node.data) {
      const startOffset = utf16Offset;
      utf16Offset += char.length;
      const endOffset = utf16Offset;

      if (isSpaceLike(char)) {
        if (length > 0) pendingSpace = true;
        continue;
      }

      emitPendingSpace();
      segments[length] = { node, start: startOffset, end: endOffset };
      foldedChars[length] = foldCodePoint(char);
      length += 1;
    }
  }

  function walk(node: Node): void {
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
  mark.className = "search-highlight search-highlight-active";
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
    if (previous?.node === segment.node && previous.end === segment.start) {
      previous.end = segment.end;
    } else {
      runs.push({ ...segment });
    }
  }

  if (runs.length === 0) return null;

  // Wrap from the end so splitting a later Text node cannot invalidate an
  // earlier run. Every Range stays within one Text node, preserving all inline
  // and block ancestors even when the match crosses their boundaries.
  const marks: HTMLElement[] = [];
  for (let i = runs.length - 1; i >= 0; i -= 1) {
    const run = runs[i];
    const range = document.createRange();
    range.setStart(run.node, run.start);
    range.setEnd(run.node, run.end);

    const mark = createSearchMark();
    try {
      range.surroundContents(mark);
      marks.push(mark);
    } catch {
      unwrapSearchMarks(marks);
      return null;
    }
  }

  return marks.at(-1) ?? null;
}

function matchesFoldedAt(
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

function findFoldedMatch(haystack: string[], needle: string[]): number {
  if (needle.length === 0 || needle.length > haystack.length) return -1;
  const limit = haystack.length - needle.length;
  for (let start = 0; start <= limit; start += 1) {
    if (matchesFoldedAt(haystack, start, needle)) return start;
  }
  return -1;
}

export function createSearchHighlight(
  deps: SearchHighlightDeps,
): SearchHighlighter {
  function clearSearchHighlights(): void {
    unwrapSearchMarks(document.querySelectorAll("mark.search-highlight"));
  }

  function revealHighlight(mark: HTMLElement): void {
    if (deps.isPagedMode()) {
      deps.goToPageInternal(deps.getElementPageIndex(mark), true);
    } else {
      mark.scrollIntoView({
        behavior: prefersReducedMotion() ? "auto" : "smooth",
        block: "center",
      });
    }
  }

  function fallbackHighlight(query: string, index: SearchTextIndex): void {
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
      matchLen <= 0 ||
      charOffset < 0
    ) {
      return;
    }

    const matchEnd = charOffset + matchLen;
    if (!Number.isSafeInteger(matchEnd)) return;
    clearSearchHighlights();
    const index = buildSearchTextIndex(content);
    if (matchEnd > index.length) {
      if (query) fallbackHighlight(query, index);
      return;
    }

    const foldedQuery = foldQuery(query);
    if (
      foldedQuery.length > 0 &&
      (foldedQuery.length !== matchLen ||
        !matchesFoldedAt(index.foldedChars, charOffset, foldedQuery))
    ) {
      fallbackHighlight(query, index);
      return;
    }

    const mark = wrapIndexRangeInMarks(index, charOffset, matchEnd);
    if (!mark) {
      if (query) fallbackHighlight(query, index);
      return;
    }
    revealHighlight(mark);
  }

  return { highlightSearchMatch, clearSearchHighlights };
}
