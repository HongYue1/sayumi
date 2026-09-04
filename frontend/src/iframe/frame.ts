import { decodeHrefComponent } from "~/lib/href";
import { keyboardEventIsOwnedByTarget } from "~/lib/keyboard";
import { reserveSearchMarkAttribute } from "~/lib/searchMarks";
import type {
  IframeSettings,
  LoadMessage,
  ParentToFrameMessage,
  FrameToParentMessage,
  FrameModeState,
} from "~/lib/frameMessages";
import {
  splitBookCSS,
  stripColorsFromCSS,
  extractBookFontFamilies,
  filterReaderFontFaces,
} from "./cssText";
import { generateCFI, resolveCFI } from "~/lib/cfi";
import { createSearchHighlight } from "./searchHighlight";
import { createBoundary } from "./boundary";
import { createPagination } from "./pagination";
import { prefersReducedMotion } from "./reduceMotion";

const EXTERNAL_BOOK_LINK_SCHEMES = new Set(["http", "https", "mailto", "tel"]);

// Keys whose browser default is to scroll the viewport / nearest scroller. In
// paged mode the page turn is a JS opacity cross-fade plus a single scrollLeft
// swap on #content, so letting these keys ALSO scroll it natively races the
// fade and makes the turn flicker. Their default is prevented in paged mode
// while the parent still drives discrete page navigation from the forwarded
// key event.
const PAGED_SCROLL_KEYS = new Set<string>([
  "ArrowLeft",
  "ArrowRight",
  "ArrowUp",
  "ArrowDown",
  "PageUp",
  "PageDown",
  "Home",
  "End",
  " ",
]);

(function () {
  "use strict";

  const BLOCK_TAGS = new Set([
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
    "H1",
    "H2",
    "H3",
    "H4",
    "H5",
    "H6",
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

  const WHEEL_THRESHOLD = 600;
  const CHAPTER_SWAP_OUT_MS = 110;
  const REVEAL_FALLBACK_SCROLL_MS = 400;
  const REVEAL_FALLBACK_PAGED_MS = 550;
  // The parent preserves nullish/omitted CFIs for backward compatibility. An
  // empty string is an explicit "use percent" marker: it crosses that bridge as
  // a real update, while commitLoad rejects it as a restore CFI.
  const NO_CFI = "";

  let activeSeq = -1;
  let activeChapterIndex = -1;
  let contentReady = false;
  // True from commitLoad until the (fonts-gated) initial position restore has
  // run. While pending, position reports, boundary accumulation, and search
  // highlights stand down: the DOM still shows the previous chapter's scroll
  // offset, and reporting it under the NEW seq clobbers saved progress (a
  // leftover momentum scroll or the 200ms trailing report timer was enough).
  let restorePending = false;
  // Non-empty when the chapter declares a vertical writing mode: content flows
  // along the horizontal axis, so all scroll-mode math must use scrollX /
  // scrollWidth. "rl" starts at the right edge (scrollX runs 0 → -max).
  let verticalWriting: "" | "rl" | "lr" = "";
  let loadScrollTarget: "top" | "end" | null = null;
  let pendingFragment: string | null = null;
  let parentOrigin = "";
  let destroyed = false;
  let rasterRefreshRafHandle: number | null = null;

  let readerFontFaces = "";

  let rawBookCSS = "";
  let bookFontFaceCSS = "";
  let preparedRawCSS = "";
  let preparedLayoutCSS = "";
  let preparedFontCSS = "";
  let preparedFontFaceCSS = "";
  let preparedBookFontFamilies = new Set<string>();

  // Last values written to the #font-face-css / #book-css <style> nodes. Only
  // applySettings writes those nodes, so these stay authoritative. Assigning
  // .textContent rebuilds the element's CSSOM stylesheet (a full reparse of the
  // whole book stylesheet) even when the text is identical, so we skip writes
  // that don't change. These two only vary on chapter swap or a
  // preserveBookStyles/preserveBookFonts toggle; font-size/line-height/margin
  // drags only touch #override-css (still rewritten unconditionally below).
  let _lastFontFaceContent: string | null = null;
  let _lastBookCSS: string | null = null;
  let _lastOverrideCSS: string | null = null;

  let _contentEl: HTMLElement | null = null;
  let _clipEl: HTMLElement | null = null;
  const _styleEls: Record<string, HTMLStyleElement> = {};

  // Memoised, finished output of prepareChapterCSS() keyed by the inputs that
  // determine it. Bounded LRU (matches the host's chapter cache size) so
  // revisiting a recent chapter skips the CSSStyleSheet reparses below.
  const PREPARED_CSS_CACHE_MAX = 4;
  const _preparedCSSCache = new Map<
    string,
    {
      origin: string;
      fontFaceInput: string;
      fontCSS: string;
      layoutCSS: string;
      rawCSS: string;
      fontFaceCSS: string;
      bookFontFamilies: Set<string>;
    }
  >();

  let activeThemeClass =
    Array.from(document.documentElement.classList).find((c) =>
      c.startsWith("theme-"),
    ) ?? "theme-light";

  let atTop = false;
  let atBottom = false;
  let hasNextChapter = true;
  let hasPrevChapter = true;

  let touchStartY = 0;
  let touchStartX = 0;
  let touchLastX = 0;
  let touchLastY = 0;
  let touchTracking = false;
  let touchBoundaryBase = 0;
  let touchAtBoundaryOnStart = false;

  const boundary = createBoundary({
    sendMessage,
    getActiveSeq: () => activeSeq,
    hasPrevChapter: () => hasPrevChapter,
    hasNextChapter: () => hasNextChapter,
    getWritingMode: () => verticalWriting,
  });

  const pagination = createPagination({
    getContentEl,
    getClipEl,
    sendMessage,
    getActiveSeq: () => activeSeq,
    getActiveChapterIndex: () => activeChapterIndex,
    isDestroyed: () => destroyed,
    isContentReady: () => contentReady,
    isRestorePending: () => restorePending,
    getPositionCfi: () => anchorCfiForCurrentView(),
    isPagedMode: () => isPagedMode,
    hasNextChapter: () => hasNextChapter,
    hasPrevChapter: () => hasPrevChapter,
    setChapterHidden,
    ensureBoundaryElements: () => boundary.ensureElements(),
    flashBoundaryEdge: (direction) => boundary.flashEdge(direction),
    updateBoundaryState,
    takePendingFragment: () => {
      const f = pendingFragment;
      pendingFragment = null;
      return f;
    },
  });

  let isPagedMode = false;
  let lastReportedMode: (FrameModeState & { seq: number }) | null = null;
  let reportPositionRafHandle: number | null = null;
  let scrollRafHandle: number | null = null;
  // Wheel/touch boundary pulls read layout and then write indicator styles, so
  // doing both per raw event forces a reflow on every tick. Coalesced into one
  // rAF each, the same way handleScroll coalesces its reads.
  let wheelPullDelta = 0;
  let wheelPullRafHandle: number | null = null;
  let touchPullRafHandle: number | null = null;
  let pagedRelayoutRafHandle: number | null = null;
  let revealFallbackTimer: ReturnType<typeof setTimeout> | null = null;
  let loadCommitTimer: ReturnType<typeof setTimeout> | null = null;
  let chapterAnimTimer: ReturnType<typeof setTimeout> | null = null;
  // Slightly longer than the longest #paged-clip transition (fade-in 0.18s) so
  // the compositor hint stays for the whole swap, then is removed.
  const CHAPTER_ANIM_SETTLE_MS = 240;
  let loadTransitionToken = 0;
  let pendingSettingsMessage: IframeSettings | null = null;
  let pendingSearchHighlight: {
    charOffset: number;
    matchLen: number;
    query: string;
    seq?: number;
  } | null = null;

  let loadRestorePercent: number | null = null;
  let loadRestoreCfi: string | null = null;

  let lastVisibleBlock: Element | null = null;
  let lastReportedAnchor: Element | null = null;
  let lastReportedCfi: string | null = null;

  function absolutifyHTML(html: string): string {
    if (!parentOrigin) return html;
    let result = html.replace(
      /((?:src|poster)\s*=\s*["'])(\/api\/)/gi,
      (_, prefix, path) => prefix + parentOrigin + path,
    );
    result = result.replace(
      /(srcset\s*=\s*["'])([^"']+)(["'])/gi,
      (_, open, value, close) =>
        open + value.replace(/\/api\//g, parentOrigin + "/api/") + close,
    );
    return result;
  }

  function absolutifyCSS(css: string): string {
    if (!parentOrigin || !css) return css;
    return css.replace(
      /url\(\s*['\"]?(\/api\/[^'"\s)]+)['\"]?\s*\)/gi,
      (_, path) => `url('${parentOrigin}${path}')`,
    );
  }

  function isUsableVisibleBlock(node: Element, content: HTMLElement): boolean {
    if (!content.contains(node) || !BLOCK_TAGS.has(node.tagName)) return false;
    const rect = node.getBoundingClientRect();
    const vh = Number.isFinite(window.innerHeight) ? window.innerHeight : 0;
    const vw = Number.isFinite(window.innerWidth) ? window.innerWidth : 0;
    // This simplified CFI addresses elements, not an offset within one. A block
    // that fills the viewport along the flow axis (or overflows it) cannot
    // restore the current position precisely, so omit its anchor and let the
    // explicit no-CFI marker select the more accurate percent fallback instead
    // of jumping to the block's start.
    //
    // Both tests follow the flow axis. A vertical chapter flows along X, where
    // every block is viewport-tall by construction: measuring height there
    // rejects every candidate and silently disables CFI anchoring, while
    // testing only vertical overlap accepts blocks sitting whole viewports away
    // along the reading axis and anchors the position to the wrong place.
    if (verticalWriting) {
      return rect.width < vw && rect.right > 0 && rect.left < vw;
    }
    return rect.height < vh && rect.bottom > 0 && rect.top < vh;
  }

  function ascendToVisibleBlock(
    leaf: Element | null,
    content: HTMLElement,
  ): Element | null {
    let node = leaf;
    while (node && node !== content) {
      if (isUsableVisibleBlock(node, content)) return node;
      node = node.parentElement;
    }
    return null;
  }

  function findFirstVisibleBlock(): Element | null {
    const content = getContentEl();
    if (!content) return null;

    if (lastVisibleBlock && isUsableVisibleBlock(lastVisibleBlock, content)) {
      return lastVisibleBlock;
    }

    const vw = Number.isFinite(window.innerWidth) ? window.innerWidth : 0;
    const vh = Number.isFinite(window.innerHeight) ? window.innerHeight : 0;
    if (vw <= 0 || vh <= 0) return null;
    // Probe inward from the reading edge: the top edge for horizontal-tb, the
    // right edge for vertical-rl (which reads right to left), the left edge for
    // vertical-lr. Sampling top-centre in a vertical chapter anchors to a block
    // up to half a viewport ahead of the reader.
    const span = verticalWriting ? vw : vh;
    const samples = [8, 40, Math.floor(span * 0.18), Math.floor(span * 0.33)];
    const probePoint = (offset: number): [number, number] => {
      if (!verticalWriting) return [vw / 2, offset];
      return verticalWriting === "rl"
        ? [vw - 1 - offset, vh / 2]
        : [offset, vh / 2];
    };

    for (const offset of samples) {
      if (offset < 0 || offset >= span) continue;
      const [px, py] = probePoint(offset);
      const leaf = document.elementFromPoint(px, py);
      if (!leaf || !content.contains(leaf)) continue;
      const block = ascendToVisibleBlock(leaf, content);
      if (block) {
        lastVisibleBlock = block;
        return block;
      }
    }

    return null;
  }

  function getStyleEl(id: string): HTMLStyleElement {
    return (_styleEls[id] ??= document.getElementById(id) as HTMLStyleElement);
  }

  // Nullable on purpose. Every caller already guards, and searchHighlight's dep
  // contract types it honestly; the cast these used to carry asserted a
  // guarantee the lookup cannot make. Caching with ??= is safe across chapter
  // swaps because #content and #paged-clip are static srcdoc shell nodes -- a
  // load replaces the inner HTML, never these elements.
  function getContentEl(): HTMLElement | null {
    return (_contentEl ??= document.getElementById("content"));
  }

  function getClipEl(): HTMLElement | null {
    return (_clipEl ??= document.getElementById("paged-clip"));
  }

  function sendMessage(msg: FrameToParentMessage): void {
    if (destroyed) return;
    // Position reports must distinguish "no semantic anchor for this position"
    // from an older sender that omitted the field. Pagination has no element
    // anchor, and scroll mode can intentionally fall back to percent for a
    // viewport-tall block, so publish the explicit marker instead of letting an
    // older CFI override the newer percent on load.
    const outbound =
      msg.type === "position" && msg.cfi === undefined
        ? { ...msg, cfi: NO_CFI }
        : msg;
    window.parent.postMessage(outbound, parentOrigin || "*");
  }

  function getScrollableMax(): number {
    const root = document.documentElement;
    const max = verticalWriting
      ? root.scrollWidth - window.innerWidth
      : root.scrollHeight - window.innerHeight;
    return Number.isFinite(max) ? Math.max(0, max) : 0;
  }

  // Reading-order scroll offset on the chapter's flow axis, always positive.
  // vertical-rl roots scroll 0 → -max (Chromium exposes leftward travel as
  // negative scrollX), so |scrollX| is the distance read in both vertical
  // directions.
  function getAxisScrollPos(): number {
    return verticalWriting ? Math.abs(window.scrollX) : window.scrollY;
  }

  // Scrolls to a reading-order offset on the flow axis (instant).
  function axisScrollTo(logicalPos: number): void {
    if (verticalWriting) {
      window.scrollTo({
        left: verticalWriting === "rl" ? -logicalPos : logicalPos,
        behavior: "instant" as ScrollBehavior,
      });
      return;
    }
    window.scrollTo({ top: logicalPos, behavior: "instant" as ScrollBehavior });
  }

  function updateBoundaryState(): void {
    const scrollPos = getAxisScrollPos();
    const scrollMax = getScrollableMax();
    if (scrollMax <= 0) {
      atTop = true;
      atBottom = true;
    } else {
      atTop = scrollPos <= 1;
      atBottom = scrollPos >= scrollMax - 1;
    }
  }

  function prepareChapterCSS(): void {
    // Each prepare runs several CSSStyleSheet parses (splitBookCSS + two
    // stripColorsFromCSS passes); memoise the finished bundle so revisiting a
    // recently-read chapter (back / forward, boundary cross) reuses it instead
    // of reparsing on the main thread. This is pure memoisation of the existing
    // output: the same CSS is parsed, split, stripped and absolutified exactly
    // as before on a miss, so the sandbox CSP, nonce handling and color/style
    // stripping behaviour are completely unchanged. parentOrigin is part of the
    // key because absolutifyCSS rewrites /api/ urls against it.
    // Keyed on the book stylesheet itself rather than on a concatenation of
    // every input: that composite key was a third full-size copy of the CSS,
    // allocated on every load and retained for the life of the entry. The other
    // two inputs are verified on the entry, so a hit is still an exact match.
    const cacheKey = rawBookCSS;
    const hit = _preparedCSSCache.get(cacheKey);
    if (
      hit &&
      hit.origin === parentOrigin &&
      hit.fontFaceInput === bookFontFaceCSS
    ) {
      _preparedCSSCache.delete(cacheKey);
      _preparedCSSCache.set(cacheKey, hit); // refresh LRU position
      preparedFontCSS = hit.fontCSS;
      preparedLayoutCSS = hit.layoutCSS;
      preparedRawCSS = hit.rawCSS;
      preparedFontFaceCSS = hit.fontFaceCSS;
      preparedBookFontFamilies = hit.bookFontFamilies;
      return;
    }
    if (hit) _preparedCSSCache.delete(cacheKey);
    const { fontCSS, layoutCSS } = splitBookCSS(rawBookCSS);
    preparedFontCSS = fontCSS;
    preparedLayoutCSS = stripColorsFromCSS(absolutifyCSS(layoutCSS));
    preparedRawCSS = stripColorsFromCSS(absolutifyCSS(rawBookCSS));
    preparedFontFaceCSS = absolutifyCSS(bookFontFaceCSS);
    preparedBookFontFamilies = extractBookFontFamilies(bookFontFaceCSS);
    if (_preparedCSSCache.size >= PREPARED_CSS_CACHE_MAX) {
      const lru = _preparedCSSCache.keys().next().value;
      if (lru !== undefined) _preparedCSSCache.delete(lru);
    }
    _preparedCSSCache.set(cacheKey, {
      origin: parentOrigin,
      fontFaceInput: bookFontFaceCSS,
      fontCSS: preparedFontCSS,
      layoutCSS: preparedLayoutCSS,
      rawCSS: preparedRawCSS,
      fontFaceCSS: preparedFontFaceCSS,
      bookFontFamilies: preparedBookFontFamilies,
    });
  }

  function setChapterHidden(hidden: boolean): void {
    const root = document.documentElement;
    // Promote the swap layer for the duration of the transition, then demote it.
    // Leaving will-change on permanently (in CSS) wastes a compositor layer;
    // toggling a transient class around the swap is the recommended
    // will-change pattern. Covers both swap-out (hidden=true) and reveal
    // (hidden=false) since both route through here.
    root.classList.add("chapter-anim");
    if (chapterAnimTimer !== null) clearTimeout(chapterAnimTimer);
    chapterAnimTimer = setTimeout(() => {
      chapterAnimTimer = null;
      document.documentElement.classList.remove("chapter-anim");
    }, CHAPTER_ANIM_SETTLE_MS);
    root.classList.toggle("chapter-hidden", hidden);
  }

  function shouldAnimateChapterSwap(): boolean {
    const root = document.documentElement;
    return (
      contentReady &&
      (isPagedMode ||
        root.classList.contains("paged") ||
        root.classList.contains("paged-two"))
    );
  }

  function beginChapterSwapOut(): void {
    boundary.reset();
    killScrollMomentum();
    document.body.style.opacity = "0";
    document.documentElement.style.overflow = "hidden";
    setChapterHidden(true);
    pagination.setPageTurning(false);
  }

  function applyRootClasses(theme: string, mode: string): void {
    const root = document.documentElement;
    const nextThemeClass = `theme-${theme}`;
    if (activeThemeClass !== nextThemeClass) {
      root.classList.remove(activeThemeClass);
      root.classList.add(nextThemeClass);
      activeThemeClass = nextThemeClass;
    }
    root.classList.toggle("paged", mode === "paged");
    root.classList.toggle("paged-two", mode === "paged-two");
  }

  function releaseOverflowAfterSettings(): void {
    if (contentReady) {
      document.documentElement.style.overflow = "";
      return;
    }

    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        if (!destroyed) document.documentElement.style.overflow = "";
      }),
    );
  }

  function revealScrollShell(): void {
    document.body.style.opacity = "1";
    document.documentElement.style.overflow = "";
    boundary.ensureElements();
    updateBoundaryState();
    requestAnimationFrame(() => {
      if (!destroyed) setChapterHidden(false);
    });
  }

  function restoreScrollPercent(percent: number): void {
    const max = getScrollableMax();
    const pct = Number.isFinite(percent)
      ? Math.min(1, Math.max(0, percent))
      : 0;
    axisScrollTo(max * pct);
  }

  function restoreScrollPosition(): void {
    // From here on the DOM reflects THIS chapter's position; reports may flow.
    restorePending = false;
    const fragment = pendingFragment;
    const restoreCfi = loadRestoreCfi;
    const restorePercent = loadRestorePercent;
    const scrollTarget = loadScrollTarget;

    pendingFragment = null;
    loadRestoreCfi = null;
    loadRestorePercent = null;
    loadScrollTarget = null;

    if (fragment) {
      const target = document.getElementById(fragment);
      if (target) {
        // Instant, and before the reveal. The interactive scroll-to-fragment
        // path animates on purpose, but on a chapter that is still hidden a
        // smooth scroll shows one frame of the chapter top and then animates
        // away from it. An unresolvable id now falls through to the percent/CFI
        // restore below instead of revealing at the top.
        target.scrollIntoView({ behavior: "instant" as ScrollBehavior });
        boundary.reset();
        revealScrollShell();
        if (reportPositionRafHandle !== null)
          cancelAnimationFrame(reportPositionRafHandle);
        reportPositionRafHandle = requestAnimationFrame(reportPosition);
        return;
      }
    }

    axisScrollTo(scrollTarget === "end" ? getScrollableMax() : 0);

    if (restoreCfi) {
      const el = resolveCFI(restoreCfi, document);
      if (el) {
        el.scrollIntoView({ behavior: "instant" as ScrollBehavior });
      } else if (restorePercent !== null) {
        restoreScrollPercent(restorePercent);
      }
    } else if (restorePercent !== null) {
      restoreScrollPercent(restorePercent);
    }

    revealScrollShell();
    if (reportPositionRafHandle !== null)
      cancelAnimationFrame(reportPositionRafHandle);
    reportPositionRafHandle = requestAnimationFrame(() => {
      reportPositionRafHandle = requestAnimationFrame(reportPosition);
    });
  }

  // Restore the scroll position after a paged→scroll switch using the anchor
  // captured before the switch. Same-document transfer like enterPagedFromScroll
  // in the other direction: the element is still connected (no reload between
  // capture and use), so scroll to it directly instead of round-tripping a
  // CFI. Instant, like the load restore — a switch is not interactive
  // navigation. Runs synchronously inside applySettings after the paged inline
  // styles are cleared; getScrollableMax/scrollIntoView force the reflow, so
  // no rAF settling is needed.
  function restoreScrollFromSwitch(
    anchor: Element | null,
    ratio: number,
  ): void {
    if (anchor?.isConnected) {
      anchor.scrollIntoView({ behavior: "instant" as ScrollBehavior });
    } else {
      const pct = Number.isFinite(ratio) ? Math.min(1, Math.max(0, ratio)) : 0;
      axisScrollTo(getScrollableMax() * pct);
    }
    boundary.reset();
    updateBoundaryState();
    reportPosition();
  }

  function drainPendingSearchHighlight(): void {
    const h = pendingSearchHighlight;
    if (!h || !contentReady) return;
    if (typeof h.seq === "number" && h.seq !== activeSeq) {
      pendingSearchHighlight = null;
      return;
    }
    pendingSearchHighlight = null;
    invalidateAnchorCache();
    searchHl.highlightSearchMatch(h.charOffset, h.matchLen, h.query);
  }

  function revealAfterFonts(
    onReveal: () => void,
    timeoutMs = REVEAL_FALLBACK_SCROLL_MS,
  ): void {
    let fired = false;

    const timer = setTimeout(run, timeoutMs);
    revealFallbackTimer = timer;

    function run(): void {
      if (fired) return;
      fired = true;

      clearTimeout(timer);
      if (revealFallbackTimer === timer) {
        revealFallbackTimer = null;
      }

      if (!destroyed) onReveal();
    }

    void document.fonts.ready.then(run);
  }

  // Runs the restore for the mode the frame is in WHEN THE REVEAL FIRES, not
  // the mode captured when it was scheduled: a scroll<->paged toggle can land
  // inside the fonts-gated window, and running the captured path would silently
  // drop the restore (axisScrollTo is a no-op under the paged shell's
  // body{overflow:hidden}).
  function performInitialRestore(): void {
    if (isPagedMode) {
      const target = loadScrollTarget || "top";
      const pagedRestore = loadRestorePercent;
      const pagedRestoreElement = loadRestoreCfi
        ? resolveCFI(loadRestoreCfi, document)
        : null;
      loadScrollTarget = null;
      loadRestorePercent = null;
      loadRestoreCfi = null;
      restorePending = false;
      pagination.restorePagedPosition(
        target,
        pagedRestore,
        pagedRestoreElement,
      );
      requestAnimationFrame(drainPendingSearchHighlight);
      sendMessage({ type: "loaded", seq: activeSeq });
      return;
    }

    restoreScrollPosition();
    drainPendingSearchHighlight();
    sendMessage({ type: "loaded", seq: activeSeq });
  }

  function runInitialLayoutRestore(): void {
    const seqAtStart = activeSeq;
    // activeSeq alone is not a sufficient guard. The load handler bumps
    // loadTransitionToken and starts the swap-out fade CHAPTER_SWAP_OUT_MS
    // before commitLoad moves activeSeq, so a reveal armed for the outgoing
    // chapter still passes a seq-only check and un-hides the chapter being
    // replaced, reversing the fade mid-transition. Pin both.
    const tokenAtStart = loadTransitionToken;
    const isStale = (): boolean =>
      destroyed ||
      activeSeq !== seqAtStart ||
      loadTransitionToken !== tokenAtStart;

    // The paged shell needs a longer fallback: its multicol layout settles
    // later than a scroll chapter's.
    const fallbackMs = isPagedMode
      ? REVEAL_FALLBACK_PAGED_MS
      : REVEAL_FALLBACK_SCROLL_MS;

    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        if (isStale()) return;
        revealAfterFonts(() => {
          if (isStale()) return;
          performInitialRestore();
        }, fallbackMs);
      });
    });
  }

  function schedulePagedRelayout(): void {
    if (pagedRelayoutRafHandle !== null) return;
    pagedRelayoutRafHandle = requestAnimationFrame(() => {
      pagedRelayoutRafHandle = requestAnimationFrame(() => {
        pagedRelayoutRafHandle = null;
        if (destroyed || !isPagedMode || !contentReady) return;
        pagination.relayout();
      });
    });
  }

  function cancelScheduledPagedRelayout(): void {
    if (pagedRelayoutRafHandle === null) return;
    cancelAnimationFrame(pagedRelayoutRafHandle);
    pagedRelayoutRafHandle = null;
  }

  function reportEffectiveMode(
    mode: FrameModeState["mode"],
    fallback: FrameModeState["fallback"],
  ): void {
    if (
      lastReportedMode?.seq === activeSeq &&
      lastReportedMode.mode === mode &&
      lastReportedMode.fallback === fallback
    ) {
      return;
    }
    lastReportedMode = { seq: activeSeq, mode, fallback };
    sendMessage({ type: "effective-mode", seq: activeSeq, mode, fallback });
  }

  function applySettings(settings: IframeSettings): void {
    // Resolve the mode once and use it for every side of the layout contract:
    // generated CSS, root classes, JS pagination, and the parent report. A
    // vertical chapter cannot use the horizontal-only multicol paginator.
    const effectiveMode = verticalWriting ? "scroll" : settings.mode;
    const modeFallback =
      verticalWriting && settings.mode !== "scroll" ? "vertical-writing" : null;
    const willBePaged =
      effectiveMode === "paged" || effectiveMode === "paged-two";

    // Mid-chapter mode switch on a settled chapter: capture the position BEFORE
    // the CSS writes below reflow the document. Entering paged mode clamps the
    // window scroll (body goes overflow:hidden), and leaving paged collapses
    // the columns back into one flow — reading either side afterwards measures
    // the new layout, not where the reader was. Without the capture the paged
    // side restarts from the stale currentPage (0 after the load reset) and
    // the scroll side keeps its pre-paged offset; the report that follows then
    // overwrites the parent's good position and the next save persists it.
    // Loads are excluded (contentReady is still false at commitLoad), as is
    // the fonts-gated restore window (performInitialRestore reads the mode at
    // reveal time, so the load restore already lands in the final mode).
    const isModeSwitch =
      contentReady && !restorePending && isPagedMode !== willBePaged;
    let switchAnchor: Element | null = null;
    let switchRatio = 0;
    if (isModeSwitch) {
      switchAnchor = findFirstVisibleBlock();
      switchRatio = isPagedMode
        ? pagination.getCurrentRatio()
        : getScrollPercent();
      if (!Number.isFinite(switchRatio)) switchRatio = 0;
    }

    let fontFaceContent: string;
    if (settings.preserveBookFonts && preparedFontFaceCSS) {
      fontFaceContent =
        filterReaderFontFaces(readerFontFaces, preparedBookFontFamilies) +
        "\n" +
        preparedFontFaceCSS;
    } else {
      fontFaceContent = readerFontFaces;
    }
    if (fontFaceContent !== _lastFontFaceContent) {
      getStyleEl("font-face-css").textContent = fontFaceContent;
      _lastFontFaceContent = fontFaceContent;
    }

    let bookCSS = "";
    if (settings.preserveBookStyles && settings.preserveBookFonts) {
      bookCSS = preparedRawCSS;
    } else if (settings.preserveBookStyles && !settings.preserveBookFonts) {
      bookCSS = preparedLayoutCSS;
    } else if (!settings.preserveBookStyles && settings.preserveBookFonts) {
      bookCSS = preparedFontCSS;
    }
    if (bookCSS !== _lastBookCSS) {
      getStyleEl("book-css").textContent = bookCSS;
      _lastBookCSS = bookCSS;
    }

    const css: string[] = [
      "html, body { color: var(--text-primary) !important; background: var(--bg-primary) !important; }",
      "body { margin: 0 !important; }",
    ];

    if (effectiveMode === "paged" || effectiveMode === "paged-two") {
      // paged and paged-two share identical layout CSS; only the margin inputs
      // differ. paged-two clamps the vertical margin to a stable minimum and
      // pins the side inset; paged uses the raw margins with a 40px side
      // default. The vertical inset is applied to the column box itself (see
      // pagination.setPagedHeights, which reads --paged-padding-top/bottom),
      // not as #content-inner padding.
      let pt: string;
      let pb: string;
      let ps: string;
      if (effectiveMode === "paged-two") {
        const MIN_TWO_MARGIN = 24;
        pt = `${Math.max(settings.margins.top ?? 24, MIN_TWO_MARGIN)}px`;
        pb = `${Math.max(settings.margins.bottom ?? 24, MIN_TWO_MARGIN)}px`;
        ps = "48px";
      } else {
        pt =
          settings.margins.top != null ? `${settings.margins.top}px` : "24px";
        pb =
          settings.margins.bottom != null
            ? `${settings.margins.bottom}px`
            : "24px";
        ps =
          settings.margins.side != null ? `${settings.margins.side}px` : "40px";
      }

      css.push(
        `html { --paged-padding-top: ${pt}; --paged-padding-bottom: ${pb}; --paged-padding-side: ${ps}; }`,
      );
      // Two height declarations, mirroring frame.css's html.paged body rule:
      // the dvh one wins where it is supported and is dropped at parse where
      // it is not. A lone 100vh !important would override that rule's dvh
      // upgrade and leave the paged page box taller than the visible viewport
      // on mobile, pushing the last line and #page-indicator under the
      // browser chrome.
      css.push(
        "body { padding: 0 !important; height: 100vh !important; height: 100dvh !important; overflow: hidden !important; }",
      );
      // Vertical margin is applied as the paged column-box inset (clip
      // height/offset in pagination, via --paged-padding-top/bottom) so it
      // insets EVERY page, not just the first/last page of the chapter. Only the
      // side inset belongs on #content-inner here.
      css.push(`#content-inner { padding: 0 ${ps} !important; }`);
      css.push(
        "#content-inner > * { margin-left: 0 !important; margin-right: 0 !important; padding-left: 0 !important; padding-right: 0 !important; }",
      );
      css.push(
        "#content-inner > *:first-child { margin-top: 0 !important; padding-top: 0 !important; }",
      );
      css.push(
        "#content-inner > *:last-child { margin-bottom: 0 !important; padding-bottom: 0 !important; }",
      );
    } else {
      const pt =
        settings.margins.top != null ? `${settings.margins.top}px` : "24px";
      const pb =
        settings.margins.bottom != null
          ? `${settings.margins.bottom}px`
          : "24px";
      const ps =
        settings.margins.side != null
          ? `${settings.margins.side}px`
          : "max(24px, 5vw)";
      css.push(`body { padding: ${pt} ${ps} ${pb} ${ps} !important; }`);
      css.push(
        "#content > *, #content-inner > *, body > *:not(#paged-clip):not(#content):not(.boundary-indicator):not(#page-indicator):not(#boundary-indicator-top):not(#boundary-indicator-bottom) { padding-left: 0 !important; padding-right: 0 !important; margin-left: 0 !important; margin-right: 0 !important; }",
      );
      css.push(
        "#content > *:first-child, #content-inner > *:first-child { margin-top: 0 !important; padding-top: 0 !important; }",
      );
      css.push(
        "#content > *:last-child, #content-inner > *:last-child { margin-bottom: 0 !important; padding-bottom: 0 !important; }",
      );
      if (settings.contentWidth != null) {
        css.push(
          `#content { max-width: ${settings.contentWidth}% !important; margin-left: auto !important; margin-right: auto !important; }`,
        );
      }
    }

    if (!settings.preserveBookFonts) {
      css.push(
        `body, body * { font-family: ${settings.fontFamily} !important; }`,
      );
      // Code is meant to be monospaced: exempt it from the reader font and fall
      // back to the browser's default monospace face. Same specificity as the
      // `body *` rule above but emitted later, so it wins for code/pre subtrees.
      css.push(
        "body pre, body code, body kbd, body samp, body pre * { font-family: monospace !important; }",
      );
      // A chapter-title font override targets headings only. Emitted after the
      // `body *` rule above (same specificity) so it wins for h1–h6; still gated
      // on !preserveBookFonts so "use the book's fonts" wins over both.
      if (settings.chapterTitleFontFamily) {
        css.push(
          `h1, h2, h3, h4, h5, h6 { font-family: ${settings.chapterTitleFontFamily} !important; }`,
        );
      }
    }

    css.push(`body { font-size: ${settings.fontSize}px !important; }`);

    if (settings.lineHeight != null) {
      css.push(`body { line-height: ${settings.lineHeight} !important; }`);
    }
    if (settings.paragraphSpacing != null) {
      css.push(
        `p { margin-bottom: ${settings.paragraphSpacing}em !important; }`,
      );
    }
    if (settings.textIndent != null) {
      css.push(
        `p { text-indent: ${settings.textIndent}em !important; }`,
        "p:first-child, h1+p, h2+p, h3+p, h4+p, h5+p, h6+p, hr+p, blockquote+p, figure+p { text-indent: 0 !important; }",
      );
    }
    if (settings.letterSpacing != null) {
      // Body letter-spacing. Headings keep their own value: a heading's direct
      // letter-spacing rule (frame.css, or headingLetterSpacing below) beats
      // this inherited one, so the two controls stay independent.
      css.push(
        `body { letter-spacing: ${settings.letterSpacing}em !important; }`,
      );
    }

    if (settings.justify) {
      css.push("body { text-align: justify !important; }");
      css.push("h1, h2, h3, h4, h5, h6 { text-align: initial !important; }");
    }
    if (settings.hyphenation) {
      css.push(
        "body { hyphens: auto !important; -webkit-hyphens: auto !important; }",
      );
      css.push(
        "h1, h2, h3, h4, h5, h6 { hyphens: none !important; -webkit-hyphens: none !important; }",
      );
    } else {
      // Disabled must actually win. A preserved book stylesheet can set
      // `hyphens: auto`, so force `manual` (the CSS default) on the body and
      // every descendant — targeting descendants too so a book's own
      // `p { hyphens: auto }` can't keep hyphenating after the toggle is off.
      css.push(
        "body, body * { hyphens: manual !important; -webkit-hyphens: manual !important; }",
      );
    }

    if (settings.chapterTitleAlign != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { text-align: ${settings.chapterTitleAlign} !important; }`,
      );
    }
    if (settings.headingLetterSpacing != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { letter-spacing: ${settings.headingLetterSpacing}em !important; }`,
      );
    }
    if (settings.headerSizesEnabled) {
      // Per-heading override: each level sets its own size (null = leave that
      // level alone). While on, the single "Title size" is ignored.
      const perHeader: [string, number | null][] = [
        ["h1", settings.h1Size],
        ["h2", settings.h2Size],
        ["h3", settings.h3Size],
        ["h4", settings.h4Size],
        ["h5", settings.h5Size],
        ["h6", settings.h6Size],
      ];
      for (const [tag, size] of perHeader) {
        if (size != null) {
          css.push(`${tag} { font-size: ${size}px !important; }`);
        }
      }
    } else if (settings.chapterTitleSize != null) {
      // "Title size" applies to every heading level (h1–h6), not just the top
      // three, so the single slider scales all headers uniformly.
      css.push(
        `h1, h2, h3, h4, h5, h6 { font-size: ${settings.chapterTitleSize}px !important; }`,
      );
    }
    if (settings.chapterTitleSpacing != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { margin-bottom: ${settings.chapterTitleSpacing}em !important; }`,
      );
    }
    // Font weight: text weight applies to body copy; header weight to every
    // heading. When header weight is Auto we push NO heading rule, so headings
    // keep their natural weight from frame.css (h1–h6 { font-weight: 700 }) or
    // the book stylesheet. That direct rule already outranks the body weight
    // headings would otherwise inherit, so setting body weight alone no longer
    // drags titles down to normal. (A `revert` fallback here resolved to
    // normal in some engines, un-bolding chapter titles.)
    if (settings.textWeight != null) {
      css.push(`body { font-weight: ${settings.textWeight} !important; }`);
    }
    if (settings.headerWeight != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { font-weight: ${settings.headerWeight} !important; }`,
      );
    }

    // Custom themes have no static html.theme-<id> rule in frame.css, so the
    // parent sends the resolved palette as CSS custom properties to set on
    // <html>. Built-in themes send null and keep using their frame.css class
    // (which outranks a bare html selector anyway). Injected via override-css
    // (after base-css) so it wins the cascade for the classless custom id.
    if (settings.themeVars) {
      css.push(`html { ${settings.themeVars} }`);
    }

    const overrideCSS = css.join("\n");
    if (overrideCSS !== _lastOverrideCSS) {
      getStyleEl("override-css").textContent = overrideCSS;
      _lastOverrideCSS = overrideCSS;
    }

    // Paged mode is horizontal-tb only (see the axis invariant in
    // pagination.ts): a vertical-writing chapter orders multicol columns along
    // the vertical inline axis, so the paged math collapses to a single page and
    // the first turn reports at-boundary — paging would skip the chapter instead
    // of reading it. Fall back to scroll for vertical chapters, and derive the
    // root classes from the same effective mode so CSS can't claim a layout that
    // JS isn't driving.
    isPagedMode = effectiveMode === "paged" || effectiveMode === "paged-two";
    applyRootClasses(settings.theme, effectiveMode);
    reportEffectiveMode(effectiveMode, modeFallback);

    if (!isPagedMode) {
      // Paged mode insets the column box by setting inline height/marginTop on
      // the clip + content (see pagination.setPagedHeights). Clear them when
      // returning to scroll so they don't constrain or offset the scroll flow.
      const clip = getClipEl();
      const content = getContentEl();
      if (clip) {
        clip.style.height = "";
        clip.style.marginTop = "";
      }
      if (content) content.style.height = "";
      // Detach the paged resize observer/listener now that we're in scroll mode
      // so it doesn't sit attached firing no-op relayouts. A later scroll→paged
      // switch re-establishes it via pagination.relayout().
      cancelScheduledPagedRelayout();
      pagination.teardownResizeObserver();
      if (isModeSwitch) restoreScrollFromSwitch(switchAnchor, switchRatio);
    }

    if (!contentReady) {
      contentReady = true;
      runInitialLayoutRestore();
    } else if (isPagedMode) {
      if (isModeSwitch)
        pagination.enterPagedFromScroll(switchAnchor, switchRatio);
      else schedulePagedRelayout();
    }
  }

  // A jump that arrives while the fonts-gated restore is still queued must not
  // clear restorePending and must not promote contentReady itself. The DOM
  // still holds the previous chapter's offset, so reopening reports here
  // clobbers saved progress under the new seq (see restorePending above), and
  // faking readiness makes the pending apply-settings skip
  // runInitialLayoutRestore entirely -- stranding the restore percent/CFI and
  // the deferred search highlight, and leaving paged mode with totalPages 0.
  // Hand the target to the restore instead; it consumes both.
  function scrollToFragmentById(id: string): void {
    if (restorePending) {
      pendingFragment = id;
      return;
    }
    if (!contentReady) return;

    if (isPagedMode) {
      pagination.scrollToFragmentPaged(id);
      boundary.reset();
      return;
    }

    const el = document.getElementById(id);
    if (!el) return;

    el.scrollIntoView({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    boundary.reset();
  }

  // Jump to a previously reported CFI within the *current* chapter (e.g. a
  // bookmark in the chapter already on screen). Mirrors scrollToFragmentById
  // but resolves the position via the CFI path instead of an element id.
  function scrollToCfiLocal(cfi: string): void {
    // Same contract as scrollToFragmentById: defer into the pending restore
    // rather than dropping the guard or forcing readiness.
    if (restorePending) {
      loadRestoreCfi = cfi;
      return;
    }
    if (!contentReady) return;

    const el = resolveCFI(cfi, document);
    if (!el) return;

    if (isPagedMode) {
      pagination.goToElementPaged(el);
      boundary.reset();
      return;
    }

    el.scrollIntoView({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    boundary.reset();
  }

  function getScrollPercent(): number {
    const max = getScrollableMax();
    if (max <= 0) return 0;
    return Math.min(1, Math.max(0, getAxisScrollPos() / max));
  }

  function killScrollMomentum(): void {
    window.scrollTo({
      top: window.scrollY,
      left: window.scrollX,
      behavior: "instant" as ScrollBehavior,
    });
  }

  let scrollThrottleTimer: ReturnType<typeof setTimeout> | null = null;
  let scrollThrottlePending = false;

  // generateCFI indexes among all element siblings, so wrapping or unwrapping
  // search <mark>s can shift the path a cached CFI encodes. Drop the cache
  // whenever the chapter DOM is mutated under it.
  function invalidateAnchorCache(): void {
    lastVisibleBlock = null;
    lastReportedAnchor = null;
    lastReportedCfi = null;
  }

  // CFI for the block anchoring the current view, or the empty marker when none
  // resolves. Both modes report through this helper so a paged report can never
  // omit the field: an absent cfi means "keep the stored value" on the parent,
  // which would pin paged progress to the page the chapter opened on.
  function anchorCfiForCurrentView(): string {
    const el = findFirstVisibleBlock();
    if (!el) {
      lastReportedAnchor = null;
      lastReportedCfi = null;
      return NO_CFI;
    }
    if (el !== lastReportedAnchor) {
      lastReportedAnchor = el;
      lastReportedCfi = generateCFI(el, document);
    }
    return lastReportedCfi ?? NO_CFI;
  }

  function reportPosition(): void {
    if (restorePending) return;
    if (isPagedMode) {
      pagination.reportPagePosition();
      return;
    }

    sendMessage({
      type: "position",
      seq: activeSeq,
      chapterIndex: activeChapterIndex,
      percent: getScrollPercent(),
      cfi: anchorCfiForCurrentView(),
    });
  }

  function throttledReportPosition(): void {
    if (!scrollThrottleTimer) {
      reportPosition();
      scrollThrottlePending = false;
      scrollThrottleTimer = setTimeout(() => {
        scrollThrottleTimer = null;
        if (scrollThrottlePending) {
          scrollThrottlePending = false;
          reportPosition();
        }
      }, 200);
    } else {
      scrollThrottlePending = true;
    }
  }

  function handleScroll(): void {
    if (destroyed || isPagedMode || restorePending) return;
    // Scroll events can fire several times per frame during momentum scrolling;
    // coalesce the layout reads + position report into a single rAF tick.
    if (scrollRafHandle !== null) return;
    scrollRafHandle = requestAnimationFrame(() => {
      scrollRafHandle = null;
      if (destroyed || isPagedMode || restorePending) return;
      updateBoundaryState();
      throttledReportPosition();
      if (!atTop && !atBottom && boundary.hasActiveDirection())
        boundary.reset();
    });
  }

  // Wheel travel in reading order, forward-positive. A vertical chapter scrolls
  // along X, where a trackpad emits deltaX for the same gesture that emits
  // deltaY in a horizontal one; vertical-rl reads right to left, so rightward
  // travel goes backwards. Chromium maps a classic wheel's deltaY onto whatever
  // axis actually scrolls, so that component is already forward-positive.
  function readingAxisWheelDelta(e: WheelEvent): number {
    if (!verticalWriting) return e.deltaY;
    if (Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
      return verticalWriting === "rl" ? -e.deltaX : e.deltaX;
    }
    return e.deltaY;
  }

  function handleWheel(e: WheelEvent): void {
    if (destroyed || !contentReady || restorePending) return;
    if (isPagedMode) return;
    // Sum synchronously, read layout and paint the indicator once per frame:
    // updateBoundaryState() reads scrollHeight and boundary.* then writes
    // indicator styles, so running both per event forces a reflow per tick.
    wheelPullDelta += readingAxisWheelDelta(e);
    if (wheelPullRafHandle !== null) return;
    wheelPullRafHandle = requestAnimationFrame(flushWheelPull);
  }

  function flushWheelPull(): void {
    wheelPullRafHandle = null;
    const delta = wheelPullDelta;
    wheelPullDelta = 0;
    if (destroyed || !contentReady || restorePending || isPagedMode) return;

    updateBoundaryState();
    if (atTop && delta < 0) {
      boundary.accumulate(Math.abs(delta), "start", WHEEL_THRESHOLD);
      return;
    }
    if (atBottom && delta > 0) {
      boundary.accumulate(Math.abs(delta), "end", WHEEL_THRESHOLD);
      return;
    }
    if (boundary.hasActiveDirection()) boundary.reset();
  }

  function handleTouchStart(e: TouchEvent): void {
    // Arming during the restore would capture the OUTGOING chapter's offset in
    // touchAtBoundaryOnStart; handleTouchMove skips until the guard clears and
    // would then measure the pull against that stale base.
    if (destroyed || !contentReady || restorePending) return;
    if (e.touches.length !== 1) return;
    touchStartY = e.touches[0].clientY;
    touchStartX = e.touches[0].clientX;
    touchLastX = touchStartX;
    touchLastY = touchStartY;
    touchTracking = true;
    // Re-arms the one-hand-off-per-gesture latch, and backstops a touchend the
    // browser never delivers (gesture stolen, tab hidden mid-drag).
    boundary.endGesture();
    touchBoundaryBase = 0;
    updateBoundaryState();
    touchAtBoundaryOnStart = atTop || atBottom;
  }

  function handleTouchMove(e: TouchEvent): void {
    if (destroyed || !touchTracking || restorePending) return;
    if (e.touches.length !== 1) return;
    touchLastX = e.touches[0].clientX;
    touchLastY = e.touches[0].clientY;
    if (isPagedMode) return;
    // Same coalescing as the wheel path; the flush reads the latest touch point.
    if (touchPullRafHandle !== null) return;
    touchPullRafHandle = requestAnimationFrame(flushTouchPull);
  }

  function flushTouchPull(): void {
    touchPullRafHandle = null;
    if (destroyed || !touchTracking || restorePending || isPagedMode) return;

    // Pull distance in reading order, forward-positive. A vertical chapter
    // flows along X, so the boundary pull is a horizontal drag: vertical-rl
    // advances as the finger moves right (the next content sits to the left),
    // vertical-lr as it moves left. Measuring Y here rejected every vertical
    // pull as an off-axis pan, which left touch chapter hand-off unreachable.
    const alongFlow = verticalWriting
      ? verticalWriting === "rl"
        ? touchLastX - touchStartX
        : touchStartX - touchLastX
      : touchStartY - touchLastY;
    const acrossFlow = verticalWriting
      ? Math.abs(touchLastY - touchStartY)
      : Math.abs(touchLastX - touchStartX);
    if (acrossFlow > Math.abs(alongFlow) * 0.7) return;

    updateBoundaryState();

    if (atBottom && alongFlow > 0) {
      if (touchBoundaryBase === 0) {
        touchBoundaryBase = touchAtBoundaryOnStart ? 0 : alongFlow;
      }
      const boundaryDelta = alongFlow - touchBoundaryBase;
      if (boundaryDelta > 0) {
        boundary.processTouch("end", boundaryDelta);
      }
      return;
    }

    if (atTop && alongFlow < 0) {
      if (touchBoundaryBase === 0) {
        touchBoundaryBase = touchAtBoundaryOnStart ? 0 : alongFlow;
      }
      const boundaryDelta = Math.abs(alongFlow - touchBoundaryBase);
      if (boundaryDelta > 0) {
        boundary.processTouch("start", boundaryDelta);
      }
      return;
    }

    touchBoundaryBase = 0;
  }

  function handleTouchEnd(): void {
    if (destroyed || !touchTracking) return;
    touchTracking = false;

    if (isPagedMode) {
      const dx = touchStartX - touchLastX;
      const dy = Math.abs(touchStartY - touchLastY);
      if (Math.abs(dx) >= 50 && Math.abs(dx) > dy * 0.7) {
        if (dx > 0) {
          pagination.isRTL() ? pagination.prevPage() : pagination.nextPage();
        } else {
          pagination.isRTL() ? pagination.nextPage() : pagination.prevPage();
        }
      }
      return;
    }

    touchBoundaryBase = 0;
    touchAtBoundaryOnStart = false;
    boundary.endGesture();
    if (!boundary.isSent()) boundary.reset();
  }

  // A cancelled gesture (system gesture, incoming call, scroll takeover) never
  // reaches handleTouchEnd, which left touchTracking armed and the boundary
  // latch held until the next touchstart.
  function handleTouchCancel(): void {
    if (destroyed || !touchTracking) return;
    touchTracking = false;
    touchBoundaryBase = 0;
    touchAtBoundaryOnStart = false;
    boundary.endGesture();
    if (!boundary.isSent()) boundary.reset();
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (destroyed) return;
    // This is the last boundary that still has the real target and composition
    // state. Never forward or suppress a key owned by editable/native content.
    if (keyboardEventIsOwnedByTarget(e, document.activeElement)) return;

    // !e.altKey: AltGr arrives as ctrl+alt on Windows and most Linux
    // layouts, where it types an ordinary character — suppressing the default
    // here would kill that keystroke (a chapter can carry a contenteditable
    // region: the sanitizer's attribute policy is a denylist, so the attribute
    // survives into the frame). The paged-scroll suppression below already
    // excludes altKey; the parent's palette branch (Read.tsx handleKeyAction)
    // carries the same conjunct.
    const isPaletteShortcut =
      (e.ctrlKey || e.metaKey) && !e.altKey && (e.key === "k" || e.key === "K");
    if (isPaletteShortcut) e.preventDefault();

    if (!contentReady) {
      sendMessage({
        type: "key",
        seq: activeSeq,
        key: e.key,
        code: e.code,
        ctrlKey: e.ctrlKey,
        shiftKey: e.shiftKey,
        altKey: e.altKey,
        metaKey: e.metaKey,
      });
      return;
    }

    if (isPagedMode) {
      // The parent drives paged navigation from this forwarded key. Suppress
      // the browser's native scroll/snap for scrolling keys so it does not move
      // #content underneath the JS page-turn cross-fade — that native scroll
      // racing the fade is what made ARROW-KEY turns flicker while edge-clicks
      // (which never scroll natively) stayed smooth.
      if (
        !e.ctrlKey &&
        !e.metaKey &&
        !e.altKey &&
        PAGED_SCROLL_KEYS.has(e.key)
      ) {
        e.preventDefault();
      }
      sendMessage({
        type: "key",
        seq: activeSeq,
        key: e.key,
        code: e.code,
        ctrlKey: e.ctrlKey,
        shiftKey: e.shiftKey,
        altKey: e.altKey,
        metaKey: e.metaKey,
      });
      return;
    }

    updateBoundaryState();
    let handled = false;

    if (atTop && (e.key === "ArrowUp" || e.key === "PageUp")) {
      if (hasPrevChapter) {
        sendMessage({ type: "at-boundary", seq: activeSeq, boundary: "start" });
      } else {
        // Nothing to hand off to: flash the same end-stop the pull gestures
        // show, so the key press is acknowledged instead of swallowed.
        boundary.flashEdge("start");
      }
      handled = true;
    } else if (
      atBottom &&
      (e.key === "ArrowDown" || e.key === "PageDown" || e.key === " ")
    ) {
      if (e.key !== " " || !e.shiftKey) {
        const scrollMax = getScrollableMax();
        if (scrollMax <= 0 || getAxisScrollPos() >= scrollMax - 1) {
          if (hasNextChapter) {
            sendMessage({
              type: "at-boundary",
              seq: activeSeq,
              boundary: "end",
            });
          } else {
            boundary.flashEdge("end");
          }
          handled = true;
        }
      }
    }

    sendMessage({
      type: "key",
      seq: activeSeq,
      key: e.key,
      code: e.code,
      ctrlKey: e.ctrlKey,
      shiftKey: e.shiftKey,
      altKey: e.altKey,
      metaKey: e.metaKey,
    });

    if (handled) e.preventDefault();
  }

  function handleClick(e: MouseEvent): void {
    if (destroyed) return;

    const vw = Number.isFinite(window.innerWidth) ? window.innerWidth : 0;
    const x = e.clientX;
    const region: "left" | "center" | "right" =
      vw > 0 && x < vw / 3
        ? "left"
        : vw > 0 && x > (vw * 2) / 3
          ? "right"
          : "center";
    const anchor = (e.target as HTMLElement).closest("a");

    if (!anchor) {
      sendMessage({ type: "click", seq: activeSeq, region });
      return;
    }

    const href = anchor.getAttribute("href");
    if (!href) {
      sendMessage({ type: "click", seq: activeSeq, region });
      return;
    }

    e.preventDefault();

    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(href)?.[1]?.toLowerCase();
    if (scheme) {
      if (EXTERNAL_BOOK_LINK_SCHEMES.has(scheme)) {
        window.open(href, "_blank", "noopener,noreferrer");
      }
      // Every explicit scheme is terminal here: four are deliberately opened,
      // while script, data, file, ftp, and unknown schemes fail closed instead
      // of leaking into in-book resolution.
      return;
    }

    if (href.startsWith("#")) {
      scrollToFragmentById(decodeHrefComponent(href.slice(1)));
      return;
    }

    sendMessage({ type: "link-clicked", seq: activeSeq, href });
  }

  const searchHl = createSearchHighlight({
    getContentEl,
    isContentReady: () => contentReady,
    isPagedMode: () => isPagedMode,
    goToPageInternal: pagination.goToPage,
    getElementPageIndex: pagination.getElementPageIndex,
  });

  function commitLoad(msg: LoadMessage): void {
    activeSeq = msg.seq;
    activeChapterIndex = msg.chapterIndex;
    rawBookCSS = msg.css || "";
    bookFontFaceCSS = msg.fontFaceCSS || "";
    prepareChapterCSS();

    contentReady = false;
    restorePending = true;
    pendingSearchHighlight = null;
    isPagedMode = false;
    pagination.resetForLoad(msg.direction === "rtl");

    // A trailing throttled report (or in-flight rAF) armed on the previous
    // chapter must not fire under the new seq with the old scroll offset.
    if (scrollThrottleTimer) {
      clearTimeout(scrollThrottleTimer);
      scrollThrottleTimer = null;
    }
    scrollThrottlePending = false;
    if (scrollRafHandle !== null) {
      cancelAnimationFrame(scrollRafHandle);
      scrollRafHandle = null;
    }

    // Conditional write, not an early return: the rest of commitLoad does not
    // depend on the element, and throwing here would abandon the load halfway.
    const contentEl = getContentEl();
    if (contentEl) contentEl.scrollLeft = 0;

    loadScrollTarget = msg.scrollTo || "top";
    pendingFragment = msg.fragment || null;
    hasNextChapter = msg.hasNext !== false;
    hasPrevChapter = msg.hasPrev !== false;
    loadRestorePercent = Number.isFinite(msg.restorePercent)
      ? Math.min(1, Math.max(0, msg.restorePercent as number))
      : null;
    loadRestoreCfi =
      typeof msg.restoreCfi === "string" && msg.restoreCfi
        ? msg.restoreCfi
        : null;

    invalidateAnchorCache();

    if (revealFallbackTimer !== null) {
      clearTimeout(revealFallbackTimer);
      revealFallbackTimer = null;
    }

    beginChapterSwapOut();

    const rawContentInnerEl = document.getElementById("content-inner");
    if (!rawContentInnerEl) {
      // content-inner is guaranteed by the HTML template; this path should
      // never fire. If it does, restore visibility and notify the parent so it
      // can reload rather than leaving the frame permanently frozen.
      document.body.style.opacity = "1";
      document.documentElement.style.overflow = "";
      sendMessage({
        type: "load-error",
        seq: activeSeq,
        error: "content-inner element not found",
      });
      return;
    }

    rawContentInnerEl.innerHTML = absolutifyHTML(msg.html || "");
    reserveSearchMarkAttribute(rawContentInnerEl);
    if (msg.direction) document.documentElement.dir = msg.direction;
    // Applied unconditionally so a vertical chapter can never leak its writing
    // mode into a following horizontal one. Vertical modes flip all scroll
    // math to the horizontal axis via verticalWriting (see getAxisScrollPos).
    {
      const raw = typeof msg.writingMode === "string" ? msg.writingMode : "";
      // Allowlisted before it reaches the DOM, matching the lang sanitization
      // below. The value originates in book CSS, and the backend closes the set
      // to horizontal-tb / vertical-rl / vertical-lr (internal/epub/chapter.go);
      // the tb-* aliases are kept so no previously accepted value regresses.
      const wm = /^(horizontal-tb|vertical-rl|vertical-lr|tb-rl|tb-lr)$/.test(
        raw,
      )
        ? raw
        : "";
      document.documentElement.style.writingMode =
        wm && wm !== "horizontal-tb" ? wm : "";
      verticalWriting = /vertical-rl|tb-rl/.test(wm)
        ? "rl"
        : /vertical-lr|tb-lr/.test(wm)
          ? "lr"
          : "";
      // The pull affordance follows the flow axis (see boundary.ts); frame.css
      // keys the inline-edge placement off these root classes.
      document.documentElement.classList.toggle(
        "vertical-rl",
        verticalWriting === "rl",
      );
      document.documentElement.classList.toggle(
        "vertical-lr",
        verticalWriting === "lr",
      );
    }
    if (typeof msg.language === "string" && msg.language) {
      // Sanitize to BCP-47-ish chars before reflecting into the DOM lang attr.
      const safeLang = msg.language.replace(/[^a-zA-Z0-9-]/g, "").slice(0, 35);
      if (safeLang) document.documentElement.lang = safeLang;
    }

    // The load carries a complete settings snapshot. A live settings update
    // received during the swap overrides it, but a missing companion message
    // can no longer strand the chapter hidden.
    const settings = pendingSettingsMessage ?? msg.settings;
    pendingSettingsMessage = null;
    applySettings(settings);
    releaseOverflowAfterSettings();
  }

  function cleanupFrame(): void {
    if (destroyed) return;
    destroyed = true;

    boundary.dispose();
    pagination.dispose();
    if (scrollThrottleTimer) {
      clearTimeout(scrollThrottleTimer);
      scrollThrottleTimer = null;
      scrollThrottlePending = false;
    }
    if (revealFallbackTimer !== null) {
      clearTimeout(revealFallbackTimer);
      revealFallbackTimer = null;
    }
    if (loadCommitTimer !== null) {
      clearTimeout(loadCommitTimer);
      loadCommitTimer = null;
    }
    if (chapterAnimTimer !== null) {
      clearTimeout(chapterAnimTimer);
      chapterAnimTimer = null;
    }
    pendingSettingsMessage = null;
    pendingSearchHighlight = null;
    if (reportPositionRafHandle !== null) {
      cancelAnimationFrame(reportPositionRafHandle);
      reportPositionRafHandle = null;
    }
    if (scrollRafHandle !== null) {
      cancelAnimationFrame(scrollRafHandle);
      scrollRafHandle = null;
    }
    if (wheelPullRafHandle !== null) {
      cancelAnimationFrame(wheelPullRafHandle);
      wheelPullRafHandle = null;
    }
    if (touchPullRafHandle !== null) {
      cancelAnimationFrame(touchPullRafHandle);
      touchPullRafHandle = null;
    }
    wheelPullDelta = 0;
    cancelScheduledPagedRelayout();
    if (rasterRefreshRafHandle !== null) {
      cancelAnimationFrame(rasterRefreshRafHandle);
      rasterRefreshRafHandle = null;
    }
    // chapter-anim is transient like raster-refresh; drop both so teardown
    // never leaves a will-change promotion behind.
    document.documentElement.classList.remove("raster-refresh", "chapter-anim");

    window.removeEventListener("message", handleMessage);
    document.removeEventListener("click", handleClick);
    window.removeEventListener("scroll", handleScroll);
    window.removeEventListener("wheel", handleWheel);
    document.removeEventListener("touchstart", handleTouchStart);
    document.removeEventListener("touchmove", handleTouchMove);
    document.removeEventListener("touchend", handleTouchEnd);
    document.removeEventListener("touchcancel", handleTouchCancel);
    document.removeEventListener("keydown", handleKeyDown);
  }

  function acceptParentMessage(event: MessageEvent): boolean {
    if (destroyed) return false;
    if (event.source !== window.parent) return false;

    if (parentOrigin) {
      return event.origin === parentOrigin;
    }

    if (
      typeof event.origin === "string" &&
      event.origin &&
      event.origin !== "null"
    ) {
      parentOrigin = event.origin;
      return true;
    }

    return false;
  }

  // Zoom is a compositor-only rescale of this frame's whole surface: nothing in
  // here is invalidated, so the text keeps the raster it was drawn at and reads
  // blurry (dragging a selection over a word used to be the only way to force a
  // redraw). Dirty paint for two frames with a class, not an inline style, since
  // the page-turn and reveal fades already own #content and body opacity.
  function refreshRaster(): void {
    if (destroyed) return;
    const root = document.documentElement;
    if (rasterRefreshRafHandle !== null) {
      cancelAnimationFrame(rasterRefreshRafHandle);
    }
    root.classList.add("raster-refresh");
    rasterRefreshRafHandle = requestAnimationFrame(() => {
      rasterRefreshRafHandle = requestAnimationFrame(() => {
        rasterRefreshRafHandle = null;
        root.classList.remove("raster-refresh");
      });
    });
  }

  // Positional commands are valid only for the committed chapter. Reject both
  // stale and future sequences: the controller holds current-load commands
  // until `loaded`, and a future command must never touch the outgoing DOM.
  function isStaleCommand(seq: number): boolean {
    return seq !== activeSeq;
  }

  function handleMessage(e: MessageEvent): void {
    const raw: unknown = e.data;
    if (!raw || typeof (raw as Record<string, unknown>).type !== "string")
      return;
    if (!acceptParentMessage(e)) return;
    const msg = raw as ParentToFrameMessage;

    switch (msg.type) {
      case "destroy":
        cleanupFrame();
        break;

      case "refresh-raster":
        refreshRaster();
        break;

      case "set-font-faces":
        if (typeof msg.fontFaces === "string") readerFontFaces = msg.fontFaces;
        break;

      case "load": {
        if (typeof msg.origin === "string" && msg.origin) {
          if (e.origin !== msg.origin) return;
          parentOrigin = msg.origin;
        }

        if (loadCommitTimer !== null) {
          clearTimeout(loadCommitTimer);
          loadCommitTimer = null;
        }

        pendingSettingsMessage = null;
        const transitionToken = ++loadTransitionToken;
        // Disarm the outgoing chapter's reveal here rather than at commitLoad:
        // the swap-out fade starts now, and the fallback would otherwise fire
        // mid-transition and un-hide the chapter being replaced.
        if (revealFallbackTimer !== null) {
          clearTimeout(revealFallbackTimer);
          revealFallbackTimer = null;
        }
        beginChapterSwapOut();

        const delay = shouldAnimateChapterSwap() ? CHAPTER_SWAP_OUT_MS : 0;
        loadCommitTimer = setTimeout(() => {
          if (loadCommitTimer) {
            clearTimeout(loadCommitTimer);
            loadCommitTimer = null;
          }
          if (destroyed || transitionToken !== loadTransitionToken) return;
          commitLoad(msg);
        }, delay);
        break;
      }

      case "apply-settings":
        if (loadCommitTimer !== null) {
          pendingSettingsMessage = msg.settings;
          break;
        }

        applySettings(msg.settings);
        releaseOverflowAfterSettings();
        break;

      case "scroll-to":
        if (isStaleCommand(msg.seq)) break;
        if (contentReady) {
          const max = getScrollableMax();
          const pct = Number.isFinite(msg.percent)
            ? Math.min(1, Math.max(0, msg.percent))
            : 0;
          // Axis-aware: in vertical-writing scroll mode the flow axis is X.
          axisScrollTo(max * pct);
          boundary.reset();
          updateBoundaryState();
        }
        break;

      case "scroll-to-end":
        if (isStaleCommand(msg.seq)) break;
        if (contentReady) {
          const max = getScrollableMax();
          // Axis-aware: in vertical-writing scroll mode the flow axis is X.
          axisScrollTo(max);
          boundary.reset();
          updateBoundaryState();
        }
        break;

      case "next-page":
        if (isStaleCommand(msg.seq)) break;
        if (isPagedMode) pagination.nextPage();
        break;

      case "prev-page":
        if (isStaleCommand(msg.seq)) break;
        if (isPagedMode) pagination.prevPage();
        break;

      case "scroll-to-fragment":
        if (isStaleCommand(msg.seq)) break;
        if (typeof msg.id === "string") scrollToFragmentById(msg.id);
        break;

      case "scroll-to-cfi":
        if (isStaleCommand(msg.seq)) break;
        if (typeof msg.cfi === "string") scrollToCfiLocal(msg.cfi);
        break;

      case "get-position":
        reportPosition();
        break;

      case "highlight-search":
        if (
          !Number.isSafeInteger(msg.seq) ||
          typeof msg.charOffset !== "number" ||
          typeof msg.matchLen !== "number" ||
          typeof msg.query !== "string" ||
          !Number.isSafeInteger(msg.charOffset) ||
          !Number.isSafeInteger(msg.matchLen) ||
          msg.charOffset < 0 ||
          msg.matchLen <= 0 ||
          !Number.isSafeInteger(msg.charOffset + msg.matchLen)
        ) {
          break;
        }
        // Drop highlights computed for a superseded chapter load. A deferred
        // cross-chapter highlight can arrive after a faster re-navigation has
        // already swapped in a different chapter; without this guard it would
        // mark the wrong text.
        if (msg.seq !== activeSeq) break;
        // Defer while the initial restore is still pending too: running the
        // highlight scroll first would be undone moments later by the
        // fonts-gated restore-to-position (drained after the restore).
        if (!contentReady || restorePending) {
          pendingSearchHighlight = {
            charOffset: msg.charOffset,
            matchLen: msg.matchLen,
            query: msg.query,
            seq: msg.seq,
          };
          break;
        }
        invalidateAnchorCache();
        searchHl.highlightSearchMatch(msg.charOffset, msg.matchLen, msg.query);
        break;

      case "clear-highlights":
        pendingSearchHighlight = null;
        invalidateAnchorCache();
        searchHl.clearSearchHighlights();
        break;

      default: {
        // Exhaustiveness guard: a new ParentToFrameMessage kind that is not
        // handled above will fail type-checking here.
        const _exhaustive: never = msg;
        void _exhaustive;
      }
    }
  }

  window.addEventListener("message", handleMessage);
  document.addEventListener("click", handleClick);
  window.addEventListener("scroll", handleScroll, { passive: true });
  window.addEventListener("wheel", handleWheel, { passive: true });
  document.addEventListener("touchstart", handleTouchStart, { passive: true });
  document.addEventListener("touchmove", handleTouchMove, { passive: true });
  document.addEventListener("touchend", handleTouchEnd, { passive: true });
  document.addEventListener("touchcancel", handleTouchCancel, {
    passive: true,
  });
  document.addEventListener("keydown", handleKeyDown);

  sendMessage({ type: "ready" });
})();
