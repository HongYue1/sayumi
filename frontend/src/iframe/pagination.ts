import type { FrameToParentMessage } from "~/lib/frameMessages";

const PAGE_TURN_FADE_MS = 120;
// Minimum bottom inset for the paged column box so the last line never sits
// under the fixed #page-indicator pill (bottom: 12px + pill height).
const PAGE_INDICATOR_CLEARANCE = 32;

export type PaginationDeps = {
  getContentEl: () => HTMLElement;
  getClipEl: () => HTMLElement;
  sendMessage: (msg: FrameToParentMessage) => void;
  getActiveSeq: () => number;
  getActiveChapterIndex: () => number;
  isDestroyed: () => boolean;
  isContentReady: () => boolean;
  isPagedMode: () => boolean;
  hasNextChapter: () => boolean;
  hasPrevChapter: () => boolean;
  setChapterHidden: (hidden: boolean) => void;
  ensureBoundaryElements: () => void;
  updateBoundaryState: () => void;
  takePendingFragment: () => string | null;
};

export type PaginationController = {
  nextPage: () => void;
  prevPage: () => void;
  goToPage: (page: number, animated: boolean) => void;
  goToLastPage: () => void;
  getElementPageIndex: (el: Element) => number;
  scrollToFragmentPaged: (id: string) => void;
  goToElementPaged: (el: Element) => void;
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

// Paged (column) reading mode: page geometry, the cross-fade page turn, paged
// fragment/position restore, the resize-driven relayout, and the page-number
// indicator. Pulled out of frame.ts behind a createPagination(deps) factory so
// frame.ts keeps only mode orchestration (isPagedMode flag, chapter swap,
// settings, lifecycle). All behaviour is verbatim from the original inline
// implementation; deps inject the small slice of frame state it needs.
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
  let pageTurnTransitionTimer: ReturnType<typeof setTimeout> | null = null;
  let pageTurnTransitionCleanup: (() => void) | null = null;
  // Live target/phase for the cross-fade turn, so rapid presses can retarget
  // the running animation instead of restarting it.
  let pageTurnTarget = 0;
  let pageTurnSwapped = false;
  let pageTurningActive = false;
  let _pageIndicator: HTMLElement | null = null;
  let pageIndicatorText = "";
  let reduceMotionQuery: MediaQueryList | null = null;

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
    if (!enabled) {
      document.documentElement.classList.remove("page-turn-out", "page-turn-in");
    }
    if (!enabled && pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
  }

  function setPageTurnPhase(phase: "out" | "in"): void {
    const root = document.documentElement;
    root.classList.toggle("page-turn-out", phase === "out");
    root.classList.toggle("page-turn-in", phase === "in");
  }

  function clearPageTurnTransition(): void {
    if (pageTurnTransitionCleanup) {
      pageTurnTransitionCleanup();
      pageTurnTransitionCleanup = null;
    }
    if (pageTurnTransitionTimer !== null) {
      clearTimeout(pageTurnTransitionTimer);
      pageTurnTransitionTimer = null;
    }
  }

  function cancelPageTurnAnimation(): void {
    clearPageTurnTransition();
    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }
  }

  function isPageTurnAnimating(): boolean {
    return pageTurnTransitionCleanup !== null || pageScrollRafHandle !== null;
  }

  function onOpacitySettled(content: HTMLElement, done: () => void): void {
    clearPageTurnTransition();
    let settled = false;
    const finish = (): void => {
      if (settled) return;
      settled = true;
      clearPageTurnTransition();
      done();
    };
    const onEnd = (event: TransitionEvent): void => {
      if (event.target === content && event.propertyName === "opacity") finish();
    };
    content.addEventListener("transitionend", onEnd);
    pageTurnTransitionCleanup = () => {
      content.removeEventListener("transitionend", onEnd);
    };
    pageTurnTransitionTimer = setTimeout(finish, PAGE_TURN_FADE_MS + 80);
  }

  function startPageTurnIn(content: HTMLElement): void {
    pageTurnSwapped = true;
    setPageTurnPhase("in");
    onOpacitySettled(content, () => {
      content.scrollLeft = pageTurnTarget;
      content.style.opacity = "";
      pageTurnSwapped = false;
      const timer = setTimeout(() => {
        if (pageTurnFinishTimer === timer) pageTurnFinishTimer = null;
        if (!deps.isDestroyed()) setPageTurning(false);
      }, 34);
      pageTurnFinishTimer = timer;
    });
    pageScrollRafHandle = requestAnimationFrame(() => {
      pageScrollRafHandle = null;
      content.style.opacity = "1";
    });
  }

  function startPageTurnOut(content: HTMLElement): void {
    cancelPageTurnAnimation();
    setPageTurning(true);
    pageTurnSwapped = false;
    setPageTurnPhase("out");
    onOpacitySettled(content, () => {
      content.scrollLeft = pageTurnTarget;
      startPageTurnIn(content);
    });
    pageScrollRafHandle = requestAnimationFrame(() => {
      pageScrollRafHandle = null;
      content.style.opacity = "0";
    });
  }

  function prefersReducedMotion(): boolean {
    if (typeof window.matchMedia !== "function") return false;
    reduceMotionQuery ??= window.matchMedia("(prefers-reduced-motion: reduce)");
    return reduceMotionQuery.matches;
  }

  function getPageStride(): number {
    const content = deps.getContentEl();
    const width = content?.clientWidth || window.innerWidth || 1;
    return Number.isFinite(width) ? Math.max(1, width) : 1;
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

  function calculateTotalPages(): number {
    const content = deps.getContentEl();
    if (!content) return 1;

    refreshPageMetrics();
    if (pageStride <= 0) return 1;

    // Pages are stride-aligned scroll positions, so the last page begins at
    // maxScrollLeft. Round (not ceil) so sub-pixel column rounding can't invent
    // a phantom trailing page; +1 converts the last page index to a count.
    // Reads two layout metrics instead of walking the DOM for the last
    // meaningful rect on every relayout.
    return Math.max(1, Math.round(maxPageScrollLeft / pageStride) + 1);
  }

  function reportPagePosition(): void {
    const percent =
      totalPages > 1 && Number.isFinite(currentPage)
        ? currentPage / (totalPages - 1)
        : 0;
    deps.sendMessage({
      type: "position",
      seq: deps.getActiveSeq(),
      chapterIndex: deps.getActiveChapterIndex(),
      percent,
    });
  }

  function applyPageScroll(page: number, animated: boolean): void {
    const content = deps.getContentEl();
    if (!content) return;

    const target = Math.max(0, Math.min(page * pageStride, maxPageScrollLeft));

    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }

    if (!animated || prefersReducedMotion()) {
      cancelPageTurnAnimation();
      content.scrollLeft = target;
      content.style.opacity = "";
      pageTurnTarget = target;
      pageTurnSwapped = false;
      setPageTurning(false);
      return;
    }

    pageTurnTarget = target;

    // A fade is already running: retarget it instead of restarting. If it has
    // already swapped and is fading the new page in, drop back to the fade-out
    // phase so the latest target gets shown. This coalesces rapid page presses
    // into one cross-fade to the final destination, instead of restarting the
    // fade (and visibly flickering the current page) on every keystroke.
    if (isPageTurnAnimating()) {
      if (pageTurnSwapped) startPageTurnOut(content);
      return;
    }

    if (Math.abs(target - content.scrollLeft) < 1) {
      content.scrollLeft = target;
      content.style.opacity = "";
      pageTurnSwapped = false;
      setPageTurning(false);
      return;
    }

    // Cross-fade page turn: fade the current page out with a CSS transition,
    // swap scrollLeft once while it is invisible (hiding the jump), then fade
    // the new page in. Only composited opacity animates, and the single scroll
    // write avoids the sub-pixel drift a per-frame scroll/transform tween
    // produces. A literal two-page blend is not possible here: every page is a
    // column in one multicol scroller, not its own layer.
    startPageTurnOut(content);
  }

  function goToPageInternal(page: number, animated: boolean): void {
    if (!Number.isFinite(page)) return;
    currentPage = Math.max(0, Math.min(totalPages - 1, page));
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
    if (clip) {
      clip.style.height = height + "px";
      clip.style.marginTop = top + "px";
    }
    if (content) content.style.height = height + "px";
  }

  function getElementPageIndex(el: Element): number {
    const content = deps.getContentEl();
    if (!content) return 0;
    const stride = pageStride;
    if (stride <= 0) return 0;

    const contentRect = content.getBoundingClientRect();
    const rect = el.getBoundingClientRect();
    const x = rect.left - contentRect.left + content.scrollLeft;
    if (!Number.isFinite(x)) return 0;
    return Math.max(0, Math.min(totalPages - 1, Math.floor(x / stride)));
  }

  function scrollToFragmentPaged(id: string): void {
    const el = document.getElementById(id);
    if (!el) return;
    goToElementPaged(el);
  }

  function goToElementPaged(el: Element): void {
    if (totalPages <= 0) {
      el.scrollIntoView({ behavior: "auto" });
      return;
    }
    goToPageInternal(getElementPageIndex(el), true);
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
    const ratio =
      totalPages > 1 && Number.isFinite(currentPage)
        ? currentPage / (totalPages - 1)
        : 0;
    totalPages = calculateTotalPages();
    currentPage = Math.max(
      0,
      Math.min(Math.round(ratio * (totalPages - 1)), totalPages - 1),
    );
    applyPageScroll(currentPage, false);
    reportPagePosition();
    updatePageIndicator();
  }

  function scheduleFinalFontRelayout(seqAtStart: number): void {
    document.fonts.ready.then(() => {
      if (
        deps.isDestroyed() ||
        deps.getActiveSeq() !== seqAtStart ||
        !deps.isPagedMode() ||
        !deps.isContentReady()
      )
        return;

      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (
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

    const fragment = deps.takePendingFragment();
    if (fragment) {
      requestAnimationFrame(() => {
        if (deps.isDestroyed() || deps.getActiveSeq() !== seqAtStart) return;
        scrollToFragmentPaged(fragment);
        revealPagedShell(seqAtStart);
      });
      return;
    }

    currentPage = restoreElement
      ? getElementPageIndex(restoreElement)
      : restorePercent !== null &&
          Number.isFinite(restorePercent) &&
          totalPages > 1
        ? Math.max(
            0,
            Math.min(
              totalPages - 1,
              Math.round(restorePercent * (totalPages - 1)),
            ),
          )
        : scrollTarget === "end"
          ? Math.max(0, totalPages - 1)
          : 0;

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
    pageStride = 1;
    maxPageScrollLeft = 0;
    isRTL = rtl;
    teardownPagedResizeObserver();
    const content = deps.getContentEl();
    if (content) content.style.opacity = "";
    cancelPageTurnAnimation();
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
    document.documentElement.classList.remove("page-turn-out", "page-turn-in");
  }

  function dispose(): void {
    cancelPageTurnAnimation();
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
    teardownPagedResizeObserver();
  }

  return {
    nextPage,
    prevPage,
    goToPage: goToPageInternal,
    goToLastPage: () => goToPageInternal(totalPages - 1, false),
    getElementPageIndex,
    scrollToFragmentPaged,
    goToElementPaged,
    relayout: relayoutPagedContentPreservingPosition,
    restorePagedPosition,
    reportPagePosition,
    setPageTurning,
    isRTL: () => isRTL,
    resetForLoad,
    teardownResizeObserver: teardownPagedResizeObserver,
    dispose,
  };
}
