import type {
  IframeSettings,
  LoadMessage,
  ParentToFrameMessage,
  FrameToParentMessage,
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

(function () {
  "use strict";

  const BLOCK_TAGS = new Set([
    "P",
    "H1",
    "H2",
    "H3",
    "H4",
    "H5",
    "H6",
    "LI",
    "BLOCKQUOTE",
    "DIV",
    "SECTION",
    "ARTICLE",
  ]);

  const WHEEL_THRESHOLD = 600;
  const CHAPTER_SWAP_OUT_MS = 110;
  const REVEAL_FALLBACK_SCROLL_MS = 400;
  const REVEAL_FALLBACK_PAGED_MS = 550;

  function prefersReducedMotion(): boolean {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  let activeSeq = -1;
  let activeChapterIndex = -1;
  let contentReady = false;
  let loadScrollTarget: "top" | "end" | null = null;
  let pendingFragment: string | null = null;
  let parentOrigin = "";
  let destroyed = false;

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
  });

  const pagination = createPagination({
    getContentEl,
    getClipEl,
    sendMessage,
    getActiveSeq: () => activeSeq,
    getActiveChapterIndex: () => activeChapterIndex,
    isDestroyed: () => destroyed,
    isContentReady: () => contentReady,
    isPagedMode: () => isPagedMode,
    hasNextChapter: () => hasNextChapter,
    hasPrevChapter: () => hasPrevChapter,
    setChapterHidden,
    ensureBoundaryElements: () => boundary.ensureElements(),
    updateBoundaryState,
    takePendingFragment: () => {
      const f = pendingFragment;
      pendingFragment = null;
      return f;
    },
  });

  let isPagedMode = false;
  let reportPositionRafHandle: number | null = null;
  let scrollRafHandle: number | null = null;
  let pagedRelayoutRafHandle: number | null = null;
  let revealFallbackTimer: ReturnType<typeof setTimeout> | null = null;
  let loadCommitTimer: ReturnType<typeof setTimeout> | null = null;
  let chapterAnimTimer: ReturnType<typeof setTimeout> | null = null;
  // Slightly longer than the longest #paged-clip transition (transform 0.3s) so
  // the compositor hint stays for the whole swap, then is removed.
  const CHAPTER_ANIM_SETTLE_MS = 360;
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
    const vh = window.innerHeight;
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

    const cx = window.innerWidth / 2;
    const vh = window.innerHeight;
    const samples = [8, 40, Math.floor(vh * 0.18), Math.floor(vh * 0.33)];

    for (const y of samples) {
      if (y < 0 || y >= vh) continue;
      const leaf = document.elementFromPoint(cx, y);
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

  function getContentEl(): HTMLElement {
    return (_contentEl ??= document.getElementById("content") as HTMLElement);
  }

  function getClipEl(): HTMLElement {
    return (_clipEl ??= document.getElementById("paged-clip") as HTMLElement);
  }

  function sendMessage(msg: FrameToParentMessage): void {
    if (destroyed) return;
    window.parent.postMessage(msg, parentOrigin || "*");
  }

  function updateBoundaryState(): void {
    const scrollTop = window.scrollY;
    const scrollMax =
      document.documentElement.scrollHeight - window.innerHeight;
    if (scrollMax <= 0) {
      atTop = true;
      atBottom = true;
    } else {
      atTop = scrollTop <= 1;
      atBottom = scrollTop >= scrollMax - 1;
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
    const cacheKey = `${parentOrigin ?? ""}\u0000${rawBookCSS}\u0000${bookFontFaceCSS}`;
    const hit = _preparedCSSCache.get(cacheKey);
    if (hit) {
      _preparedCSSCache.delete(cacheKey);
      _preparedCSSCache.set(cacheKey, hit); // refresh LRU position
      preparedFontCSS = hit.fontCSS;
      preparedLayoutCSS = hit.layoutCSS;
      preparedRawCSS = hit.rawCSS;
      preparedFontFaceCSS = hit.fontFaceCSS;
      preparedBookFontFamilies = hit.bookFontFamilies;
      return;
    }
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
    // Leaving will-change on permanently (in CSS) wastes a compositor layer and
    // a blur buffer; toggling a transient class around the swap is the
    // recommended will-change pattern. Covers both swap-out (hidden=true) and
    // reveal (hidden=false) since both route through here.
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
    const max = document.documentElement.scrollHeight - window.innerHeight;
    window.scrollTo({
      top: max * percent,
      behavior: "instant" as ScrollBehavior,
    });
  }

  function restoreScrollPosition(): void {
    const fragment = pendingFragment;
    const restoreCfi = loadRestoreCfi;
    const restorePercent = loadRestorePercent;
    const scrollTarget = loadScrollTarget;

    pendingFragment = null;
    loadRestoreCfi = null;
    loadRestorePercent = null;
    loadScrollTarget = null;

    if (fragment) {
      revealScrollShell();
      requestAnimationFrame(() => {
        if (destroyed) return;
        scrollToFragmentById(fragment);
        if (reportPositionRafHandle !== null)
          cancelAnimationFrame(reportPositionRafHandle);
        reportPositionRafHandle = requestAnimationFrame(reportPosition);
      });
      return;
    }

    window.scrollTo({
      top: scrollTarget === "end" ? document.documentElement.scrollHeight : 0,
      behavior: "instant" as ScrollBehavior,
    });

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

  function drainPendingSearchHighlight(): void {
    const h = pendingSearchHighlight;
    if (!h || !contentReady) return;
    if (typeof h.seq === "number" && h.seq !== activeSeq) {
      pendingSearchHighlight = null;
      return;
    }
    pendingSearchHighlight = null;
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

    document.fonts.ready.then(run);
  }

  function runInitialLayoutRestore(): void {
    const seqAtStart = activeSeq;

    if (isPagedMode) {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          if (destroyed || activeSeq !== seqAtStart) return;

          revealAfterFonts(() => {
            if (destroyed || activeSeq !== seqAtStart) return;
            const target = loadScrollTarget || "top";
            const pagedRestore = loadRestorePercent;
            const pagedRestoreElement = loadRestoreCfi
              ? resolveCFI(loadRestoreCfi, document)
              : null;
            loadScrollTarget = null;
            loadRestorePercent = null;
            loadRestoreCfi = null;
            pagination.restorePagedPosition(
              target,
              pagedRestore,
              pagedRestoreElement,
            );
            requestAnimationFrame(drainPendingSearchHighlight);
          }, REVEAL_FALLBACK_PAGED_MS);
        });
      });
      return;
    }

    requestAnimationFrame(() =>
      requestAnimationFrame(() =>
        revealAfterFonts(() => {
          if (destroyed || activeSeq !== seqAtStart) return;
          restoreScrollPosition();
          drainPendingSearchHighlight();
        }),
      ),
    );
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

  function applySettings(settings: IframeSettings): void {
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

    if (settings.mode === "paged") {
      const pt =
        settings.margins.top != null ? `${settings.margins.top}px` : "24px";
      const pb =
        settings.margins.bottom != null
          ? `${settings.margins.bottom}px`
          : "24px";
      const ps =
        settings.margins.side != null ? `${settings.margins.side}px` : "40px";

      css.push(
        `html { --paged-padding-top: ${pt}; --paged-padding-bottom: ${pb}; --paged-padding-side: ${ps}; }`,
      );
      css.push(
        "body { padding: 0 !important; height: 100vh !important; overflow: hidden !important; }",
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
    } else if (settings.mode === "paged-two") {
      // Double-page spread now honors the user's vertical margin, clamped to a
      // stable minimum so the spread geometry stays predictable. Side inset stays
      // fixed. The vertical inset is applied to the column box itself (see
      // pagination.setPagedHeights, which reads --paged-padding-top/bottom), not
      // as #content-inner padding.
      const MIN_TWO_MARGIN = 24;
      const pt = `${Math.max(settings.margins.top ?? 24, MIN_TWO_MARGIN)}px`;
      const pb = `${Math.max(settings.margins.bottom ?? 24, MIN_TWO_MARGIN)}px`;
      const ps = "48px";

      css.push(
        `html { --paged-padding-top: ${pt}; --paged-padding-bottom: ${pb}; --paged-padding-side: ${ps}; }`,
      );
      css.push(
        "body { padding: 0 !important; height: 100vh !important; overflow: hidden !important; }",
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
    }

    if (settings.chapterTitleAlign != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { text-align: ${settings.chapterTitleAlign} !important; }`,
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
    // heading. Heading rules are pushed last so they win over the body weight
    // headings would otherwise inherit. When only text weight is set, headings
    // revert to their natural (book/UA) weight instead of inheriting it.
    if (settings.textWeight != null) {
      css.push(`body { font-weight: ${settings.textWeight} !important; }`);
    }
    if (settings.headerWeight != null) {
      css.push(
        `h1, h2, h3, h4, h5, h6 { font-weight: ${settings.headerWeight} !important; }`,
      );
    } else if (settings.textWeight != null) {
      css.push(`h1, h2, h3, h4, h5, h6 { font-weight: revert !important; }`);
    }

    getStyleEl("override-css").textContent = css.join("\n");

    isPagedMode = settings.mode === "paged" || settings.mode === "paged-two";
    applyRootClasses(settings.theme, settings.mode);

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
    }

    if (!contentReady) {
      contentReady = true;
      runInitialLayoutRestore();
    } else if (isPagedMode) {
      schedulePagedRelayout();
    }
  }

  function scrollToFragmentById(id: string): void {
    if (isPagedMode) {
      pagination.scrollToFragmentPaged(id);
      boundary.reset();
      return;
    }

    const el = document.getElementById(id);
    if (!el) return;

    if (!contentReady) {
      contentReady = true;
      document.body.style.opacity = "1";
      document.documentElement.style.overflow = "";
      requestAnimationFrame(() => {
        if (!destroyed) setChapterHidden(false);
      });
    }

    el.scrollIntoView({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    boundary.reset();
  }

  // Jump to a previously reported CFI within the *current* chapter (e.g. a
  // bookmark in the chapter already on screen). Mirrors scrollToFragmentById
  // but resolves the position via the CFI path instead of an element id.
  function scrollToCfiLocal(cfi: string): void {
    const el = resolveCFI(cfi, document);
    if (!el) return;

    if (isPagedMode) {
      pagination.goToElementPaged(el);
      boundary.reset();
      return;
    }

    if (!contentReady) {
      contentReady = true;
      document.body.style.opacity = "1";
      document.documentElement.style.overflow = "";
      requestAnimationFrame(() => {
        if (!destroyed) setChapterHidden(false);
      });
    }

    el.scrollIntoView({
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    boundary.reset();
  }

  function getScrollPercent(): number {
    const max = document.documentElement.scrollHeight - window.innerHeight;
    if (max <= 0) return 0;
    return Math.min(1, Math.max(0, window.scrollY / max));
  }

  function killScrollMomentum(): void {
    window.scrollTo({
      top: window.scrollY,
      behavior: "instant" as ScrollBehavior,
    });
  }

  let scrollThrottleTimer: ReturnType<typeof setTimeout> | null = null;
  let scrollThrottlePending = false;

  function reportPosition(): void {
    if (isPagedMode) {
      pagination.reportPagePosition();
      return;
    }

    const percent = getScrollPercent();
    const el = findFirstVisibleBlock();
    let cfi: string | null = null;

    if (el) {
      if (el === lastReportedAnchor) {
        cfi = lastReportedCfi;
      } else {
        cfi = generateCFI(el, document);
        lastReportedAnchor = el;
        lastReportedCfi = cfi;
      }
    } else {
      lastReportedAnchor = null;
      lastReportedCfi = null;
    }

    const msg: Extract<FrameToParentMessage, { type: "position" }> = {
      type: "position",
      seq: activeSeq,
      chapterIndex: activeChapterIndex,
      percent,
    };
    if (cfi !== null) msg.cfi = cfi;
    sendMessage(msg);
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
    if (destroyed || isPagedMode) return;
    // Scroll events can fire several times per frame during momentum scrolling;
    // coalesce the layout reads + position report into a single rAF tick.
    if (scrollRafHandle !== null) return;
    scrollRafHandle = requestAnimationFrame(() => {
      scrollRafHandle = null;
      if (destroyed || isPagedMode) return;
      updateBoundaryState();
      throttledReportPosition();
      if (!atTop && !atBottom && boundary.hasActiveDirection())
        boundary.reset();
    });
  }

  function handleWheel(e: WheelEvent): void {
    if (destroyed || !contentReady) return;
    if (isPagedMode) return;
    updateBoundaryState();
    if (atTop && e.deltaY < 0) {
      boundary.accumulate(Math.abs(e.deltaY), "start", WHEEL_THRESHOLD);
      return;
    }
    if (atBottom && e.deltaY > 0) {
      boundary.accumulate(Math.abs(e.deltaY), "end", WHEEL_THRESHOLD);
      return;
    }
    if (boundary.hasActiveDirection()) boundary.reset();
  }

  function handleTouchStart(e: TouchEvent): void {
    if (destroyed || !contentReady || e.touches.length !== 1) return;
    touchStartY = e.touches[0].clientY;
    touchStartX = e.touches[0].clientX;
    touchLastX = touchStartX;
    touchLastY = touchStartY;
    touchTracking = true;
    touchBoundaryBase = 0;
    updateBoundaryState();
    touchAtBoundaryOnStart = atTop || atBottom;
  }

  function handleTouchMove(e: TouchEvent): void {
    if (destroyed || !touchTracking || e.touches.length !== 1) return;
    touchLastX = e.touches[0].clientX;
    touchLastY = e.touches[0].clientY;
    if (isPagedMode) return;

    const touchY = e.touches[0].clientY;
    const touchX = e.touches[0].clientX;
    const totalDeltaY = touchStartY - touchY;
    const deltaX = Math.abs(touchX - touchStartX);
    if (deltaX > Math.abs(totalDeltaY) * 0.7) return;

    updateBoundaryState();

    if (atBottom && totalDeltaY > 0) {
      if (touchBoundaryBase === 0) {
        touchBoundaryBase = touchAtBoundaryOnStart ? 0 : totalDeltaY;
      }
      const boundaryDelta = totalDeltaY - touchBoundaryBase;
      if (boundaryDelta > 0) {
        boundary.processTouch("end", hasNextChapter, boundaryDelta);
      }
      return;
    }

    if (atTop && totalDeltaY < 0) {
      if (touchBoundaryBase === 0) {
        touchBoundaryBase = touchAtBoundaryOnStart ? 0 : totalDeltaY;
      }
      const boundaryDelta = Math.abs(totalDeltaY - touchBoundaryBase);
      if (boundaryDelta > 0) {
        boundary.processTouch("start", hasPrevChapter, boundaryDelta);
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
    if (!boundary.isSent()) boundary.reset();
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (destroyed) return;

    const isPaletteShortcut =
      (e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K");
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
    const scrollMax =
      document.documentElement.scrollHeight - window.innerHeight;

    if (atTop && (e.key === "ArrowUp" || e.key === "PageUp")) {
      if (hasPrevChapter) {
        sendMessage({ type: "at-boundary", seq: activeSeq, boundary: "start" });
      }
      handled = true;
    } else if (
      atBottom &&
      (e.key === "ArrowDown" || e.key === "PageDown" || e.key === " ")
    ) {
      if (e.key !== " " || !e.shiftKey) {
        if (scrollMax <= 0 || window.scrollY >= scrollMax - 1) {
          if (hasNextChapter) {
            sendMessage({
              type: "at-boundary",
              seq: activeSeq,
              boundary: "end",
            });
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

    const vw = window.innerWidth;
    const x = e.clientX;
    const region: "left" | "center" | "right" =
      x < vw / 3 ? "left" : x > (vw * 2) / 3 ? "right" : "center";
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
    if (/^(javascript|data|vbscript):/i.test(href)) return;

    if (/^https?:\/\//i.test(href)) {
      window.open(href, "_blank", "noopener,noreferrer");
      return;
    }

    if (href.startsWith("#")) {
      scrollToFragmentById(href.slice(1));
      return;
    }

    sendMessage({ type: "link-clicked", href });
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
    pendingSearchHighlight = null;
    isPagedMode = false;
    pagination.resetForLoad(msg.direction === "rtl");

    const contentEl = getContentEl();
    contentEl.scrollLeft = 0;

    loadScrollTarget = msg.scrollTo || "top";
    pendingFragment = msg.fragment || null;
    hasNextChapter = msg.hasNext !== false;
    hasPrevChapter = msg.hasPrev !== false;
    loadRestorePercent =
      typeof msg.restorePercent === "number" ? msg.restorePercent : null;
    loadRestoreCfi =
      typeof msg.restoreCfi === "string" && msg.restoreCfi
        ? msg.restoreCfi
        : null;

    lastVisibleBlock = null;
    lastReportedAnchor = null;
    lastReportedCfi = null;

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
    if (msg.direction) document.documentElement.dir = msg.direction;
    if (msg.writingMode) {
      document.documentElement.style.writingMode = msg.writingMode;
    }
    if (typeof msg.language === "string" && msg.language) {
      // Sanitize to BCP-47-ish chars before reflecting into the DOM lang attr.
      const safeLang = msg.language.replace(/[^a-zA-Z0-9-]/g, "").slice(0, 35);
      if (safeLang) document.documentElement.lang = safeLang;
    }

    sendMessage({ type: "loaded", seq: activeSeq });

    if (pendingSettingsMessage) {
      const pending = pendingSettingsMessage;
      pendingSettingsMessage = null;
      applySettings(pending);
      releaseOverflowAfterSettings();
    }
  }

  function cleanupFrame(): void {
    if (destroyed) return;
    destroyed = true;

    boundary.disposeTimers();
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
    cancelScheduledPagedRelayout();

    window.removeEventListener("message", handleMessage);
    document.removeEventListener("click", handleClick);
    window.removeEventListener("scroll", handleScroll);
    window.removeEventListener("wheel", handleWheel);
    document.removeEventListener("touchstart", handleTouchStart);
    document.removeEventListener("touchmove", handleTouchMove);
    document.removeEventListener("touchend", handleTouchEnd);
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
        if (contentReady) {
          const max =
            document.documentElement.scrollHeight - window.innerHeight;
          const pct = Number.isFinite(msg.percent)
            ? Math.min(1, Math.max(0, msg.percent))
            : 0;
          window.scrollTo({
            top: max * pct,
            behavior: "instant" as ScrollBehavior,
          });
          boundary.reset();
          updateBoundaryState();
        }
        break;

      case "scroll-to-end":
        if (contentReady) {
          window.scrollTo({
            top: document.documentElement.scrollHeight,
            behavior: "instant" as ScrollBehavior,
          });
          boundary.reset();
          updateBoundaryState();
        }
        break;

      case "next-page":
        if (isPagedMode) pagination.nextPage();
        break;

      case "prev-page":
        if (isPagedMode) pagination.prevPage();
        break;

      case "go-to-page":
        if (isPagedMode && typeof msg.page === "number") {
          pagination.goToPage(msg.page - 1, false);
        }
        break;

      case "go-to-last-page":
        if (isPagedMode) pagination.goToLastPage();
        break;

      case "scroll-to-fragment":
        if (typeof msg.id === "string") scrollToFragmentById(msg.id);
        break;

      case "scroll-to-cfi":
        if (typeof msg.cfi === "string") scrollToCfiLocal(msg.cfi);
        break;

      case "get-position":
        reportPosition();
        break;

      case "highlight-search":
        if (
          typeof msg.charOffset !== "number" ||
          typeof msg.matchLen !== "number"
        ) {
          break;
        }
        // Drop highlights computed for a superseded chapter load. A deferred
        // cross-chapter highlight can arrive after a faster re-navigation has
        // already swapped in a different chapter; without this guard it would
        // mark the wrong text. Messages without a seq still apply (same-chapter
        // highlights, where the live seq is authoritative).
        if (typeof msg.seq === "number" && msg.seq !== activeSeq) break;
        if (!contentReady) {
          pendingSearchHighlight = {
            charOffset: msg.charOffset,
            matchLen: msg.matchLen,
            query: String(msg.query || ""),
            seq: typeof msg.seq === "number" ? msg.seq : undefined,
          };
          break;
        }
        searchHl.highlightSearchMatch(
          msg.charOffset,
          msg.matchLen,
          String(msg.query || ""),
        );
        break;

      case "clear-highlights":
        pendingSearchHighlight = null;
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
  document.addEventListener("keydown", handleKeyDown);

  sendMessage({ type: "ready" });
})();
