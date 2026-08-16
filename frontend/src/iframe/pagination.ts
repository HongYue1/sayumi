import type { FrameToParentMessage } from "~/lib/frameMessages";
import { prefersReducedMotion } from "./reduceMotion";

// Page-turn cross-fade timing. The paged view is one multicol scroller, so a
// turn fades out, swaps scrollLeft while invisible, then fades back in (see
// applyPageScroll). Fast + asymmetric on purpose — a quick dip out, a gentler
// reveal in — so it reads as a smooth fade, not the slow symmetric blink the
// old ~155ms/phase value produced.
const PAGE_TURN_FADE_OUT_MS = 70;
const PAGE_TURN_FADE_IN_MS = 110;
// Cap on the per-frame opacity delta. The step is time-based (so a retargeted
// turn resumes from the current opacity), but the FIRST frame after
// setPageTurning() promotes #content to its own layer (will-change: opacity)
// can be delayed enough that an unclamped step exceeds 1 and collapses the
// entire fade-out into a single frame — the turn then reads as an instant cut
// with no fade. Clamping guarantees the fade always spans several frames
// regardless of frame pacing. 0.2 => at least ~5 frames per phase.
const MAX_FADE_STEP = 0.2;
// Minimum bottom inset for the paged column box so the last line never sits
// under the fixed #page-indicator pill (bottom: 12px + pill height).
const PAGE_INDICATOR_CLEARANCE = 32;

export type PaginationDeps = {
  /** The paged multicol scroller (#content); null before the shell exists. */
  getContentEl: () => HTMLElement | null;
  /** The paged viewport clip (#paged-clip); null before the shell exists. */
  getClipEl: () => HTMLElement | null;
  sendMessage: (msg: FrameToParentMessage) => void;
  getActiveSeq: () => number;
  getActiveChapterIndex: () => number;
  isDestroyed: () => boolean;
  isContentReady: () => boolean;
  /**
   * True while a chapter's position restore is still pending — position
   * reports are suppressed so they can't clobber saved progress. Required, not
   * optional: a caller that forgets it silently re-enables those reports.
   */
  isRestorePending: () => boolean;
  /**
   * CFI for the block anchoring the current page, or the explicit empty marker
   * when none resolves. Every position report must carry one (see
   * iframe/AGENTS.md): the parent reads an ABSENT cfi field as "keep the stored
   * value", so omitting it freezes saved progress at the CFI the chapter opened
   * with while percent keeps advancing past it.
   */
  getPositionCfi: () => string;
  isPagedMode: () => boolean;
  hasNextChapter: () => boolean;
  hasPrevChapter: () => boolean;
  setChapterHidden: (hidden: boolean) => void;
  ensureBoundaryElements: () => void;
  /**
   * Show the "beginning/end of book" end-stop. A paged turn has no pull
   * gesture behind it, so without this the first/last page of the book
   * absorbs the turn with no feedback at all.
   */
  flashBoundaryEdge: (direction: "start" | "end") => void;
  updateBoundaryState: () => void;
  takePendingFragment: () => string | null;
};

export type PaginationController = {
  nextPage: () => void;
  prevPage: () => void;
  goToPage: (page: number, animated: boolean) => void;
  getElementPageIndex: (el: Element) => number;
  /** Turn to the page holding `id`; false when the anchor isn't in this chapter. */
  scrollToFragmentPaged: (id: string, animated?: boolean) => boolean;
  goToElementPaged: (el: Element, animated?: boolean) => void;
  relayout: () => void;
  restorePagedPosition: (
    scrollTarget: "top" | "end",
    restorePercent: number | null,
    restoreElement: Element | null,
  ) => void;
  reportPagePosition: () => void;
  setPageTurning: (turning: boolean) => void;
  isRTL: () => boolean;
  resetForLoad: (rtl: boolean) => void;
  /** Detach the paged resize observer/listener without touching page-turn state (used when switching to scroll mode). */
  teardownResizeObserver: () => void;
  dispose: () => void;
};

// ── Pure page geometry ─────────────────────────────────────────────────────
// Exported for tests: these encode the multicol contract (stride = column box
// + gap, page origins are stride-aligned, RTL runs on negative DOM offsets)
// that the controller reads layout for. Keeping them DOM-free is what makes
// the off-by-one edges testable without a browser.

/** Stride between page origins: the column box plus the multicol column-gap. */
export function pageStrideFrom(width: number, gap: number): number {
  const stride = width + (Number.isFinite(gap) ? gap : 0);
  return Number.isFinite(stride) ? Math.max(1, stride) : 1;
}

/**
 * Page count from the scroller's max scroll offset. Round (not ceil) so
 * sub-pixel column rounding can't invent a phantom trailing page; +1 turns the
 * last page index into a count.
 */
export function pageCountFrom(maxScrollLeft: number, stride: number): number {
  if (!(stride > 0) || !Number.isFinite(maxScrollLeft)) return 1;
  return Math.max(1, Math.round(maxScrollLeft / stride) + 1);
}

export function clampPage(page: number, totalPages: number): number {
  if (!Number.isFinite(page)) return 0;
  return Math.max(0, Math.min(totalPages - 1, page));
}

/** Logical scroll offset for a page, clamped so the last page lands exactly. */
export function logicalOffsetForPage(
  page: number,
  stride: number,
  maxScrollLeft: number,
): number {
  const raw = page * stride;
  if (!Number.isFinite(raw)) return 0;
  return Math.max(0, Math.min(raw, maxScrollLeft));
}

/** Inverse of logicalOffsetForPage: the page containing a logical offset. */
export function pageAtOffset(
  x: number,
  stride: number,
  totalPages: number,
): number {
  if (!Number.isFinite(x) || !(stride > 0)) return 0;
  return clampPage(Math.floor(x / stride), totalPages);
}

export function pageForRatio(ratio: number, totalPages: number): number {
  if (!Number.isFinite(ratio) || totalPages <= 1) return 0;
  return clampPage(Math.round(ratio * (totalPages - 1)), totalPages);
}

export function pagePercent(page: number, totalPages: number): number {
  return totalPages > 1 && Number.isFinite(page) ? page / (totalPages - 1) : 0;
}

/**
 * An element's logical (positive, reading-order) x inside the multicol
 * scroller. RTL columns advance left from scrollLeft=0 using negative DOM
 * offsets, so measure from the right edge and flip the scroll sign back.
 */
export function elementLogicalX(m: {
  containerLeft: number;
  containerRight: number;
  elementLeft: number;
  elementRight: number;
  domScrollLeft: number;
  rtl: boolean;
}): number {
  return m.rtl
    ? m.containerRight - m.elementRight - m.domScrollLeft
    : m.elementLeft - m.containerLeft + m.domScrollLeft;
}

// Paged (column) reading mode: page geometry, the cross-fade page turn, paged
// fragment/position restore, the resize-driven relayout, and the page-number
// indicator. Pulled out of frame.ts behind a createPagination(deps) factory so
// frame.ts keeps only mode orchestration (isPagedMode flag, chapter swap,
// settings, lifecycle); deps inject the small slice of frame state it needs.
//
// AXIS INVARIANT — horizontal-tb only. Every metric here (scrollLeft,
// clientWidth, scrollWidth, column-gap) is physical-X, but a vertical-writing
// chapter orders multicol columns along the INLINE axis (vertical) and
// overflows along the block axis: those reads then measure the wrong axis,
// maxPageScrollLeft collapses to ~0, totalPages pins to 1, and the first
// nextPage() reports at-boundary — the reader skips the chapter instead of
// paging it. frame.ts keeps isPagedMode false for vertical chapters; never
// drive this controller while verticalWriting is set.
export function createPagination(deps: PaginationDeps): PaginationController {
  let currentPage = 0;
  let totalPages = 0;
  let isRTL = false;
  let pagedResizeObserver: ResizeObserver | null = null;
  let pagedResizeDebounce: ReturnType<typeof setTimeout> | null = null;
  // Viewport dims (innerWidth/innerHeight) at the last pagination. Lets the
  // resize handler skip relayouts that don't change the viewport box.
  // -1 = nothing laid out yet, so the next relayout always runs.
  let lastLayoutW = -1;
  let lastLayoutH = -1;
  let pageStride = 1;
  let maxPageScrollLeft = 0;
  let pageScrollRafHandle: number | null = null;
  let pageTurnFinishTimer: ReturnType<typeof setTimeout> | null = null;
  // Live target/phase for the cross-fade turn, so rapid presses can retarget
  // the running animation instead of restarting it.
  let pageTurnTarget = 0;
  let pageTurnSwapped = false;
  let pageTurningActive = false;
  let _pageIndicator: HTMLElement | null = null;
  let pageIndicatorText = "";
  let fontRelayoutToken = 0;
  // Element the current page was resolved from (restore/fragment only). A
  // relayout re-derives the page from it so a font-driven repagination lands on
  // the same content instead of a ratio-rounded neighbour. Any later page
  // change clears it, since from then on the ratio is the honest estimate.
  let lastAnchorEl: Element | null = null;

  function ensurePageIndicator(): HTMLElement {
    if (!_pageIndicator) {
      const el = document.createElement("div");
      el.id = "page-indicator";
      document.body.appendChild(el);
      _pageIndicator = el;
    }
    return _pageIndicator;
  }

  function updatePageIndicator(): void {
    const text = totalPages > 0 ? `${currentPage + 1} / ${totalPages}` : "";
    if (text === pageIndicatorText) return;
    pageIndicatorText = text;
    ensurePageIndicator().textContent = text;
  }

  function setPageTurning(turning: boolean): void {
    const enabled = turning && deps.isPagedMode();
    if (enabled === pageTurningActive) return;
    pageTurningActive = enabled;
    document.documentElement.classList.toggle("page-turning", enabled);
    if (!enabled && pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
  }

  function getPageStride(): number {
    const content = deps.getContentEl();
    const width = content?.clientWidth || window.innerWidth || 1;
    // The stride between page origins is the container width PLUS the multicol
    // column-gap (two-page mode uses a 1px gap): CSS lays column i at
    // i * (colWidth + gap), so a spread advances by width + gap. Using bare
    // width drifted 1px per spread — ~40px of bleed/clipping by the end of a
    // long chapter. getComputedStyle here is fine: this only runs inside
    // refreshPageMetrics, which already reads layout.
    let gap = 0;
    if (content) {
      const parsed = parseFloat(getComputedStyle(content).columnGap);
      if (Number.isFinite(parsed)) gap = parsed;
    }
    return pageStrideFrom(width, gap);
  }

  function getMaxPageScrollLeft(): number {
    const content = deps.getContentEl();
    if (!content) return 0;
    const max = content.scrollWidth - content.clientWidth;
    return Number.isFinite(max) ? Math.max(0, max) : 0;
  }

  function refreshPageMetrics(): void {
    pageStride = getPageStride();
    maxPageScrollLeft = getMaxPageScrollLeft();
  }

  // currentPage and maxPageScrollLeft stay logical and positive in reading
  // order. Browsers expose RTL scroll containers with zero at the right edge
  // and negative scrollLeft values toward the left, so translate only at the
  // DOM boundary instead of spreading sign handling through page math.
  function toDOMScrollLeft(logicalOffset: number): number {
    return isRTL ? -logicalOffset : logicalOffset;
  }

  function calculateTotalPages(): number {
    const content = deps.getContentEl();
    if (!content) return 1;

    refreshPageMetrics();
    if (pageStride <= 0) return 1;

    // Pages are stride-aligned scroll positions, so the last page begins at
    // maxScrollLeft (see pageCountFrom). Reads two layout metrics instead of
    // walking the DOM for the last meaningful rect on every relayout.
    return pageCountFrom(maxPageScrollLeft, pageStride);
  }

  function reportPagePosition(): void {
    // A relayout triggered by settings arriving mid-load must not report the
    // pre-restore page (percent 0) under the new seq; the restore reports.
    if (deps.isRestorePending()) return;
    deps.sendMessage({
      type: "position",
      seq: deps.getActiveSeq(),
      chapterIndex: deps.getActiveChapterIndex(),
      percent: pagePercent(currentPage, totalPages),
      // Always send a CFI (or the empty marker). An absent field means "keep
      // the stored one" on the parent, which pins paged progress to the CFI the
      // chapter opened with while percent advances past it.
      cfi: deps.getPositionCfi(),
    });
  }

  function applyPageScroll(page: number, animated: boolean): void {
    const content = deps.getContentEl();
    if (!content) return;

    const target = toDOMScrollLeft(
      logicalOffsetForPage(page, pageStride, maxPageScrollLeft),
    );

    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }

    if (!animated || prefersReducedMotion()) {
      if (pageScrollRafHandle !== null) {
        cancelAnimationFrame(pageScrollRafHandle);
        pageScrollRafHandle = null;
      }
      content.scrollLeft = target;
      content.style.opacity = "";
      pageTurnTarget = target;
      setPageTurning(false);
      return;
    }

    pageTurnTarget = target;

    // A fade is already running: retarget it instead of restarting. If it has
    // already swapped and is fading the new page in, drop back to the fade-out
    // phase so the latest target gets shown. This coalesces rapid page presses
    // into one cross-fade to the final destination, instead of restarting the
    // fade (and visibly flickering the current page) on every keystroke.
    if (pageScrollRafHandle !== null) {
      // Retargeting to the page we are already on (a fast next→prev) has
      // nothing to swap: abort instead of blinking through a turn that doesn't
      // move. Only safe before the swap, while scrollLeft is still the origin.
      if (!pageTurnSwapped && Math.abs(target - content.scrollLeft) < 1) {
        cancelAnimationFrame(pageScrollRafHandle);
        pageScrollRafHandle = null;
        content.style.opacity = "";
        setPageTurning(false);
        return;
      }
      if (pageTurnSwapped) pageTurnSwapped = false;
      return;
    }

    if (Math.abs(target - content.scrollLeft) < 1) {
      content.scrollLeft = target;
      content.style.opacity = "";
      setPageTurning(false);
      return;
    }

    setPageTurning(true);
    pageTurnSwapped = false;

    // Cross-fade page turn: fade the current page out, swap scrollLeft once
    // while it is invisible (hiding the jump), then fade the new page in. Only
    // composited opacity animates, and the single scroll write avoids the
    // sub-pixel drift a per-frame scroll/transform tween produces. Opacity
    // tracks a real elapsed delta so a retargeted turn resumes from the current
    // opacity rather than snapping back to 1. A literal two-page blend is not
    // possible here: every page is a column in one multicol scroller, not its
    // own layer.
    let lastTime = performance.now();
    // Track opacity in JS rather than parsing it back out of the style
    // attribute every frame: this animation is its only writer, so the
    // parseFloat/String round-trip was pure per-frame overhead.
    let opacity = parseFloat(content.style.opacity || "1");
    if (!Number.isFinite(opacity)) opacity = 1;

    const animate = (now: number): void => {
      if (deps.isDestroyed()) {
        pageScrollRafHandle = null;
        return;
      }

      // Quick fade-out, gentler fade-in (asymmetric; see the constants) so the
      // turn reads as a smooth dip rather than a slow symmetric blink.
      const phaseMs = pageTurnSwapped
        ? PAGE_TURN_FADE_IN_MS
        : PAGE_TURN_FADE_OUT_MS;
      // Clamp so a single delayed frame can't skip the whole fade (see
      // MAX_FADE_STEP) — that collapse is what read as an instant cut.
      const rawStep = phaseMs > 0 ? (now - lastTime) / phaseMs : 1;
      const step = Math.min(rawStep, MAX_FADE_STEP);
      lastTime = now;

      if (!pageTurnSwapped) {
        opacity -= step;
        if (opacity <= 0) {
          opacity = 0;
          content.scrollLeft = pageTurnTarget;
          pageTurnSwapped = true;
        }
      } else {
        opacity += step;
      }

      if (pageTurnSwapped && opacity >= 1) {
        content.scrollLeft = pageTurnTarget;
        content.style.opacity = "";
        pageScrollRafHandle = null;
        const timer = setTimeout(() => {
          if (pageTurnFinishTimer === timer) pageTurnFinishTimer = null;
          if (!deps.isDestroyed()) setPageTurning(false);
        }, 34);
        pageTurnFinishTimer = timer;
        return;
      }

      opacity = opacity < 0 ? 0 : opacity > 1 ? 1 : opacity;
      content.style.opacity = String(opacity);
      pageScrollRafHandle = requestAnimationFrame(animate);
    };

    pageScrollRafHandle = requestAnimationFrame(animate);
  }

  function goToPageInternal(page: number, animated: boolean): void {
    if (!Number.isFinite(page)) return;
    // An explicit page change invalidates the restore anchor: from here a
    // relayout should preserve the ratio, not jump back to the anchor.
    lastAnchorEl = null;
    currentPage = clampPage(page, totalPages);
    applyPageScroll(currentPage, animated);
    reportPagePosition();
    updatePageIndicator();
  }

  function nextPage(): void {
    if (totalPages === 0) return;
    if (currentPage >= totalPages - 1) {
      if (deps.hasNextChapter()) {
        deps.sendMessage({
          type: "at-boundary",
          seq: deps.getActiveSeq(),
          boundary: "end",
        });
      } else {
        deps.flashBoundaryEdge("end");
      }
      return;
    }
    goToPageInternal(currentPage + 1, true);
  }

  function prevPage(): void {
    if (totalPages === 0) return;
    if (currentPage <= 0) {
      if (deps.hasPrevChapter()) {
        deps.sendMessage({
          type: "at-boundary",
          seq: deps.getActiveSeq(),
          boundary: "start",
        });
      } else {
        deps.flashBoundaryEdge("start");
      }
      return;
    }
    goToPageInternal(currentPage - 1, true);
  }

  // Vertical margins inset the paged column box itself rather than padding
  // #content-inner (which, in a multicol flow, only insets the first/last page
  // of the chapter). Read the values applySettings publishes on the root, and
  // keep a minimum bottom inset so text never sits under the page indicator.
  function getPagedVerticalInsets(): { top: number; bottom: number } {
    const cs = getComputedStyle(document.documentElement);
    const parse = (name: string, fallback: number): number => {
      const v = parseFloat(cs.getPropertyValue(name));
      return Number.isFinite(v) ? v : fallback;
    };
    return {
      top: Math.max(0, parse("--paged-padding-top", 24)),
      bottom: Math.max(
        parse("--paged-padding-bottom", 24),
        PAGE_INDICATOR_CLEARANCE,
      ),
    };
  }

  function setPagedHeights(): void {
    const { top, bottom } = getPagedVerticalInsets();
    // Shorten the column box by the vertical margins and offset it down by the
    // top margin, so every page gets a real top/bottom frame margin and the
    // indicator lives in the reserved bottom strip.
    const viewportHeight = Number.isFinite(window.innerHeight)
      ? window.innerHeight
      : 0;
    const height = Math.max(0, viewportHeight - top - bottom);
    const clip = deps.getClipEl();
    const content = deps.getContentEl();
    // Write only on change: a style write dirties layout even when the value is
    // identical, and the content-load path relayouts without the viewport guard
    // (see queuePagedRelayout), so a chapter with several late images would
    // otherwise force a full reflow per no-op relayout.
    const heightPx = height + "px";
    const topPx = top + "px";
    if (clip) {
      if (clip.style.height !== heightPx) clip.style.height = heightPx;
      if (clip.style.marginTop !== topPx) clip.style.marginTop = topPx;
    }
    if (content && content.style.height !== heightPx)
      content.style.height = heightPx;
  }

  function getElementPageIndex(el: Element): number {
    const content = deps.getContentEl();
    if (!content) return 0;
    // Refresh geometry first. searchHighlight resolves a match to a page at
    // arbitrary times — inside the 120ms resize debounce, or before the
    // double-rAF settings relayout lands — and stale stride/totalPages send the
    // jump to the wrong page, silently clamped by clampPage. Two layout reads,
    // never on a hot path.
    totalPages = calculateTotalPages();
    if (pageStride <= 0) return 0;

    const contentRect = content.getBoundingClientRect();
    const rect = el.getBoundingClientRect();
    return pageAtOffset(
      elementLogicalX({
        containerLeft: contentRect.left,
        containerRight: contentRect.right,
        elementLeft: rect.left,
        elementRight: rect.right,
        domScrollLeft: content.scrollLeft,
        rtl: isRTL,
      }),
      pageStride,
      totalPages,
    );
  }

  function scrollToFragmentPaged(id: string, animated = true): boolean {
    const el = document.getElementById(id);
    if (!el) return false;
    goToElementPaged(el, animated);
    return true;
  }

  function goToElementPaged(el: Element, animated = true): void {
    if (totalPages <= 0) {
      el.scrollIntoView({ behavior: "auto" });
      return;
    }
    goToPageInternal(getElementPageIndex(el), animated);
  }

  function relayoutPagedContentPreservingPosition(): void {
    if (!deps.isPagedMode() || deps.isDestroyed()) return;
    // A scroll→paged switch via apply-settings (no chapter reload) routes here
    // instead of through revealPagedShell, so the paged resize observer may not
    // be wired yet. Ensure it exists so later viewport/window resizes
    // re-paginate instead of leaving currentPage/totalPages stale. Guarded on
    // null so repeated relayouts don't churn the observer.
    if (!pagedResizeObserver) setupPagedResizeObserver();
    setPagedHeights();
    // Record the viewport box this layout is computed against so the resize
    // handler can skip no-op relayouts (see handlePagedResize).
    lastLayoutW = window.innerWidth;
    lastLayoutH = window.innerHeight;
    // Prefer the element the page was resolved from: the fonts.ready correction
    // fires right after a CFI-exact restore, and remapping by ratio across a
    // changed page count lands a page or two off the anchor the restore just
    // resolved. Ratio is the fallback once the reader has turned a page.
    const anchor = lastAnchorEl?.isConnected ? lastAnchorEl : null;
    const ratio = pagePercent(currentPage, totalPages);
    totalPages = calculateTotalPages();
    currentPage = anchor
      ? getElementPageIndex(anchor)
      : pageForRatio(ratio, totalPages);
    lastAnchorEl = anchor;
    applyPageScroll(currentPage, false);
    reportPagePosition();
    updatePageIndicator();
  }

  function scheduleFinalFontRelayout(seqAtStart: number): void {
    const token = ++fontRelayoutToken;
    void document.fonts.ready.then(() => {
      if (
        token !== fontRelayoutToken ||
        deps.isDestroyed() ||
        deps.getActiveSeq() !== seqAtStart ||
        !deps.isPagedMode() ||
        !deps.isContentReady()
      )
        return;

      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (
            token !== fontRelayoutToken ||
            deps.isDestroyed() ||
            deps.getActiveSeq() !== seqAtStart ||
            !deps.isPagedMode() ||
            !deps.isContentReady()
          ) {
            return;
          }
          relayoutPagedContentPreservingPosition();
        }),
      );
    });
  }

  // Settings relayouts run after the CSS writes have settled, but a newly
  // selected EPUB/user font may still be loading. Recalculate immediately for
  // responsive feedback, then once more after the latest font set settles.
  // The final pass calls the internal relayout directly so it cannot recurse.
  function relayoutAfterSettings(): void {
    relayoutPagedContentPreservingPosition();
    scheduleFinalFontRelayout(deps.getActiveSeq());
  }

  function revealPagedShell(seqAtStart: number): void {
    if (deps.isDestroyed() || deps.getActiveSeq() !== seqAtStart) return;
    document.body.style.opacity = "1";
    document.documentElement.style.overflow = "";
    deps.ensureBoundaryElements();
    deps.updateBoundaryState();
    updatePageIndicator();
    setupPagedResizeObserver();
    requestAnimationFrame(() => {
      if (!deps.isDestroyed() && deps.getActiveSeq() === seqAtStart) {
        deps.setChapterHidden(false);
      }
    });
    scheduleFinalFontRelayout(seqAtStart);
  }

  function restorePagedPosition(
    scrollTarget: "top" | "end",
    restorePercent: number | null,
    restoreElement: Element | null,
  ): void {
    const seqAtStart = deps.getActiveSeq();
    const content = deps.getContentEl();
    if (content) content.scrollLeft = 0;

    setPageTurning(false);
    if (!pagedResizeObserver) setupPagedResizeObserver();
    setPagedHeights();
    lastLayoutW = window.innerWidth;
    lastLayoutH = window.innerHeight;
    totalPages = calculateTotalPages();

    // Resolve the fragment before committing to it: the pending id is consumed
    // here and there is no second chance, so an anchor that isn't in this
    // chapter must fall through to the percent/CFI restore instead of stranding
    // the reader on page 0.
    const fragmentId = deps.takePendingFragment();
    const fragmentEl = fragmentId ? document.getElementById(fragmentId) : null;
    if (fragmentEl) {
      requestAnimationFrame(() => {
        if (deps.isDestroyed() || deps.getActiveSeq() !== seqAtStart) return;
        // Not animated: the shell is revealed in the same frame, so a cross-fade
        // here plays after the chapter is already visible — the page fades out
        // and back in on top of its own reveal.
        goToElementPaged(fragmentEl, false);
        lastAnchorEl = fragmentEl;
        revealPagedShell(seqAtStart);
      });
      return;
    }

    currentPage = restoreElement
      ? getElementPageIndex(restoreElement)
      : restorePercent !== null &&
          Number.isFinite(restorePercent) &&
          totalPages > 1
        ? pageForRatio(restorePercent, totalPages)
        : scrollTarget === "end"
          ? Math.max(0, totalPages - 1)
          : 0;
    lastAnchorEl = restoreElement;

    applyPageScroll(currentPage, false);
    reportPagePosition();

    requestAnimationFrame(() => revealPagedShell(seqAtStart));
  }

  function queuePagedRelayout(checkViewport: boolean): void {
    if (pagedResizeDebounce) clearTimeout(pagedResizeDebounce);

    pagedResizeDebounce = setTimeout(() => {
      pagedResizeDebounce = null;
      if (deps.isDestroyed() || !deps.isPagedMode() || !deps.isContentReady())
        return;
      // ResizeObserver(documentElement) and the window resize listener both feed
      // this path and can fire for churn that doesn't change the viewport box
      // (scrollbar toggling, sub-pixel rounding, visual-viewport-only changes).
      // A relayout forces a full reflow + scroll reset, so skip viewport-driven
      // events when neither dimension changed since the last pagination.
      // innerWidth/innerHeight reads don't force layout, unlike the clientWidth
      // reads inside relayout. Content-load relayouts bypass this guard because
      // a late image can change scrollWidth/page count with the same viewport.
      if (
        checkViewport &&
        window.innerWidth === lastLayoutW &&
        window.innerHeight === lastLayoutH
      ) {
        return;
      }
      relayoutPagedContentPreservingPosition();
    }, 120);
  }

  function handlePagedResize(): void {
    if (!deps.isPagedMode() || !deps.isContentReady()) return;
    queuePagedRelayout(true);
  }

  function handlePagedContentLoad(event: Event): void {
    if (!deps.isPagedMode() || !deps.isContentReady()) return;
    const target = event.target;
    if (
      target instanceof HTMLImageElement ||
      target instanceof HTMLVideoElement ||
      target instanceof HTMLAudioElement ||
      target instanceof HTMLIFrameElement
    )
      queuePagedRelayout(false);
  }

  function setupPagedResizeObserver(): void {
    if (pagedResizeObserver) return;
    pagedResizeObserver = new ResizeObserver(handlePagedResize);
    pagedResizeObserver.observe(document.documentElement);
    window.addEventListener("resize", handlePagedResize);
    // Late-loading replaced media can change multicol scrollWidth without a
    // viewport resize. Capture-phase `load` catches non-bubbling media loads and
    // forces one debounced page-count refresh for the final intrinsic size.
    document.addEventListener("load", handlePagedContentLoad, true);
  }

  function teardownPagedResizeObserver(): void {
    // Invalidate a pending fonts.ready correction when leaving paged mode or
    // replacing/disposing the chapter. A later paged activation schedules its
    // own correction against the current sequence.
    fontRelayoutToken++;
    if (pagedResizeObserver) {
      pagedResizeObserver.disconnect();
      pagedResizeObserver = null;
    }
    window.removeEventListener("resize", handlePagedResize);
    document.removeEventListener("load", handlePagedContentLoad, true);
    if (pagedResizeDebounce) {
      clearTimeout(pagedResizeDebounce);
      pagedResizeDebounce = null;
    }
  }

  function resetForLoad(rtl: boolean): void {
    currentPage = 0;
    totalPages = 0;
    lastLayoutW = -1;
    lastLayoutH = -1;
    pageTurnTarget = 0;
    pageTurnSwapped = false;
    pageTurningActive = false;
    pageIndicatorText = "";
    if (_pageIndicator) _pageIndicator.textContent = "";
    pageStride = 1;
    maxPageScrollLeft = 0;
    lastAnchorEl = null;
    isRTL = rtl;
    teardownPagedResizeObserver();
    const content = deps.getContentEl();
    if (content) content.style.opacity = "";
    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
  }

  function dispose(): void {
    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
    teardownPagedResizeObserver();
    // cleanupFrame must leave nothing behind (see iframe/AGENTS.md). The
    // indicator is a body child, not part of #content-inner, so a chapter
    // rewrite never removes it.
    _pageIndicator?.remove();
    _pageIndicator = null;
    pageIndicatorText = "";
    lastAnchorEl = null;
  }

  return {
    nextPage,
    prevPage,
    goToPage: goToPageInternal,
    getElementPageIndex,
    scrollToFragmentPaged,
    goToElementPaged,
    relayout: relayoutAfterSettings,
    restorePagedPosition,
    reportPagePosition,
    setPageTurning,
    isRTL: () => isRTL,
    resetForLoad,
    teardownResizeObserver: teardownPagedResizeObserver,
    dispose,
  };
}
