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

  const FONT_FAMILY_RE =
    /font-family\s*:\s*['\"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['\"]?/gi;
  const FONT_FACE_BLOCK_RE = /@font-face\s*\{[^}]*\}/gi;
  const FONT_FACE_FAMILY_RE =
    /font-family\s*:\s*['\"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['\"]?/i;

  const WHEEL_THRESHOLD = 600;
  const TOUCH_THRESHOLD = 200;
  const BOUNDARY_RESET_MS = 600;
  const CHAPTER_SWAP_OUT_MS = 110;
  const REVEAL_FALLBACK_SCROLL_MS = 400;
  const REVEAL_FALLBACK_PAGED_MS = 550;
  const PAGE_TURN_BASE_MS = 240;
  const PAGE_TURN_PER_PAGE_MS = 70;
  const PAGE_TURN_MAX_MS = 420;

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

  let _contentEl: HTMLElement | null = null;
  let _contentInnerEl: HTMLElement | null = null;
  let _clipEl: HTMLElement | null = null;
  const _styleEls: Record<string, HTMLStyleElement> = {};
  let _boundaryTop: HTMLElement | null = null;
  let _boundaryBottom: HTMLElement | null = null;
  let _boundaryTopLabel: HTMLElement | null = null;
  let _boundaryBottomLabel: HTMLElement | null = null;
  let _pageIndicator: HTMLElement | null = null;
  let _stripCSSCache: { input: string; output: string } | null = null;

  let activeThemeClass =
    Array.from(document.documentElement.classList).find((c) =>
      c.startsWith("theme-"),
    ) ?? "theme-light";

  let atTop = false;
  let atBottom = false;
  let boundaryAccum = 0;
  let boundaryDirection: "start" | "end" | null = null;
  let boundaryResetTimer: ReturnType<typeof setTimeout> | null = null;
  let boundaryFireTimer: ReturnType<typeof setTimeout> | null = null;
  let boundarySent = false;
  let hasNextChapter = true;
  let hasPrevChapter = true;

  let touchStartY = 0;
  let touchStartX = 0;
  let touchLastX = 0;
  let touchLastY = 0;
  let touchTracking = false;
  let touchBoundaryBase = 0;
  let touchAtBoundaryOnStart = false;

  let isPagedMode = false;
  let currentPage = 0;
  let totalPages = 0;
  let isRTL = false;
  let pagedResizeObserver: ResizeObserver | null = null;
  let pagedResizeDebounce: ReturnType<typeof setTimeout> | null = null;
  let pageScrollRafHandle: number | null = null;
  let pageTurnFinishTimer: ReturnType<typeof setTimeout> | null = null;
  let reportPositionRafHandle: number | null = null;
  let scrollRafHandle: number | null = null;
  let revealFallbackTimer: ReturnType<typeof setTimeout> | null = null;
  let loadCommitTimer: ReturnType<typeof setTimeout> | null = null;
  let loadTransitionToken = 0;
  let pendingSettingsMessage: IframeSettings | null = null;

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

  function generateCFIForElement(el: Element): string | null {
    const body = document.body;
    if (!body || !body.contains(el) || el === body) return null;

    const path: number[] = [];
    let current: Element | null = el;

    while (current && current !== body) {
      const parent: Element | null = current.parentElement;
      if (!parent) return null;

      let index = 0;
      for (let i = 0; i < parent.children.length; i++) {
        if (parent.children[i] === current) {
          index = i + 1;
          break;
        }
      }
      if (index === 0) return null;

      path.unshift(index);
      current = parent === body ? null : parent;
    }

    if (path.length === 0) return null;
    return "cfi:" + path.join("/");
  }

  function resolveCFILocal(cfi: string): Element | null {
    if (!cfi.startsWith("cfi:")) return null;
    const body = document.body;
    if (!body) return null;

    const parts = cfi.slice(4).split("/");
    let current: Element = body;

    for (const part of parts) {
      // Strict integer parse: a malformed/foreign CFI segment (e.g. "3x", "",
      // "1.5") should cleanly fail to null so callers fall back to percent,
      // rather than parseInt leniently coercing it to a wrong-but-valid index.
      if (!/^\d+$/.test(part)) return null;
      const index = parseInt(part, 10);
      if (index < 1) return null;
      const child = current.children[index - 1];
      if (!child) return null;
      current = child;
    }

    return current;
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

  function getContentInnerEl(): HTMLElement {
    return (_contentInnerEl ??= document.getElementById(
      "content-inner",
    ) as HTMLElement);
  }

  function getClipEl(): HTMLElement {
    return (_clipEl ??= document.getElementById("paged-clip") as HTMLElement);
  }

  function sendMessage(msg: Record<string, unknown>): void {
    if (destroyed) return;
    window.parent.postMessage(msg, parentOrigin || "*");
  }

  const COLOR_PROPS_SET = new Set([
    "color",
    "background",
    "background-color",
    "background-image",
    "background-blend-mode",
    "border-color",
    "border-top-color",
    "border-right-color",
    "border-bottom-color",
    "border-left-color",
    "outline-color",
    "text-decoration-color",
    "column-rule-color",
    "fill",
    "stroke",
  ]);

  function stripColorsFromCSS(cssText: string): string {
    if (_stripCSSCache?.input === cssText) return _stripCSSCache.output;
    let output: string;
    try {
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(cssText);
      let result = "";
      for (const rule of sheet.cssRules) result += processRuleStripColors(rule);
      output = result;
    } catch {
      output = cssText;
    }
    _stripCSSCache = { input: cssText, output };
    return output;
  }

  function processRuleStripColors(rule: CSSRule): string {
    if (rule instanceof CSSStyleRule) {
      const style = rule.style;
      const kept: string[] = [];
      for (let i = 0; i < style.length; i++) {
        const prop = style.item(i);
        if (!COLOR_PROPS_SET.has(prop)) {
          const val = style.getPropertyValue(prop);
          const pri = style.getPropertyPriority(prop);
          kept.push(`${prop}: ${val}${pri ? " !important" : ""}`);
        }
      }
      return kept.length > 0
        ? `${rule.selectorText} { ${kept.join("; ")}; }\n`
        : "";
    }
    if (rule instanceof CSSMediaRule) {
      let inner = "";
      for (const child of rule.cssRules) inner += processRuleStripColors(child);
      return inner ? `@media ${rule.conditionText} {\n${inner}}\n` : "";
    }
    return rule.cssText + "\n";
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

  function resetBoundary(): void {
    boundaryAccum = 0;
    boundaryDirection = null;
    boundarySent = false;
    if (boundaryResetTimer) {
      clearTimeout(boundaryResetTimer);
      boundaryResetTimer = null;
    }
    updateBoundaryIndicator(null, 0);
  }

  function scheduleBoundaryReset(): void {
    if (boundaryResetTimer) clearTimeout(boundaryResetTimer);
    boundaryResetTimer = setTimeout(resetBoundary, BOUNDARY_RESET_MS);
  }

  function accumulateBoundary(
    delta: number,
    direction: "start" | "end",
    threshold: number,
  ): void {
    if (boundarySent) return;
    if (direction === "start" && !hasPrevChapter) {
      updateBoundaryIndicator("edge-start", Math.min(1, Math.abs(delta) / 40));
      scheduleBoundaryReset();
      return;
    }
    if (direction === "end" && !hasNextChapter) {
      updateBoundaryIndicator("edge-end", Math.min(1, Math.abs(delta) / 40));
      scheduleBoundaryReset();
      return;
    }
    if (boundaryDirection !== direction) {
      boundaryAccum = 0;
      boundaryDirection = direction;
    }
    boundaryAccum += Math.abs(delta);
    scheduleBoundaryReset();
    const progress = Math.min(1, boundaryAccum / threshold);
    updateBoundaryIndicator(direction, progress);
    if (boundaryAccum >= threshold) {
      boundarySent = true;
      updateBoundaryIndicator(direction, 1);
      sendMessage({ type: "at-boundary", seq: activeSeq, boundary: direction });
      if (boundaryFireTimer) clearTimeout(boundaryFireTimer);
      boundaryFireTimer = setTimeout(resetBoundary, 300);
    }
  }

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
    ensurePageIndicator().textContent =
      totalPages > 0 ? `${currentPage + 1} / ${totalPages}` : "";
  }

  function ensureIndicatorElements(): void {
    if (!_boundaryTop) {
      const el = document.createElement("div");
      el.id = "boundary-indicator-top";
      el.className = "boundary-indicator boundary-top";
      const span = document.createElement("span");
      span.className = "boundary-label";
      el.appendChild(span);
      document.body.appendChild(el);
      _boundaryTop = el;
      _boundaryTopLabel = span;
    }
    if (!_boundaryBottom) {
      const el = document.createElement("div");
      el.id = "boundary-indicator-bottom";
      el.className = "boundary-indicator boundary-bottom";
      const span = document.createElement("span");
      span.className = "boundary-label";
      el.appendChild(span);
      document.body.appendChild(el);
      _boundaryBottom = el;
      _boundaryBottomLabel = span;
    }
  }

  function updateBoundaryIndicator(
    dir: "start" | "end" | "edge-start" | "edge-end" | null,
    progress: number,
  ): void {
    const topEl = _boundaryTop;
    const bottomEl = _boundaryBottom;
    if (!topEl || !bottomEl) return;

    // _boundaryTopLabel and _boundaryBottomLabel are always co-assigned with
    // _boundaryTop/_boundaryBottom inside ensureIndicatorElements, so if the
    // elements exist the labels exist too. Explicit checks make this invariant
    // self-evident instead of relying on non-null assertions.
    const topLabel = _boundaryTopLabel;
    const bottomLabel = _boundaryBottomLabel;
    if (!topLabel || !bottomLabel) return;

    topEl.style.opacity = "0";
    topEl.style.transform = "translateY(-40px)";
    topEl.classList.remove("edge");
    bottomEl.style.opacity = "0";
    bottomEl.style.transform = "translateY(40px)";
    bottomEl.classList.remove("edge");

    if (dir === "start" || dir === "edge-start") {
      topLabel.textContent =
        dir === "start" ? "Previous chapter" : "Beginning of book";
      if (dir === "edge-start") topEl.classList.add("edge");
      topEl.style.opacity = String(
        dir === "edge-start"
          ? Math.min(0.8, progress)
          : Math.min(1, progress * 1.5),
      );
      topEl.style.transform = `translateY(${-40 + progress * 40}px)`;
    } else if (dir === "end" || dir === "edge-end") {
      bottomLabel.textContent = dir === "end" ? "Next chapter" : "End of book";
      if (dir === "edge-end") bottomEl.classList.add("edge");
      bottomEl.style.opacity = String(
        dir === "edge-end"
          ? Math.min(0.8, progress)
          : Math.min(1, progress * 1.5),
      );
      bottomEl.style.transform = `translateY(${40 - progress * 40}px)`;
    }
  }

  function splitBookCSS(cssText: string): {
    fontCSS: string;
    layoutCSS: string;
  } {
    try {
      const sheet = new CSSStyleSheet();
      sheet.replaceSync(cssText);
      let fontCSS = "";
      let layoutCSS = "";

      for (const rule of sheet.cssRules) {
        if (rule instanceof CSSStyleRule) {
          const ff = rule.style.getPropertyValue("font-family");
          if (ff) {
            fontCSS += `${rule.selectorText} { font-family: ${ff}; }\n`;
            const clone = rule.style.cssText
              .replace(/font-family\s*:[^;]+;?/gi, "")
              .trim();
            if (clone) layoutCSS += `${rule.selectorText} { ${clone} }\n`;
          } else {
            layoutCSS += rule.cssText + "\n";
          }
        } else {
          layoutCSS += rule.cssText + "\n";
        }
      }

      return { fontCSS, layoutCSS };
    } catch {
      return { fontCSS: "", layoutCSS: cssText };
    }
  }

  function extractBookFontFamilies(fontFaceCSS: string): Set<string> {
    const names = new Set<string>();
    for (const match of fontFaceCSS.matchAll(FONT_FAMILY_RE)) {
      names.add(match[1].trim().toLowerCase());
    }
    return names;
  }

  function filterReaderFontFaces(
    readerFF: string,
    excludeNames: Set<string>,
  ): string {
    if (excludeNames.size === 0) return readerFF;
    return readerFF.replace(FONT_FACE_BLOCK_RE, (block) => {
      const match = block.match(FONT_FACE_FAMILY_RE);
      if (match && excludeNames.has(match[1].trim().toLowerCase())) return "";
      return block;
    });
  }

  function prepareChapterCSS(): void {
    const { fontCSS, layoutCSS } = splitBookCSS(rawBookCSS);
    preparedFontCSS = fontCSS;
    preparedLayoutCSS = stripColorsFromCSS(absolutifyCSS(layoutCSS));
    preparedRawCSS = stripColorsFromCSS(absolutifyCSS(rawBookCSS));
    preparedFontFaceCSS = absolutifyCSS(bookFontFaceCSS);
    preparedBookFontFamilies = extractBookFontFamilies(bookFontFaceCSS);
  }

  function setChapterHidden(hidden: boolean): void {
    document.documentElement.classList.toggle("chapter-hidden", hidden);
  }

  function setPageTurning(turning: boolean): void {
    const enabled = turning && isPagedMode;
    document.documentElement.classList.toggle("page-turning", enabled);
    if (!enabled && pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
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
    resetBoundary();
    killScrollMomentum();
    document.body.style.opacity = "0";
    document.documentElement.style.overflow = "hidden";
    setChapterHidden(true);
    setPageTurning(false);
  }

  function getPageStride(): number {
    const content = getContentEl();
    return Math.max(1, content?.clientWidth || window.innerWidth || 1);
  }

  function getMaxPageScrollLeft(): number {
    const content = getContentEl();
    if (!content) return 0;
    return Math.max(0, content.scrollWidth - content.clientWidth);
  }

  function getLastMeaningfulRect(root: Node): DOMRect | null {
    for (let node = root.lastChild; node; node = node.previousSibling) {
      if (node.nodeType === Node.TEXT_NODE) {
        const text = node.textContent ?? "";
        let end = text.length;
        while (end > 0 && /\s/u.test(text[end - 1])) end--;
        if (end === 0) continue;

        const range = document.createRange();
        range.setStart(node, Math.max(0, end - 1));
        range.setEnd(node, end);

        const rects = range.getClientRects();
        if (rects.length > 0) return rects[rects.length - 1];

        const rect = range.getBoundingClientRect();
        if (rect.width > 0 || rect.height > 0) return rect;
        continue;
      }

      if (node.nodeType !== Node.ELEMENT_NODE) continue;

      const el = node as Element;
      if (el.tagName === "SCRIPT" || el.tagName === "STYLE") continue;

      const nested = getLastMeaningfulRect(node);
      if (nested) return nested;

      if (el.tagName === "BR") continue;

      const rects = el.getClientRects();
      if (rects.length > 0) return rects[rects.length - 1];

      const rect = el.getBoundingClientRect();
      if (rect.width > 0 || rect.height > 0) return rect;
    }

    return null;
  }

  function calculateTotalPages(): number {
    const content = getContentEl();
    if (!content) return 1;

    const stride = getPageStride();
    if (stride <= 0) return 1;

    const lastRect = getLastMeaningfulRect(getContentInnerEl());
    if (lastRect) {
      const contentRect = content.getBoundingClientRect();
      const rightEdge =
        lastRect.right - contentRect.left + content.scrollLeft - 1;
      return Math.max(1, Math.floor(Math.max(0, rightEdge) / stride) + 1);
    }

    return Math.max(1, Math.ceil(getMaxPageScrollLeft() / stride) + 1);
  }

  function reportPagePosition(): void {
    sendMessage({
      type: "position",
      seq: activeSeq,
      chapterIndex: activeChapterIndex,
      percent: totalPages > 1 ? currentPage / (totalPages - 1) : 0,
    });
  }

  function applyPageScroll(page: number, animated: boolean): void {
    const content = getContentEl();
    if (!content) return;

    const target = Math.max(
      0,
      Math.min(page * getPageStride(), getMaxPageScrollLeft()),
    );

    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }

    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }

    if (!animated) {
      content.scrollLeft = target;
      setPageTurning(false);
      return;
    }

    const start = content.scrollLeft;
    const delta = target - start;
    if (Math.abs(delta) < 1) {
      content.scrollLeft = target;
      setPageTurning(false);
      return;
    }

    setPageTurning(true);

    const distancePages = Math.max(1, Math.abs(delta) / getPageStride());
    const duration = Math.min(
      PAGE_TURN_MAX_MS,
      PAGE_TURN_BASE_MS + distancePages * PAGE_TURN_PER_PAGE_MS,
    );
    const startTime = performance.now();

    const animate = (now: number): void => {
      if (destroyed) return;

      const t = Math.min((now - startTime) / duration, 1);
      const ease = t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;

      content.scrollLeft = Math.round(start + delta * ease);

      if (t < 1) {
        pageScrollRafHandle = requestAnimationFrame(animate);
      } else {
        content.scrollLeft = target;
        pageScrollRafHandle = null;
        const timer = setTimeout(() => {
          if (pageTurnFinishTimer === timer) pageTurnFinishTimer = null;
          if (!destroyed) setPageTurning(false);
        }, 34);
        pageTurnFinishTimer = timer;
      }
    };

    pageScrollRafHandle = requestAnimationFrame(animate);
  }

  function goToPageInternal(page: number, animated: boolean): void {
    currentPage = Math.max(0, Math.min(totalPages - 1, page));
    applyPageScroll(currentPage, animated);
    sendMessage({
      type: "page-changed",
      seq: activeSeq,
      current: currentPage + 1,
      total: totalPages,
    });
    reportPagePosition();
    updatePageIndicator();
  }

  function nextPage(): void {
    if (totalPages === 0) return;
    if (currentPage >= totalPages - 1) {
      if (hasNextChapter) {
        sendMessage({ type: "at-boundary", seq: activeSeq, boundary: "end" });
      }
      return;
    }
    goToPageInternal(currentPage + 1, true);
  }

  function prevPage(): void {
    if (totalPages === 0) return;
    if (currentPage <= 0) {
      if (hasPrevChapter) {
        sendMessage({ type: "at-boundary", seq: activeSeq, boundary: "start" });
      }
      return;
    }
    goToPageInternal(currentPage - 1, true);
  }

  function setPagedHeights(): void {
    const height = window.innerHeight;
    const clip = getClipEl();
    const content = getContentEl();
    if (clip) clip.style.height = height + "px";
    if (content) content.style.height = height + "px";
  }

  function getElementPageIndex(el: Element): number {
    const content = getContentEl();
    if (!content) return 0;
    const stride = getPageStride();
    if (stride <= 0) return 0;

    const contentRect = content.getBoundingClientRect();
    const rect = el.getBoundingClientRect();
    const x = rect.left - contentRect.left + content.scrollLeft;
    return Math.max(0, Math.min(totalPages - 1, Math.floor(x / stride)));
  }

  function scrollToFragmentPaged(id: string): void {
    const el = document.getElementById(id);
    if (!el) return;
    if (totalPages <= 0) {
      el.scrollIntoView({ behavior: "auto" });
      return;
    }
    goToPageInternal(getElementPageIndex(el), true);
  }

  function relayoutPagedContentPreservingPosition(): void {
    if (!isPagedMode || destroyed) return;
    setPagedHeights();
    const ratio = totalPages > 1 ? currentPage / (totalPages - 1) : 0;
    totalPages = calculateTotalPages();
    currentPage = Math.max(
      0,
      Math.min(Math.round(ratio * (totalPages - 1)), totalPages - 1),
    );
    applyPageScroll(currentPage, false);
    sendMessage({
      type: "page-changed",
      seq: activeSeq,
      current: currentPage + 1,
      total: totalPages,
    });
    reportPagePosition();
    updatePageIndicator();
  }

  function scheduleFinalFontRelayout(seqAtStart: number): void {
    document.fonts.ready.then(() => {
      if (
        destroyed ||
        activeSeq !== seqAtStart ||
        !isPagedMode ||
        !contentReady
      )
        return;

      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (
            destroyed ||
            activeSeq !== seqAtStart ||
            !isPagedMode ||
            !contentReady
          ) {
            return;
          }
          relayoutPagedContentPreservingPosition();
        }),
      );
    });
  }

  function revealPagedShell(seqAtStart: number): void {
    if (destroyed || activeSeq !== seqAtStart) return;
    document.body.style.opacity = "1";
    document.documentElement.style.overflow = "";
    ensureIndicatorElements();
    updateBoundaryState();
    updatePageIndicator();
    setupPagedResizeObserver();
    requestAnimationFrame(() => {
      if (!destroyed && activeSeq === seqAtStart) {
        setChapterHidden(false);
      }
    });
    scheduleFinalFontRelayout(seqAtStart);
  }

  function restorePagedPosition(
    scrollTarget: "top" | "end",
    restorePercent: number | null,
  ): void {
    const seqAtStart = activeSeq;
    const content = getContentEl();
    if (content) content.scrollLeft = 0;

    setPageTurning(false);
    setPagedHeights();
    totalPages = calculateTotalPages();

    if (pendingFragment) {
      const fragment = pendingFragment;
      pendingFragment = null;

      requestAnimationFrame(() => {
        if (destroyed || activeSeq !== seqAtStart) return;
        scrollToFragmentPaged(fragment);
        revealPagedShell(seqAtStart);
      });
      return;
    }

    currentPage =
      restorePercent !== null && totalPages > 1
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
    sendMessage({
      type: "page-changed",
      seq: activeSeq,
      current: currentPage + 1,
      total: totalPages,
    });
    reportPagePosition();

    requestAnimationFrame(() => revealPagedShell(seqAtStart));
  }

  function handlePagedResize(): void {
    if (!isPagedMode || !contentReady) return;
    if (pagedResizeDebounce) clearTimeout(pagedResizeDebounce);

    pagedResizeDebounce = setTimeout(() => {
      if (destroyed) return;
      pagedResizeDebounce = null;
      relayoutPagedContentPreservingPosition();
    }, 120);
  }

  function setupPagedResizeObserver(): void {
    teardownPagedResizeObserver();
    pagedResizeObserver = new ResizeObserver(handlePagedResize);
    pagedResizeObserver.observe(document.documentElement);
    window.addEventListener("resize", handlePagedResize);
  }

  function teardownPagedResizeObserver(): void {
    if (pagedResizeObserver) {
      pagedResizeObserver.disconnect();
      pagedResizeObserver = null;
    }
    window.removeEventListener("resize", handlePagedResize);
    if (pagedResizeDebounce) {
      clearTimeout(pagedResizeDebounce);
      pagedResizeDebounce = null;
    }
  }

  interface IframeSettings {
    mode: "scroll" | "paged" | "paged-two";
    fontSize: number;
    fontFamily: string;
    preserveBookStyles: boolean;
    preserveBookFonts: boolean;
    lineHeight: number | null;
    paragraphSpacing: number | null;
    textIndent: number | null;
    contentWidth: number | null;
    margins: { top: number | null; bottom: number | null; side: number | null };
    justify: boolean;
    hyphenation: boolean;
    theme: string;
    chapterTitleAlign: "left" | "center" | "right" | null;
    chapterTitleSize: number | null;
    chapterTitleSpacing: number | null;
  }

  interface LoadMessage {
    type: "load";
    seq: number;
    chapterIndex: number;
    css?: string;
    fontFaceCSS?: string;
    direction?: string;
    writingMode?: string;
    html?: string;
    scrollTo?: "top" | "end";
    fragment?: string | null;
    hasNext?: boolean;
    hasPrev?: boolean;
    restorePercent?: number | null;
    restoreCfi?: string | null;
    origin?: string;
  }

  type ParentMessage =
    | { type: "destroy" }
    | { type: "set-font-faces"; fontFaces?: string }
    | LoadMessage
    | { type: "apply-settings"; settings: IframeSettings }
    | { type: "scroll-to"; percent: number }
    | { type: "scroll-to-end" }
    | { type: "next-page" }
    | { type: "prev-page" }
    | { type: "go-to-page"; page?: number }
    | { type: "go-to-last-page" }
    | { type: "scroll-to-fragment"; id?: string }
    | { type: "scroll-to-cfi"; cfi?: string }
    | { type: "get-position" }
    | {
        type: "highlight-search";
        charOffset?: number;
        matchLen?: number;
        query?: unknown;
      }
    | { type: "clear-highlights" };

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
    ensureIndicatorElements();
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
        reportPositionRafHandle = requestAnimationFrame(reportPosition);
      });
      return;
    }

    window.scrollTo({
      top: scrollTarget === "end" ? document.documentElement.scrollHeight : 0,
      behavior: "instant" as ScrollBehavior,
    });

    if (restoreCfi) {
      const el = resolveCFILocal(restoreCfi);
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
            loadScrollTarget = null;
            loadRestorePercent = null;
            loadRestoreCfi = null;
            restorePagedPosition(target, pagedRestore);
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
        }),
      ),
    );
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
    getStyleEl("font-face-css").textContent = fontFaceContent;

    let bookCSS = "";
    if (settings.preserveBookStyles && settings.preserveBookFonts) {
      bookCSS = preparedRawCSS;
    } else if (settings.preserveBookStyles && !settings.preserveBookFonts) {
      bookCSS = preparedLayoutCSS;
    } else if (!settings.preserveBookStyles && settings.preserveBookFonts) {
      bookCSS = preparedFontCSS;
    }
    getStyleEl("book-css").textContent = bookCSS;

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
      css.push(`#content-inner { padding: ${pt} ${ps} ${pb} !important; }`);
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
      // Double-page spread uses fixed safe insets. User margin sliders do not
      // affect this mode so the spread geometry stays stable and predictable.
      const pt = "24px";
      const pb = "24px";
      const ps = "48px";

      css.push(
        `html { --paged-padding-top: ${pt}; --paged-padding-bottom: ${pb}; --paged-padding-side: ${ps}; }`,
      );
      css.push(
        "body { padding: 0 !important; height: 100vh !important; overflow: hidden !important; }",
      );
      css.push(`#content-inner { padding: ${pt} ${ps} ${pb} !important; }`);
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
          `#content { max-width: ${settings.contentWidth}px !important; margin-left: auto !important; margin-right: auto !important; }`,
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
        `h1, h2, h3 { text-align: ${settings.chapterTitleAlign} !important; }`,
      );
    }
    if (settings.chapterTitleSize != null) {
      css.push(
        `h1, h2, h3 { font-size: ${settings.chapterTitleSize}px !important; }`,
      );
    }
    if (settings.chapterTitleSpacing != null) {
      css.push(
        `h1, h2, h3 { margin-bottom: ${settings.chapterTitleSpacing}em !important; }`,
      );
    }

    getStyleEl("override-css").textContent = css.join("\n");

    isPagedMode = settings.mode === "paged" || settings.mode === "paged-two";
    applyRootClasses(settings.theme, settings.mode);

    if (!contentReady) {
      contentReady = true;
      runInitialLayoutRestore();
    } else if (isPagedMode) {
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          if (destroyed) return;
          relayoutPagedContentPreservingPosition();
        }),
      );
    }
  }

  function scrollToFragmentById(id: string): void {
    if (isPagedMode) {
      scrollToFragmentPaged(id);
      resetBoundary();
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

    el.scrollIntoView({ behavior: "smooth" });
    resetBoundary();
  }

  // Jump to a previously reported CFI within the *current* chapter (e.g. a
  // bookmark in the chapter already on screen). Mirrors scrollToFragmentById
  // but resolves the position via the CFI path instead of an element id.
  function scrollToCfiLocal(cfi: string): void {
    const el = resolveCFILocal(cfi);
    if (!el) return;

    if (isPagedMode) {
      if (totalPages <= 0) {
        el.scrollIntoView({ behavior: "auto" });
      } else {
        goToPageInternal(getElementPageIndex(el), true);
      }
      resetBoundary();
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

    el.scrollIntoView({ behavior: "smooth" });
    resetBoundary();
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
      reportPagePosition();
      return;
    }

    const percent = getScrollPercent();
    const el = findFirstVisibleBlock();
    let cfi: string | null = null;

    if (el) {
      if (el === lastReportedAnchor) {
        cfi = lastReportedCfi;
      } else {
        cfi = generateCFIForElement(el);
        lastReportedAnchor = el;
        lastReportedCfi = cfi;
      }
    } else {
      lastReportedAnchor = null;
      lastReportedCfi = null;
    }

    const msg: Record<string, unknown> = {
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
      if (!atTop && !atBottom && boundaryDirection) resetBoundary();
    });
  }

  function handleWheel(e: WheelEvent): void {
    if (destroyed || !contentReady) return;
    if (isPagedMode) return;
    updateBoundaryState();
    if (atTop && e.deltaY < 0) {
      accumulateBoundary(Math.abs(e.deltaY), "start", WHEEL_THRESHOLD);
      return;
    }
    if (atBottom && e.deltaY > 0) {
      accumulateBoundary(Math.abs(e.deltaY), "end", WHEEL_THRESHOLD);
      return;
    }
    if (boundaryDirection) resetBoundary();
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

  function processTouchBoundary(
    direction: "start" | "end",
    hasAdjacentChapter: boolean,
    boundaryDelta: number,
  ): void {
    if (boundaryDirection !== direction) {
      boundaryAccum = 0;
      boundaryDirection = direction;
    }
    boundaryAccum = boundaryDelta;
    scheduleBoundaryReset();
    if (!hasAdjacentChapter) {
      updateBoundaryIndicator(
        direction === "end" ? "edge-end" : "edge-start",
        Math.min(1, boundaryDelta / 40),
      );
      return;
    }
    updateBoundaryIndicator(
      direction,
      Math.min(1, boundaryAccum / TOUCH_THRESHOLD),
    );
    if (boundaryAccum >= TOUCH_THRESHOLD && !boundarySent) {
      boundarySent = true;
      updateBoundaryIndicator(direction, 1);
      sendMessage({ type: "at-boundary", seq: activeSeq, boundary: direction });
      if (boundaryFireTimer) clearTimeout(boundaryFireTimer);
      boundaryFireTimer = setTimeout(resetBoundary, 300);
    }
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
        processTouchBoundary("end", hasNextChapter, boundaryDelta);
      }
      return;
    }

    if (atTop && totalDeltaY < 0) {
      if (touchBoundaryBase === 0) {
        touchBoundaryBase = touchAtBoundaryOnStart ? 0 : totalDeltaY;
      }
      const boundaryDelta = Math.abs(totalDeltaY - touchBoundaryBase);
      if (boundaryDelta > 0) {
        processTouchBoundary("start", hasPrevChapter, boundaryDelta);
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
          isRTL ? prevPage() : nextPage();
        } else {
          isRTL ? nextPage() : prevPage();
        }
      }
      return;
    }

    touchBoundaryBase = 0;
    touchAtBoundaryOnStart = false;
    if (!boundarySent) resetBoundary();
  }

  function handleKeyDown(e: KeyboardEvent): void {
    if (destroyed) return;

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

  interface DOMPoint {
    node: Text;
    offset: number;
  }

  interface SearchTextIndex {
    positions: Array<DOMPoint | null>;
    length: number;
  }

  function isSpaceLike(char: string): boolean {
    return /\s/u.test(char);
  }

  function isTextBoundaryElement(node: Element): boolean {
    return (
      TEXT_BOUNDARY_TAGS.has(node.tagName) ||
      node.tagName === "SVG" ||
      node.tagName === "MATH"
    );
  }

  function buildSearchTextIndex(root: HTMLElement): SearchTextIndex {
    const positions: Array<DOMPoint | null> = [];
    let length = 0;
    let pendingSpace = false;
    let pendingSpaceStart: DOMPoint | null = null;
    let pendingSpaceEnd: DOMPoint | null = null;
    let lastPoint: DOMPoint | null = null;

    function queueBoundary(): void {
      if (length === 0) return;
      pendingSpace = true;
      if (!pendingSpaceStart) pendingSpaceStart = lastPoint;
      if (!pendingSpaceEnd) pendingSpaceEnd = lastPoint;
    }

    function emitPendingSpace(nextPoint: DOMPoint): void {
      if (!pendingSpace || length === 0) return;
      const start = pendingSpaceStart ?? lastPoint ?? nextPoint;
      const end = pendingSpaceEnd ?? nextPoint;
      positions[length] = start;
      length += 1;
      positions[length] = end;
      lastPoint = end;
      pendingSpace = false;
      pendingSpaceStart = null;
      pendingSpaceEnd = null;
    }

    function emitTextNode(node: Text): void {
      let utf16Offset = 0;

      for (const char of node.data) {
        const startOffset = utf16Offset;
        utf16Offset += char.length;
        const endOffset = utf16Offset;

        const startPoint: DOMPoint = { node, offset: startOffset };
        const endPoint: DOMPoint = { node, offset: endOffset };

        if (isSpaceLike(char)) {
          if (length > 0) {
            pendingSpace = true;
            pendingSpaceStart = pendingSpaceStart ?? lastPoint ?? startPoint;
            pendingSpaceEnd = endPoint;
          }
          continue;
        }

        emitPendingSpace(startPoint);
        positions[length] = startPoint;
        length += 1;
        positions[length] = endPoint;
        lastPoint = endPoint;
        pendingSpaceEnd = endPoint;
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

      if (tag === "HEAD" || tag === "SCRIPT" || tag === "STYLE") return;
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
    return { positions, length };
  }

  function wrapRangeInMark(range: Range): HTMLElement | null {
    const mark = document.createElement("mark");
    mark.className = "search-highlight search-highlight-active";

    try {
      range.surroundContents(mark);
      return mark;
    } catch {
      try {
        const contents = range.extractContents();
        mark.appendChild(contents);
        range.insertNode(mark);
        return mark;
      } catch {
        return null;
      }
    }
  }

  function highlightSearchMatch(
    charOffset: number,
    matchLen: number,
    query: string,
  ): void {
    clearSearchHighlights();
    const content = getContentEl();
    if (!contentReady || !content || matchLen <= 0 || charOffset < 0) return;

    const matchEnd = charOffset + matchLen;
    const index = buildSearchTextIndex(content);
    if (matchEnd > index.length) {
      if (query) fallbackHighlight(query);
      return;
    }

    const start = index.positions[charOffset];
    const end = index.positions[matchEnd];
    if (!start || !end) {
      if (query) fallbackHighlight(query);
      return;
    }

    try {
      const range = document.createRange();
      range.setStart(start.node, start.offset);
      range.setEnd(end.node, end.offset);

      if (range.collapsed) {
        if (query) fallbackHighlight(query);
        return;
      }

      const mark = wrapRangeInMark(range);
      if (!mark) {
        if (query) fallbackHighlight(query);
        return;
      }

      if (isPagedMode) {
        goToPageInternal(getElementPageIndex(mark), true);
      } else {
        mark.scrollIntoView({ behavior: "smooth", block: "center" });
      }
    } catch {
      if (query) fallbackHighlight(query);
    }
  }

  function fallbackHighlight(query: string): void {
    const content = getContentEl();
    if (!content) return;

    const lowerQuery = query.toLowerCase();
    const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode() as Text | null;

    while (node) {
      const index = node.data.toLowerCase().indexOf(lowerQuery);
      if (index !== -1 && index + lowerQuery.length <= node.length) {
        try {
          const range = document.createRange();
          range.setStart(node, index);
          range.setEnd(node, index + lowerQuery.length);
          const mark = wrapRangeInMark(range);
          if (mark) {
            if (isPagedMode) {
              goToPageInternal(getElementPageIndex(mark), true);
            } else {
              mark.scrollIntoView({ behavior: "smooth", block: "center" });
            }
          }
        } catch {
          // Skip nodes that cannot be safely surrounded.
        }
        return;
      }
      node = walker.nextNode() as Text | null;
    }
  }

  function clearSearchHighlights(): void {
    for (const mark of document.querySelectorAll("mark.search-highlight")) {
      const parent = mark.parentNode;
      mark.replaceWith(document.createTextNode(mark.textContent ?? ""));
      parent?.normalize();
    }
  }

  function commitLoad(msg: LoadMessage): void {
    activeSeq = msg.seq;
    activeChapterIndex = msg.chapterIndex;
    rawBookCSS = msg.css || "";
    bookFontFaceCSS = msg.fontFaceCSS || "";
    prepareChapterCSS();

    contentReady = false;
    isPagedMode = false;
    currentPage = 0;
    totalPages = 0;
    isRTL = msg.direction === "rtl";
    teardownPagedResizeObserver();

    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }

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

    beginChapterSwapOut();
    _contentInnerEl = null;

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

    if (boundaryResetTimer) {
      clearTimeout(boundaryResetTimer);
      boundaryResetTimer = null;
    }
    if (boundaryFireTimer) {
      clearTimeout(boundaryFireTimer);
      boundaryFireTimer = null;
    }
    if (pagedResizeDebounce) {
      clearTimeout(pagedResizeDebounce);
      pagedResizeDebounce = null;
    }
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
    pendingSettingsMessage = null;
    if (pageScrollRafHandle !== null) {
      cancelAnimationFrame(pageScrollRafHandle);
      pageScrollRafHandle = null;
    }
    if (pageTurnFinishTimer !== null) {
      clearTimeout(pageTurnFinishTimer);
      pageTurnFinishTimer = null;
    }
    if (reportPositionRafHandle !== null) {
      cancelAnimationFrame(reportPositionRafHandle);
      reportPositionRafHandle = null;
    }
    if (scrollRafHandle !== null) {
      cancelAnimationFrame(scrollRafHandle);
      scrollRafHandle = null;
    }

    teardownPagedResizeObserver();

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
    const msg = raw as ParentMessage;

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
          resetBoundary();
          updateBoundaryState();
        }
        break;

      case "scroll-to-end":
        if (contentReady) {
          window.scrollTo({
            top: document.documentElement.scrollHeight,
            behavior: "instant" as ScrollBehavior,
          });
          resetBoundary();
          updateBoundaryState();
        }
        break;

      case "next-page":
        if (isPagedMode) nextPage();
        break;

      case "prev-page":
        if (isPagedMode) prevPage();
        break;

      case "go-to-page":
        if (isPagedMode && typeof msg.page === "number") {
          goToPageInternal(msg.page - 1, false);
        }
        break;

      case "go-to-last-page":
        if (isPagedMode) goToPageInternal(totalPages - 1, false);
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
        highlightSearchMatch(
          msg.charOffset,
          msg.matchLen,
          String(msg.query || ""),
        );
        break;

      case "clear-highlights":
        clearSearchHighlights();
        break;
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
