<script lang="ts">
  import { onMount, untrack } from "svelte";
  import {
    isProgressDuplicate,
    chooseBootProgress,
    isBookmarkAtPosition,
  } from "~/lib/progress";
  import {
    getBook,
    getProgress,
    saveProgress,
    beaconProgress,
    fetchChapter,
    getBookmarks,
    createBookmark,
    updateBookmark,
    deleteBookmark,
    type BookDetail,
    type ChapterData,
    type ProgressData,
    type Bookmark,
    type SearchResult,
  } from "~/api/client";
  import { settings, type IframeSettings } from "~/lib/settings.svelte";
  import { toast } from "~/lib/toast.svelte";
  import { fontRegistry } from "~/lib/fontRegistry.svelte";
  import { buildAllFontFaces } from "~/lib/readerFontFaces";
  import { router } from "~/lib/router.svelte";
  import { ui } from "~/lib/ui.svelte";
  import {
    resolveHref,
    findTocLabel,
    buildTocChapterEntries,
  } from "~/lib/href";
  import { getErrorMessage } from "~/lib/errors";
  import { applyTheme } from "~/lib/theme";
  import ChapterFrame from "~/components/reader/ChapterFrame.svelte";
  import type {
    ChapterFrameAPI,
    KeyEvent,
  } from "~/components/reader/frame-types";

  // Reader side-panels load on first open so they don't inflate the reader
  // route's initial JS bundle. import() results are module-cached, so reopening
  // a panel is instant.
  const tocPanel = () => import("~/components/reader/TocPanel.svelte");
  const settingsPanel = () =>
    import("~/components/reader/SettingsPanel.svelte");
  const searchPanel = () => import("~/components/reader/SearchPanel.svelte");
  const bookmarksPanel = () =>
    import("~/components/reader/BookmarksPanel.svelte");
  import Icon from "~/lib/Icon.svelte";
  import { focusTrap } from "~/lib/focusTrap";
  import {
    ArrowLeft,
    Bookmark as BookmarkIcon,
    BookmarkCheck,
    BookMarked,
    Search,
    Settings,
    List,
    CircleHelp,
  } from "@lucide/svelte";

  interface Props {
    bookId: string;
  }
  let { bookId }: Props = $props();

  const PROGRESS_FLUSH_THROTTLE_MS = 5_000;
  const PROGRESS_SAVE_INTERVAL_MS = 15_000;
  const BOUNDARY_COOLDOWN_MS = 400;
  const CHAPTER_LOAD_RETRY_ATTEMPTS = 3;
  // Each cached chapter retains full decoded HTML + CSS, so this is the largest
  // retained-memory knob in the reader. Active navigation only needs the
  // current chapter ± 1 (3 chapters); 4 keeps one extra recently-read chapter
  // for instant back-nav without holding several full chapters' worth of
  // detached HTML/CSS strings in memory.
  const MAX_CHAPTER_CACHE = 4;
  // bookId is read once at mount; App.svelte wraps Read in {#key params.id} so a
  // different book remounts this component rather than mutating bookId in place.
  // svelte-ignore state_referenced_locally
  const progressCacheKey = `sayumi:progress:${bookId}`;

  // Reactive (rendered) state.
  let book = $state<BookDetail | null>(null);
  let currentChapter = $state(0);
  let chapterPercent = $state(0);
  let chapterDirection = $state("ltr");
  let chapterLoading = $state(false);
  let error = $state("");
  type Panel = "none" | "toc" | "settings" | "search" | "bookmarks";
  let activePanel = $state<Panel>("none");
  let bookmarks = $state<Bookmark[]>([]);
  let chromeVisible = $state(true);
  // Cache the resolved TocPanel component so reopening renders synchronously,
  // instead of an {#await} that flashes one empty frame while the
  // module-cached dynamic import settles on a microtask. Prewarmed on idle in
  // onMount so the very first open is instant too.
  let TocPanelComp = $state<
    Awaited<ReturnType<typeof tocPanel>>["default"] | null
  >(null);
  let SettingsPanelComp = $state<
    Awaited<ReturnType<typeof settingsPanel>>["default"] | null
  >(null);
  let SearchPanelComp = $state<
    Awaited<ReturnType<typeof searchPanel>>["default"] | null
  >(null);
  let BookmarksPanelComp = $state<
    Awaited<ReturnType<typeof bookmarksPanel>>["default"] | null
  >(null);

  const isPaged = $derived(settings.value.displayMode !== "scroll");
  const isRTL = $derived(chapterDirection === "rtl");
  // When a modal panel (toc/settings/search/bookmarks) is open, the reader
  // chrome + iframe behind it must leave the tab + AT order. focusTrap only
  // traps Tab; `inert` also pulls the background out of the accessibility tree.
  const panelOpen = $derived(activePanel !== "none");
  // Combined reader @font-face CSS (embedded + user families), recomputed when
  // the user font registry or the per-family role mapping change.
  const fontFaceCSS = $derived(
    buildAllFontFaces(fontRegistry.families, settings.value.fontRoles),
  );
  const chapterLabel = $derived(
    book ? findTocLabel(book.toc, book.spine, currentChapter) : "",
  );
  // A bookmark at (or very near) the current reading position, if any.
  const currentBookmarkId = $derived(
    bookmarks.find((b) =>
      isBookmarkAtPosition(b, currentChapter, chapterPercent),
    )?.id ?? null,
  );
  // Active TOC entry to highlight per chapter. Built once per book
  // (O(toc + spine)) rather than re-resolving every TOC entry each time the
  // panel opens — that per-open walk made the TOC slow to open in books with
  // many chapters. We track the entry object itself (not its href) so exactly
  // one node is highlighted even when two entries share an href (e.g. a
  // top-level "book title" entry pointing at the same file as a chapter).
  const tocChapterEntries = $derived.by(() =>
    book ? buildTocChapterEntries(book.toc, book.spine) : null,
  );
  // O(1) lookup of the active entry for the current chapter. A chapter with no
  // TOC line of its own inherits the nearest preceding heading (filled forward
  // in buildTocChapterEntries).
  const activeTocEntry = $derived(
    tocChapterEntries ? (tocChapterEntries[currentChapter] ?? null) : null,
  );

  // Non-reactive instance state.
  let api: ChapterFrameAPI | null = null;
  let frameReady = false;
  let panelPrewarm: { cancel: () => void } | null = null;
  let bookLoaded = false;
  let initialLoadDone = false;
  let saveData: ProgressData = { chapter: 0, percent: 0 };
  const chapterCache = new Map<number, ChapterData>();
  let chapterLoadInProgress = false;
  let pendingNav: {
    index: number;
    scrollTo: "top" | "end";
    fragment?: string;
    restore?: { percent: number; cfi?: string };
  } | null = null;
  let pendingHighlight: {
    chapterIndex: number;
    charOffset: number;
    matchLen: number;
    query: string;
  } | null = null;
  let highlightTimer: ReturnType<typeof setTimeout> | undefined;
  let chromeHideTimer: ReturnType<typeof setTimeout> | undefined;
  let fetchAbort: AbortController | null = null;
  let progressTimer: ReturnType<typeof setTimeout> | undefined;
  let lastFlushTime = 0;
  let lastPersistedChapter = -1;
  let lastPersistedPercent = -1;
  let lastBoundaryTime = 0;

  const CHROME_AUTO_HIDE_MS = 4000;

  function cancelPendingHighlight(): void {
    pendingHighlight = null;
    if (highlightTimer) {
      clearTimeout(highlightTimer);
      highlightTimer = undefined;
    }
  }

  // Single choke point for panel changes. Leaving the search panel drops its
  // in-book highlight as an explicit side effect of the transition, instead of
  // a reactive $effect that watches activePanel on every change.
  function setPanel(p: Panel): void {
    if (p === activePanel) return;
    if (activePanel === "search" && p !== "search") {
      cancelPendingHighlight();
      api?.clearHighlights();
    }
    activePanel = p;
  }
  function togglePanel(p: Panel): void {
    setPanel(activePanel === p ? "none" : p);
    // A panel is part of the chrome — keep it visible while open.
    if (activePanel !== "none") showChrome(false);
    else resetChromeTimer();
  }
  function closePanel(): void {
    if (activePanel === "none") return;
    setPanel("none");
    resetChromeTimer();
  }
  function showToast(msg: string): void {
    toast.show(msg);
  }

  // ---- chrome auto-hide ----------------------------------------------------
  function resetChromeTimer(): void {
    if (chromeHideTimer) {
      clearTimeout(chromeHideTimer);
      chromeHideTimer = undefined;
    }
    // Never auto-hide while a panel is open.
    if (activePanel === "none") {
      chromeHideTimer = setTimeout(
        () => (chromeVisible = false),
        CHROME_AUTO_HIDE_MS,
      );
    }
  }
  function showChrome(arm = true): void {
    chromeVisible = true;
    if (arm) resetChromeTimer();
    else if (chromeHideTimer) {
      clearTimeout(chromeHideTimer);
      chromeHideTimer = undefined;
    }
  }
  function toggleChrome(): void {
    if (chromeVisible) {
      chromeVisible = false;
      if (chromeHideTimer) {
        clearTimeout(chromeHideTimer);
        chromeHideTimer = undefined;
      }
    } else {
      showChrome();
    }
  }
  // pointermove fires continuously; resetting the auto-hide timer on every
  // event churns timers needlessly. Poke it at most a couple times a second —
  // the 4s hide window makes the small delay imperceptible.
  let lastChromePoke = 0;
  function handlePointerActivity(): void {
    if (!chromeVisible) {
      chromeVisible = true;
      lastChromePoke = Date.now();
      resetChromeTimer();
      return;
    }
    const now = Date.now();
    if (now - lastChromePoke < 500) return;
    lastChromePoke = now;
    resetChromeTimer();
  }

  // Pushes the current @font-face CSS into the iframe. The frame only stores it;
  // a following applySettings() (or chapter load) re-injects it into the DOM.
  function pushFontFaces(): void {
    api?.setFontFaces(fontFaceCSS);
  }

  // Coalesce settings pushes into one per animation frame: dragging a slider
  // fires oninput many times per frame, but the iframe only needs the latest
  // value. This keeps live preview smooth without flooding postMessage.
  let applyRaf: number | null = null;
  let pendingSettings: IframeSettings | null = null;
  function scheduleApplySettings(s: IframeSettings): void {
    pendingSettings = s;
    if (applyRaf !== null) return;
    applyRaf = requestAnimationFrame(() => {
      applyRaf = null;
      const next = pendingSettings;
      pendingSettings = null;
      if (next) api?.applySettings(next);
    });
  }

  // Re-inject the @font-face CSS only when it actually changes (a font role or
  // registry change) — not on every settings tweak. The frame stores the faces
  // and needs a following applySettings to inject them, so re-apply the current
  // settings without subscribing to them here (untrack), keeping this effect
  // keyed solely on fontFaceCSS.
  $effect(() => {
    const faces = fontFaceCSS;
    if (api && initialLoadDone) {
      api.setFontFaces(faces);
      scheduleApplySettings(untrack(() => settings.iframe));
    }
  });

  // Push reader settings whenever they change (after the first load). Split out
  // from the font-face effect so a settings change no longer re-sends the
  // (large) font-face CSS to the iframe.
  $effect(() => {
    const s = settings.iframe;
    if (api && initialLoadDone) scheduleApplySettings(s);
  });

  // Keep the app chrome (reader bar, panels) in sync with the reading theme.
  $effect(() => {
    applyTheme(settings.value.theme);
  });

  function preloadTocPanel(): void {
    if (TocPanelComp) return;
    void tocPanel()
      .then((m) => (TocPanelComp = m.default))
      .catch(() => {});
  }
  function preloadSettingsPanel(): void {
    if (SettingsPanelComp) return;
    void settingsPanel()
      .then((m) => (SettingsPanelComp = m.default))
      .catch(() => {});
  }
  function preloadSearchPanel(): void {
    if (SearchPanelComp) return;
    void searchPanel()
      .then((m) => (SearchPanelComp = m.default))
      .catch(() => {});
  }
  function preloadBookmarksPanel(): void {
    if (BookmarksPanelComp) return;
    void bookmarksPanel()
      .then((m) => (BookmarksPanelComp = m.default))
      .catch(() => {});
  }
  // Defer non-critical prewarming to idle time, with a setTimeout fallback for
  // browsers without requestIdleCallback. Returns a canceller for cleanup.
  function schedulePrewarm(fn: () => void): { cancel: () => void } {
    if (typeof window.requestIdleCallback === "function") {
      const handle = window.requestIdleCallback(fn);
      return { cancel: () => window.cancelIdleCallback(handle) };
    }
    const handle = window.setTimeout(fn, 200);
    return { cancel: () => window.clearTimeout(handle) };
  }

  onMount(() => {
    void boot();
    resetChromeTimer();
    // Warm the side-panel chunks on idle so their first open renders
    // synchronously instead of flashing a blank frame while the dynamic import
    // resolves.
    panelPrewarm = schedulePrewarm(() => {
      preloadTocPanel();
      preloadSettingsPanel();
      preloadSearchPanel();
      preloadBookmarksPanel();
    });
    return () => {
      cancelProgressSave();
      fetchAbort?.abort();
      if (highlightTimer) clearTimeout(highlightTimer);
      if (chromeHideTimer) clearTimeout(chromeHideTimer);
      if (applyRaf !== null) cancelAnimationFrame(applyRaf);
      panelPrewarm?.cancel();
    };
  });

  async function boot(): Promise<void> {
    try {
      await settings.load();
    } catch {
      // keep defaults
    }

    // User font families are non-blocking for startup; faces re-push on load.
    void fontRegistry.load();

    let saved: ProgressData = { chapter: 0, percent: 0 };
    let serverLoaded = false;
    try {
      saved = await getProgress(bookId);
      serverLoaded = true;
      lastPersistedChapter = saved.chapter;
      lastPersistedPercent = saved.percent;
    } catch {
      // first periodic save will retry
    }

    // Merge a possibly-newer local cache (written on the last page-hide beacon).
    try {
      const raw = localStorage.getItem(progressCacheKey);
      if (raw) {
        const cached: ProgressData = JSON.parse(raw);
        saved = chooseBootProgress(saved, cached, serverLoaded);
      }
    } catch {
      // ignore malformed cache
    }

    try {
      const data = await getBook(bookId);
      book = data;
      bookLoaded = true;
      const chapter = Math.max(
        0,
        Math.min(saved.chapter, data.chapterCount - 1),
      );
      currentChapter = chapter;
      saveData = { chapter, percent: saved.percent, cfi: saved.cfi };
      tryInitialLoad();
      scheduleProgressSave();
    } catch (err) {
      error = getErrorMessage(err, "Failed to load book");
    }

    // Bookmarks are non-blocking for reader startup.
    getBookmarks(bookId)
      .then((bms) => (bookmarks = bms))
      .catch(() => {});
  }

  function tryInitialLoad(): void {
    if (initialLoadDone || !bookLoaded || !frameReady || !api) return;
    initialLoadDone = true;
    const { chapter, percent, cfi } = saveData;
    // Restore when we have either a meaningful percent OR a stored CFI. A saved
    // CFI at the very start of a chapter (percent ≈ 0) is still a real position
    // worth honoring, so don't gate restoration on percent alone.
    if (percent > 0.001 || cfi)
      void loadChapter(chapter, "top", undefined, { percent, cfi });
    else void loadChapter(chapter, "top");
  }

  async function fetchChapterWithRetry(
    index: number,
    signal?: AbortSignal,
  ): Promise<ChapterData> {
    const cached = chapterCache.get(index);
    if (cached) {
      chapterCache.delete(index);
      chapterCache.set(index, cached); // refresh LRU position
      return cached;
    }
    const data = await fetchChapter(
      bookId,
      index,
      CHAPTER_LOAD_RETRY_ATTEMPTS,
      signal,
    );
    if (chapterCache.size >= MAX_CHAPTER_CACHE) {
      const lru = chapterCache.keys().next().value;
      if (lru !== undefined) chapterCache.delete(lru);
    }
    chapterCache.set(index, data);
    return data;
  }

  async function loadChapter(
    index: number,
    scrollTo: "top" | "end" = "top",
    fragment?: string,
    restore?: { percent: number; cfi?: string },
  ): Promise<void> {
    const b = book;
    if (!b || index < 0 || index >= b.chapterCount || !api) return;

    if (chapterLoadInProgress) {
      pendingNav = { index, scrollTo, fragment, restore };
      // Latest navigation should win immediately. If the current load is still
      // fetching a stale chapter, abort it so the finally block can drain the
      // pending navigation instead of waiting on and then rendering the stale
      // response first. Adjacent prefetches are intentionally separate and stay
      // best-effort/non-aborted.
      fetchAbort?.abort();
      return;
    }

    fetchAbort?.abort();
    fetchAbort = new AbortController();
    const { signal } = fetchAbort;

    void flushProgress();
    chapterLoadInProgress = true;
    chapterLoading = true;
    error = "";

    const nextPercent = restore ? restore.percent : scrollTo === "end" ? 1 : 0;
    const nextCFI = restore?.cfi;

    try {
      const data = await fetchChapterWithRetry(index, signal);
      const hasPrev = index > 0;
      const hasNext = index + 1 < b.chapterCount;

      // Ensure the iframe has the current font faces before it applies settings.
      pushFontFaces();
      if (restore) {
        api.loadChapter(
          data,
          settings.iframe,
          "top",
          undefined,
          hasPrev,
          hasNext,
          restore.percent,
          restore.cfi,
          b.language,
        );
      } else {
        api.loadChapter(
          data,
          settings.iframe,
          scrollTo,
          fragment,
          hasPrev,
          hasNext,
          undefined,
          undefined,
          b.language,
        );
      }

      currentChapter = index;
      chapterPercent = nextPercent;
      chapterDirection = data.direction === "rtl" ? "rtl" : "ltr";
      saveData = { chapter: index, percent: nextPercent, cfi: nextCFI };

      if (!pendingNav) {
        if (hasNext) void fetchChapterWithRetry(index + 1).catch(() => {});
        if (hasPrev) void fetchChapterWithRetry(index - 1).catch(() => {});
      }
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") return;
      error = getErrorMessage(err, "Failed to load chapter");
    } finally {
      chapterLoadInProgress = false;
      chapterLoading = false;
      const nav = pendingNav;
      if (nav) {
        pendingNav = null;
        void loadChapter(nav.index, nav.scrollTo, nav.fragment, nav.restore);
      }
    }
  }

  // ---- progress persistence -----------------------------------------------
  function flushProgress(force = false): Promise<void> {
    if (!bookLoaded) return Promise.resolve();
    const now = Date.now();
    if (!force && now - lastFlushTime < PROGRESS_FLUSH_THROTTLE_MS)
      return Promise.resolve();

    const { chapter, percent } = saveData;
    if (
      !force &&
      isProgressDuplicate(
        { chapter, percent },
        { chapter: lastPersistedChapter, percent: lastPersistedPercent },
      )
    ) {
      return Promise.resolve();
    }

    lastFlushTime = now;
    const payload = { ...saveData };
    return saveProgress(bookId, payload)
      .then(() => {
        lastPersistedChapter = payload.chapter;
        lastPersistedPercent = payload.percent;
        try {
          localStorage.removeItem(progressCacheKey);
        } catch {
          // ignore
        }
      })
      .catch(() => {
        // best-effort; interval or beacon will retry
      });
  }

  function scheduleProgressSave(): void {
    if (!bookLoaded || progressTimer) return;
    progressTimer = setTimeout(() => {
      progressTimer = undefined;
      void flushProgress();
      scheduleProgressSave();
    }, PROGRESS_SAVE_INTERVAL_MS);
  }

  function cancelProgressSave(): void {
    if (progressTimer) {
      clearTimeout(progressTimer);
      progressTimer = undefined;
    }
  }

  function handleVisibility(): void {
    if (!bookLoaded) return;
    if (document.visibilityState === "hidden") {
      cancelProgressSave();
      try {
        localStorage.setItem(progressCacheKey, JSON.stringify(saveData));
      } catch {
        // ignore
      }
      beaconProgress(bookId, { ...saveData });
    } else {
      scheduleProgressSave();
    }
  }

  // ---- frame event handlers -----------------------------------------------
  function handleApi(a: ChapterFrameAPI): void {
    api = a;
  }
  function handleReady(): void {
    frameReady = true;
    tryInitialLoad();
  }
  function handleLoaded(seq: number): void {
    // Apply a pending search highlight once the new chapter has settled.
    if (!pendingHighlight) return;
    const h = pendingHighlight;
    pendingHighlight = null;
    if (highlightTimer) clearTimeout(highlightTimer);
    if (h.chapterIndex !== currentChapter) return;
    // Tag the deferred highlight with the seq of the chapter it was computed
    // for. If a faster re-navigation supersedes this chapter before the timer
    // fires, the iframe drops the now-stale highlight instead of marking the
    // wrong chapter.
    highlightTimer = setTimeout(() => {
      highlightTimer = undefined;
      api?.highlightSearch(h.charOffset, h.matchLen, h.query, seq);
    }, 120);
  }
  function handlePosition(
    chapterIndex: number,
    percent: number,
    cfi?: string,
  ): void {
    const b = book;
    if (
      !b ||
      !Number.isSafeInteger(chapterIndex) ||
      chapterIndex < 0 ||
      chapterIndex >= b.chapterCount
    )
      return;
    const safePercent = Number.isFinite(percent)
      ? Math.min(1, Math.max(0, percent))
      : 0;
    chapterPercent = safePercent;
    // A position report can omit the CFI (e.g. no first visible block was
    // resolvable). Don't let that wipe a good CFI we already hold for the same
    // chapter — only overwrite when the report actually carries one.
    const keptCfi =
      cfi ?? (chapterIndex === saveData.chapter ? saveData.cfi : undefined);
    saveData = { chapter: chapterIndex, percent: safePercent, cfi: keptCfi };
  }
  function handleBoundary(boundary: "start" | "end"): void {
    const now = Date.now();
    if (now - lastBoundaryTime < BOUNDARY_COOLDOWN_MS || chapterLoadInProgress)
      return;
    lastBoundaryTime = now;
    const b = book;
    if (!b) return;
    if (boundary === "end" && currentChapter + 1 < b.chapterCount)
      void loadChapter(currentChapter + 1, "top");
    else if (boundary === "start" && currentChapter > 0)
      void loadChapter(currentChapter - 1, "end");
  }
  function handleFrameError(_code: string, message: string): void {
    // An in-iframe render failure: stop the spinner and show the error UI with
    // Retry instead of silently swallowing it.
    chapterLoadInProgress = false;
    chapterLoading = false;
    error = message || "Failed to render this chapter.";
  }

  function handleLinkClicked(href: string): void {
    const b = book;
    if (!b) return;
    const resolved = resolveHref(href, b.spine);
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter) {
      if (resolved.fragment) api?.scrollToFragment(resolved.fragment);
    } else {
      void loadChapter(
        resolved.chapterIndex,
        "top",
        resolved.fragment || undefined,
      );
    }
  }
  function handleTocNavigate(href: string): void {
    setPanel("none");
    const b = book;
    if (!b) return;
    const resolved = resolveHref(href, b.spine);
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter) {
      if (resolved.fragment) api?.scrollToFragment(resolved.fragment);
      else api?.scrollTo(0);
    } else {
      void loadChapter(
        resolved.chapterIndex,
        "top",
        resolved.fragment || undefined,
      );
    }
  }

  // ---- navigation + keyboard ----------------------------------------------
  function goPrev(): void {
    if (isPaged) api?.prevPage();
    else if (book && currentChapter > 0)
      void loadChapter(currentChapter - 1, "top");
  }
  function goNext(): void {
    if (isPaged) api?.nextPage();
    else if (book && currentChapter + 1 < book.chapterCount)
      void loadChapter(currentChapter + 1, "top");
  }
  function handleBack(): void {
    void flushProgress(true);
    router.navigate("/");
  }

  // ---- search -------------------------------------------------------------
  function isValidSearchResult(result: SearchResult, b: BookDetail): boolean {
    return (
      Number.isSafeInteger(result.chapterIndex) &&
      result.chapterIndex >= 0 &&
      result.chapterIndex < b.chapterCount &&
      Number.isSafeInteger(result.charOffset) &&
      result.charOffset >= 0 &&
      Number.isSafeInteger(result.matchLen) &&
      result.matchLen > 0 &&
      Number.isSafeInteger(result.charOffset + result.matchLen)
    );
  }

  function navigateToResult(result: SearchResult, query: string): void {
    const b = book;
    if (!b || !isValidSearchResult(result, b)) {
      cancelPendingHighlight();
      api?.clearHighlights();
      showToast("Search result is no longer available");
      return;
    }

    cancelPendingHighlight();
    api?.clearHighlights();
    if (result.chapterIndex === currentChapter) {
      api?.highlightSearch(result.charOffset, result.matchLen, query);
    } else {
      pendingHighlight = {
        chapterIndex: result.chapterIndex,
        charOffset: result.charOffset,
        matchLen: result.matchLen,
        query,
      };
      void loadChapter(result.chapterIndex, "top");
    }
  }

  // ---- bookmarks ----------------------------------------------------------
  async function toggleBookmark(): Promise<void> {
    const existingId = currentBookmarkId;
    if (existingId) {
      const prev = bookmarks;
      bookmarks = bookmarks.filter((b) => b.id !== existingId);
      try {
        await deleteBookmark(bookId, existingId);
        showToast("Bookmark removed");
      } catch (err) {
        bookmarks = prev;
        showToast(getErrorMessage(err, "Failed to remove bookmark"));
      }
      return;
    }
    try {
      const bm = await createBookmark(bookId, {
        chapter: currentChapter,
        percent: chapterPercent,
        cfi: saveData.cfi,
      });
      bookmarks = [...bookmarks, bm];
      showToast("Bookmark added");
    } catch (err) {
      showToast(getErrorMessage(err, "Failed to add bookmark"));
    }
  }

  function navigateBookmark(bm: Bookmark): void {
    setPanel("none");
    if (bm.chapter === currentChapter) {
      if (bm.cfi) api?.scrollToCfi(bm.cfi);
      else api?.scrollTo(bm.percent);
    } else {
      void loadChapter(bm.chapter, "top", undefined, {
        percent: bm.percent,
        cfi: bm.cfi,
      });
    }
  }

  async function removeBookmark(id: string): Promise<void> {
    const prev = bookmarks;
    bookmarks = bookmarks.filter((b) => b.id !== id);
    try {
      await deleteBookmark(bookId, id);
    } catch (err) {
      bookmarks = prev;
      showToast(getErrorMessage(err, "Failed to remove bookmark"));
    }
  }

  async function editBookmark(
    id: string,
    label: string,
    comment: string,
  ): Promise<void> {
    const prev = bookmarks;
    bookmarks = bookmarks.map((b) =>
      b.id === id ? { ...b, label, comment } : b,
    );
    try {
      const updated = await updateBookmark(bookId, id, { label, comment });
      bookmarks = bookmarks.map((b) => (b.id === id ? updated : b));
    } catch (err) {
      bookmarks = prev;
      showToast(getErrorMessage(err, "Failed to update bookmark"));
    }
  }

  // Returns true when the key was acted on, so the caller can cancel its
  // default (keeps a letter shortcut from also being typed into a panel input).
  function handleKeyAction(e: {
    key: string;
    ctrlKey?: boolean;
    metaKey?: boolean;
  }): boolean {
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
      ui.togglePalette();
      return true;
    }
    if (e.ctrlKey || e.metaKey) return false;
    // A global overlay (command palette / shortcuts help) owns the keyboard
    // while open, so reader shortcuts (Esc → back, arrows, etc.) must stand
    // down — otherwise Esc would close the modal *and* navigate to the library.
    if (ui.palette || ui.shortcuts) return false;
    switch (e.key) {
      case "?":
        // The book renders in an iframe; once it has focus the window-level
        // handler in App.svelte never sees this key, so open the modal here
        // (the iframe forwards keystrokes to handleKeyAction).
        ui.openShortcuts();
        return true;
      case "Escape":
        if (activePanel !== "none") {
          setPanel("none");
          resetChromeTimer();
        } else handleBack();
        return true;
      case "ArrowLeft":
        // In RTL paged mode, visual-left advances reading order, mirroring the
        // swipe handler so keyboard and touch agree.
        if (isPaged && isRTL) goNext();
        else goPrev();
        return true;
      case "ArrowRight":
        if (isPaged && isRTL) goPrev();
        else goNext();
        return true;
      case "t":
      case "T":
        togglePanel("toc");
        return true;
      case "s":
      case "S":
        togglePanel("settings");
        return true;
      case "f":
      case "F":
        togglePanel("search");
        return true;
      case "b":
      case "B":
        void toggleBookmark();
        return true;
    }
    return false;
  }

  function handleWindowKey(e: KeyboardEvent): void {
    const tag = (document.activeElement as HTMLElement | null)?.tagName ?? "";
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    // Cancel the default for keys we handle so a letter shortcut (f/s/t/b)
    // isn't also typed into a panel input that opens and grabs focus during
    // this same keystroke.
    if (handleKeyAction(e)) e.preventDefault();
  }

  function handleFrameKey(e: KeyEvent): void {
    handleKeyAction(e);
  }

  // Tap/click inside the reader iframe: in paged mode, edges turn the page and
  // the centre toggles the chrome; in scroll mode any tap toggles the chrome.
  // An open panel always closes first.
  function handleClickRegion(region: "left" | "center" | "right"): void {
    if (activePanel !== "none") {
      setPanel("none");
      resetChromeTimer();
      return;
    }
    if (isPaged) {
      // Mirror reading order in RTL so tap edges agree with the keyboard
      // (ArrowLeft/ArrowRight) and the swipe handler: visual-left turns one
      // step *back* in reading order for LTR and *forward* for RTL.
      if (region === "left") isRTL ? api?.nextPage() : api?.prevPage();
      else if (region === "right") isRTL ? api?.prevPage() : api?.nextPage();
      else toggleChrome();
      return;
    }
    toggleChrome();
  }
</script>

<svelte:window
  onkeydown={handleWindowKey}
  onpointermove={handlePointerActivity}
/>
<svelte:document onvisibilitychange={handleVisibility} />

<div class="reader" class:chrome-hidden={!chromeVisible}>
  <header
    class="bar"
    class:hidden={!chromeVisible}
    inert={panelOpen || !chromeVisible}
    aria-hidden={!chromeVisible}
  >
    <button class="icon" onclick={handleBack} aria-label="Back to library"
      ><Icon icon={ArrowLeft} /></button
    >
    <div class="title">
      <span class="book">{book?.title ?? "…"}</span>
      {#if book}
        <span class="chapter">
          {chapterLabel || `Chapter ${currentChapter + 1}`} ·
          <span class="tnum">{currentChapter + 1}/{book.chapterCount}</span>
        </span>
      {/if}
    </div>
    <div class="tools">
      <button
        class="icon"
        class:active={currentBookmarkId !== null}
        onclick={() => void toggleBookmark()}
        aria-label={currentBookmarkId ? "Remove bookmark" : "Add bookmark"}
        aria-pressed={currentBookmarkId !== null}
        ><Icon
          icon={currentBookmarkId ? BookmarkCheck : BookmarkIcon}
        /></button
      >
      <button
        class="icon"
        onclick={() => togglePanel("bookmarks")}
        aria-label="Bookmarks"
        aria-pressed={activePanel === "bookmarks"}
        ><Icon icon={BookMarked} /></button
      >
      <button
        class="icon"
        onclick={() => togglePanel("search")}
        aria-label="Search in book"
        aria-pressed={activePanel === "search"}><Icon icon={Search} /></button
      >
      <button
        class="icon"
        onclick={() => togglePanel("settings")}
        aria-label="Settings"
        aria-pressed={activePanel === "settings"}
        ><Icon icon={Settings} /></button
      >
      <button
        class="icon"
        onclick={() => togglePanel("toc")}
        aria-label="Table of contents"
        aria-pressed={activePanel === "toc"}><Icon icon={List} /></button
      >
      <button
        class="icon"
        onclick={() => ui.openShortcuts()}
        aria-label="Keyboard shortcuts"><Icon icon={CircleHelp} /></button
      >
    </div>
  </header>

  <div class="stage">
    <div class="stage-content" inert={panelOpen}>
      {#if book}
        <ChapterFrame
          initialTheme={settings.value.theme}
          onapi={handleApi}
          onready={handleReady}
          onloaded={handleLoaded}
          onposition={handlePosition}
          onboundary={handleBoundary}
          onlinkclicked={handleLinkClicked}
          onkey={handleFrameKey}
          onclickregion={handleClickRegion}
          onframeerror={handleFrameError}
        />
      {/if}

      {#if chapterLoading}
        <div class="loading" role="status" aria-live="polite">
          <span class="sr-only">Loading chapter…</span>
        </div>
      {/if}

      {#if error}
        <div class="error" role="alert">
          <p>{error}</p>
          <button onclick={() => loadChapter(currentChapter)}>Retry</button>
        </div>
      {/if}
    </div>

    {#if activePanel === "toc" && book}
      <div
        class="panel left"
        role="dialog"
        aria-modal="true"
        aria-label="Table of contents"
        {@attach focusTrap}
      >
        {#if TocPanelComp}
          <TocPanelComp
            toc={book.toc}
            activeEntry={activeTocEntry}
            onnavigate={handleTocNavigate}
            onclose={closePanel}
          />
        {:else}
          {#await tocPanel() then { default: TocPanel }}
            <TocPanel
              toc={book.toc}
              activeEntry={activeTocEntry}
              onnavigate={handleTocNavigate}
              onclose={closePanel}
            />
          {:catch}
            <div class="error" role="alert">
              <p>Couldn't load this panel.</p>
              <button onclick={preloadTocPanel}>Retry</button>
            </div>
          {/await}
        {/if}
      </div>
      <button
        class="scrim"
        aria-label="Close panel backdrop"
        tabindex="-1"
        onclick={closePanel}
      ></button>
    {/if}

    {#if activePanel === "bookmarks"}
      <div
        class="panel left"
        role="dialog"
        aria-modal="true"
        aria-label="Bookmarks"
        {@attach focusTrap}
      >
        {#if BookmarksPanelComp}
          <BookmarksPanelComp
            {bookmarks}
            onnavigate={navigateBookmark}
            ondelete={(id) => void removeBookmark(id)}
            onupdate={(id, label, comment) =>
              void editBookmark(id, label, comment)}
            onclose={closePanel}
          />
        {:else}
          {#await bookmarksPanel() then { default: BookmarksPanel }}
            <BookmarksPanel
              {bookmarks}
              onnavigate={navigateBookmark}
              ondelete={(id) => void removeBookmark(id)}
              onupdate={(id, label, comment) =>
                void editBookmark(id, label, comment)}
              onclose={closePanel}
            />
          {:catch}
            <div class="error" role="alert">
              <p>Couldn't load this panel.</p>
              <button onclick={preloadBookmarksPanel}>Retry</button>
            </div>
          {/await}
        {/if}
      </div>
      <button
        class="scrim"
        aria-label="Close panel backdrop"
        tabindex="-1"
        onclick={closePanel}
      ></button>
    {/if}

    {#if activePanel === "search"}
      <div
        class="panel right"
        role="dialog"
        aria-modal="true"
        aria-label="Search in book"
        {@attach focusTrap}
      >
        {#if SearchPanelComp}
          <SearchPanelComp
            {bookId}
            chapterCount={book?.chapterCount ?? 0}
            onresultclick={navigateToResult}
            onclose={closePanel}
          />
        {:else}
          {#await searchPanel() then { default: SearchPanel }}
            <SearchPanel
              {bookId}
              chapterCount={book?.chapterCount ?? 0}
              onresultclick={navigateToResult}
              onclose={closePanel}
            />
          {:catch}
            <div class="error" role="alert">
              <p>Couldn't load this panel.</p>
              <button onclick={preloadSearchPanel}>Retry</button>
            </div>
          {/await}
        {/if}
      </div>
      <button
        class="scrim"
        aria-label="Close panel backdrop"
        tabindex="-1"
        onclick={closePanel}
      ></button>
    {/if}

    {#if activePanel === "settings"}
      <div
        class="panel right"
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        {@attach focusTrap}
      >
        {#if SettingsPanelComp}
          <SettingsPanelComp onclose={closePanel} />
        {:else}
          {#await settingsPanel() then { default: SettingsPanel }}
            <SettingsPanel onclose={closePanel} />
          {:catch}
            <div class="error" role="alert">
              <p>Couldn't load this panel.</p>
              <button onclick={preloadSettingsPanel}>Retry</button>
            </div>
          {/await}
        {/if}
      </div>
      <button
        class="scrim"
        aria-label="Close panel backdrop"
        tabindex="-1"
        onclick={closePanel}
      ></button>
    {/if}
  </div>

  <!-- Scroll mode has no page-indicator pill, so the progress bar is the only
       positional cue: keep it visible even when the chrome auto-hides. Paged
       mode shows the indicator pill, so the bar can hide with the chrome. -->
  <div
    class="progress"
    class:hidden={!chromeVisible && isPaged}
    aria-hidden="true"
  >
    <div class="fill" style:--progress-scale={chapterPercent}></div>
  </div>
</div>

<style>
  .reader {
    position: relative;
    height: 100vh;
    overflow: hidden;
  }
  /* The chrome (bar + progress) overlays a full-bleed stage so toggling its
     visibility never resizes the iframe (which would force re-pagination). */
  .bar {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: 0.55rem 0.9rem;
    background: color-mix(in srgb, var(--bg) 92%, transparent);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--hairline);
    transition:
      transform var(--dur) var(--ease-out),
      opacity var(--dur) var(--ease-out);
  }
  .bar.hidden {
    transform: translateY(-100%);
    opacity: 0;
    pointer-events: none;
    transition:
      transform var(--dur) var(--ease-in),
      opacity var(--dur) var(--ease-in);
  }
  .chrome-hidden {
    cursor: none;
  }
  .tools {
    display: flex;
    align-items: center;
    gap: var(--sp-1);
  }
  .icon {
    border: none;
    background: transparent;
    color: var(--fg);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
    padding: 0.45rem;
    /* 44×44 minimum hit area (fixing-accessibility / web-quality-audit tap
       targets); the icon glyph stays centered via flex. */
    min-width: 2.75rem;
    min-height: 2.75rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur-fast) var(--ease-out),
      color var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .icon:hover {
    background: var(--surface-hover);
  }
  .icon:active {
    transform: scale(0.94);
  }
  .icon.active {
    color: var(--accent);
  }
  .title {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .book {
    font-family: var(--font-display);
    font-weight: 600;
    font-size: 1.1rem;
    line-height: var(--lh-tight);
    letter-spacing: 0.01em;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chapter {
    font-size: var(--text-xs);
    line-height: var(--lh-snug);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .stage {
    position: absolute;
    inset: 0;
  }
  /* display:contents adds no box (zero layout / Lighthouse impact) but still
     propagates `inert` to the iframe + overlays behind an open panel. */
  .stage-content {
    display: contents;
  }
  .loading {
    position: absolute;
    inset: 0;
    background: var(--bg);
    opacity: 0.6;
    pointer-events: none;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  .error {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--sp-3);
    background: var(--bg);
    text-align: center;
    padding: var(--sp-4);
  }
  .error button {
    padding: var(--sp-2) var(--sp-4);
    border: 1px solid var(--hairline);
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
  }
  .panel {
    position: absolute;
    top: 0;
    bottom: 0;
    width: min(20rem, 85vw);
    background: var(--bg);
    z-index: 7;
  }
  .panel.left {
    left: 0;
    border-right: 1px solid var(--hairline-strong);
    animation: panel-left-in var(--dur) var(--ease-out) backwards;
  }
  .panel.right {
    right: 0;
    width: min(22rem, 90vw);
    border-left: 1px solid var(--hairline-strong);
    animation: panel-right-in var(--dur) var(--ease-out) backwards;
  }
  .scrim {
    position: absolute;
    inset: 0;
    border: none;
    background: color-mix(in srgb, #000 25%, transparent);
    cursor: pointer;
    z-index: 6;
    animation: app-overlay-in var(--dur-fast) var(--ease-out) backwards;
  }
  @keyframes panel-left-in {
    from {
      opacity: 0;
      transform: translateX(-0.75rem);
    }
    to {
      opacity: 1;
      transform: translateX(0);
    }
  }
  @keyframes panel-right-in {
    from {
      opacity: 0;
      transform: translateX(0.75rem);
    }
    to {
      opacity: 1;
      transform: translateX(0);
    }
  }
  .progress {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    z-index: 5;
    height: 3px;
    background: color-mix(in srgb, var(--fg) 12%, transparent);
    transition: opacity var(--dur) var(--ease-out);
  }
  .progress.hidden {
    opacity: 0;
  }
  .fill {
    width: 100%;
    height: 100%;
    background: var(--accent);
    transform: scaleX(var(--progress-scale, 0));
    transform-origin: left center;
    transition: transform var(--dur-fast) var(--ease-out);
  }
</style>
