<script lang="ts">
  import { onMount } from "svelte";
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
  import { settings } from "~/lib/settings.svelte";
  import { toast } from "~/lib/toast.svelte";
  import { fontRegistry } from "~/lib/fontRegistry.svelte";
  import { buildAllFontFaces } from "~/lib/readerFontFaces";
  import { router } from "~/lib/router.svelte";
  import { ui } from "~/lib/ui.svelte";
  import { resolveHref, findTocLabel } from "~/lib/href";
  import { getErrorMessage } from "~/lib/errors";
  import { applyTheme } from "~/lib/theme";
  import ChapterFrame from "~/components/reader/ChapterFrame.svelte";
  import TocPanel from "~/components/reader/TocPanel.svelte";
  import SettingsPanel from "~/components/reader/SettingsPanel.svelte";
  import SearchPanel from "~/components/reader/SearchPanel.svelte";
  import BookmarksPanel from "~/components/reader/BookmarksPanel.svelte";
  import type { ChapterFrameAPI, KeyEvent } from "~/components/reader/frame-types";
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
  const MAX_CHAPTER_CACHE = 6;
  // bookId is read once at mount; App.svelte wraps Read in {#key params.id} so a
  // different book remounts this component rather than mutating bookId in place.
  // svelte-ignore state_referenced_locally
  const progressCacheKey = `sayumi:progress:${bookId}`;

  // Reactive (rendered) state.
  let book = $state<BookDetail | null>(null);
  let currentChapter = $state(0);
  let chapterPercent = $state(0);
  let chapterLoading = $state(false);
  let error = $state("");
  type Panel = "none" | "toc" | "settings" | "search" | "bookmarks";
  let activePanel = $state<Panel>("none");
  let bookmarks = $state<Bookmark[]>([]);
  let chromeVisible = $state(true);

  const isPaged = $derived(settings.value.displayMode !== "scroll");
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
    bookmarks.find(
      (b) => b.chapter === currentChapter && Math.abs(b.percent - chapterPercent) < 0.02,
    )?.id ?? null,
  );

  // Non-reactive instance state.
  let api: ChapterFrameAPI | null = null;
  let frameReady = false;
  let bookLoaded = false;
  let initialLoadDone = false;
  let saveData: ProgressData = { chapter: 0, percent: 0 };
  const chapterCache = new Map<number, ChapterData>();
  let chapterLoadInProgress = false;
  let pendingNav: { index: number; scrollTo: "top" | "end"; fragment?: string; restore?: { percent: number; cfi?: string } } | null = null;
  let pendingHighlight: { charOffset: number; matchLen: number; query: string } | null = null;
  let highlightTimer: ReturnType<typeof setTimeout> | undefined;
  let chromeHideTimer: ReturnType<typeof setTimeout> | undefined;
  let fetchAbort: AbortController | null = null;
  let progressTimer: ReturnType<typeof setTimeout> | undefined;
  let lastFlushTime = 0;
  let lastPersistedChapter = -1;
  let lastPersistedPercent = -1;
  let lastBoundaryTime = 0;

  const CHROME_AUTO_HIDE_MS = 4000;

  function togglePanel(p: Panel): void {
    activePanel = activePanel === p ? "none" : p;
    // A panel is part of the chrome — keep it visible while open.
    if (activePanel !== "none") showChrome(false);
    else resetChromeTimer();
  }
  function closePanel(): void {
    if (activePanel === "none") return;
    activePanel = "none";
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
      chromeHideTimer = setTimeout(() => (chromeVisible = false), CHROME_AUTO_HIDE_MS);
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
  function handlePointerActivity(): void {
    if (!chromeVisible) chromeVisible = true;
    resetChromeTimer();
  }

  // Pushes the current @font-face CSS into the iframe. The frame only stores it;
  // a following applySettings() (or chapter load) re-injects it into the DOM.
  function pushFontFaces(): void {
    api?.setFontFaces(fontFaceCSS);
  }

  // Push settings into the iframe whenever they change (after the first load).
  // Font faces are pushed first so a role/registry change takes effect in the
  // same applySettings pass.
  $effect(() => {
    const s = settings.iframe;
    const faces = fontFaceCSS;
    if (api && initialLoadDone) {
      api.setFontFaces(faces);
      api.applySettings(s);
    }
  });

  // Keep the app chrome (reader bar, panels) in sync with the reading theme.
  $effect(() => {
    applyTheme(settings.value.theme);
  });

  // Drop any in-book search highlight as soon as the search panel is dismissed.
  // Closing search shouldn't leave the matched text highlighted until the
  // reader is exited.
  let searchPanelWasOpen = false;
  $effect(() => {
    const searchOpen = activePanel === "search";
    if (searchPanelWasOpen && !searchOpen) api?.clearHighlights();
    searchPanelWasOpen = searchOpen;
  });

  onMount(() => {
    void boot();
    resetChromeTimer();
    return () => {
      cancelProgressSave();
      fetchAbort?.abort();
      if (highlightTimer) clearTimeout(highlightTimer);
      if (chromeHideTimer) clearTimeout(chromeHideTimer);
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
        const serverAhead =
          cached.chapter < saved.chapter ||
          (cached.chapter === saved.chapter && cached.percent <= saved.percent);
        if (!serverAhead || !serverLoaded) saved = cached;
      }
    } catch {
      // ignore malformed cache
    }

    try {
      const data = await getBook(bookId);
      book = data;
      bookLoaded = true;
      const chapter = Math.max(0, Math.min(saved.chapter, data.chapterCount - 1));
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
    if (percent > 0.001) void loadChapter(chapter, "top", undefined, { percent, cfi });
    else void loadChapter(chapter, "top");
  }

  async function fetchChapterWithRetry(index: number, signal?: AbortSignal): Promise<ChapterData> {
    const cached = chapterCache.get(index);
    if (cached) {
      chapterCache.delete(index);
      chapterCache.set(index, cached); // refresh LRU position
      return cached;
    }
    const data = await fetchChapter(bookId, index, CHAPTER_LOAD_RETRY_ATTEMPTS, signal);
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
        api.loadChapter(data, settings.iframe, "top", undefined, hasPrev, hasNext, restore.percent, restore.cfi);
      } else {
        api.loadChapter(data, settings.iframe, scrollTo, fragment, hasPrev, hasNext);
      }

      currentChapter = index;
      chapterPercent = nextPercent;
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
    if (!force && now - lastFlushTime < PROGRESS_FLUSH_THROTTLE_MS) return Promise.resolve();

    const { chapter, percent } = saveData;
    if (!force && chapter === lastPersistedChapter && Math.abs(percent - lastPersistedPercent) < 0.001) {
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
  function handleLoaded(): void {
    // Apply a pending search highlight once the new chapter has settled.
    if (!pendingHighlight) return;
    const h = pendingHighlight;
    pendingHighlight = null;
    if (highlightTimer) clearTimeout(highlightTimer);
    highlightTimer = setTimeout(() => {
      highlightTimer = undefined;
      api?.highlightSearch(h.charOffset, h.matchLen, h.query);
    }, 120);
  }
  function handlePosition(chapterIndex: number, percent: number, cfi?: string): void {
    chapterPercent = percent;
    saveData = { chapter: chapterIndex, percent, cfi };
  }
  function handleBoundary(boundary: "start" | "end"): void {
    const now = Date.now();
    if (now - lastBoundaryTime < BOUNDARY_COOLDOWN_MS || chapterLoadInProgress) return;
    lastBoundaryTime = now;
    const b = book;
    if (!b) return;
    if (boundary === "end" && currentChapter + 1 < b.chapterCount) void loadChapter(currentChapter + 1, "top");
    else if (boundary === "start" && currentChapter > 0) void loadChapter(currentChapter - 1, "end");
  }
  function handleLinkClicked(href: string): void {
    const b = book;
    if (!b) return;
    const resolved = resolveHref(href, b.spine);
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter) {
      if (resolved.fragment) api?.scrollToFragment(resolved.fragment);
    } else {
      void loadChapter(resolved.chapterIndex, "top", resolved.fragment || undefined);
    }
  }
  function handleTocNavigate(href: string): void {
    activePanel = "none";
    const b = book;
    if (!b) return;
    const resolved = resolveHref(href, b.spine);
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter) {
      if (resolved.fragment) api?.scrollToFragment(resolved.fragment);
      else api?.scrollTo(0);
    } else {
      void loadChapter(resolved.chapterIndex, "top", resolved.fragment || undefined);
    }
  }

  // ---- navigation + keyboard ----------------------------------------------
  function goPrev(): void {
    if (isPaged) api?.prevPage();
    else if (book && currentChapter > 0) void loadChapter(currentChapter - 1, "top");
  }
  function goNext(): void {
    if (isPaged) api?.nextPage();
    else if (book && currentChapter + 1 < book.chapterCount) void loadChapter(currentChapter + 1, "top");
  }
  function handleBack(): void {
    void flushProgress(true);
    router.navigate("/");
  }

  // ---- search -------------------------------------------------------------
  function navigateToResult(result: SearchResult, query: string): void {
    api?.clearHighlights();
    if (result.chapterIndex === currentChapter) {
      api?.highlightSearch(result.charOffset, result.matchLen, query);
    } else {
      pendingHighlight = { charOffset: result.charOffset, matchLen: result.matchLen, query };
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
    activePanel = "none";
    if (bm.chapter === currentChapter) {
      if (bm.cfi) api?.scrollToCfi(bm.cfi);
      else api?.scrollTo(bm.percent);
    } else {
      void loadChapter(bm.chapter, "top", undefined, { percent: bm.percent, cfi: bm.cfi });
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

  async function editBookmark(id: string, label: string, comment: string): Promise<void> {
    const prev = bookmarks;
    bookmarks = bookmarks.map((b) => (b.id === id ? { ...b, label, comment } : b));
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
  function handleKeyAction(e: { key: string; ctrlKey?: boolean; metaKey?: boolean }): boolean {
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
          activePanel = "none";
          resetChromeTimer();
        } else handleBack();
        return true;
      case "ArrowLeft":
        goPrev();
        return true;
      case "ArrowRight":
        goNext();
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
      activePanel = "none";
      resetChromeTimer();
      return;
    }
    if (isPaged) {
      if (region === "left") api?.prevPage();
      else if (region === "right") api?.nextPage();
      else toggleChrome();
      return;
    }
    toggleChrome();
  }
</script>

<svelte:window onkeydown={handleWindowKey} onpointermove={handlePointerActivity} />
<svelte:document onvisibilitychange={handleVisibility} />

<div class="reader" class:chrome-hidden={!chromeVisible}>
  <header class="bar" class:hidden={!chromeVisible}>
    <button class="icon" onclick={handleBack} aria-label="Back to library"><Icon icon={ArrowLeft} /></button>
    <div class="title">
      <span class="book">{book?.title ?? "…"}</span>
      {#if book}
        <span class="chapter">
          {chapterLabel || `Chapter ${currentChapter + 1}`} · <span class="tnum">{currentChapter + 1}/{book.chapterCount}</span>
        </span>
      {/if}
    </div>
    <button
      class="icon"
      class:active={currentBookmarkId !== null}
      onclick={() => void toggleBookmark()}
      aria-label={currentBookmarkId ? "Remove bookmark" : "Add bookmark"}
      aria-pressed={currentBookmarkId !== null}
    ><Icon icon={currentBookmarkId ? BookmarkCheck : BookmarkIcon} /></button>
    <button
      class="icon"
      onclick={() => togglePanel("bookmarks")}
      aria-label="Bookmarks"
      aria-pressed={activePanel === "bookmarks"}
    ><Icon icon={BookMarked} /></button>
    <button
      class="icon"
      onclick={() => togglePanel("search")}
      aria-label="Search in book"
      aria-pressed={activePanel === "search"}
    ><Icon icon={Search} /></button>
    <button
      class="icon"
      onclick={() => togglePanel("settings")}
      aria-label="Settings"
      aria-pressed={activePanel === "settings"}
    ><Icon icon={Settings} /></button>
    <button
      class="icon"
      onclick={() => togglePanel("toc")}
      aria-label="Table of contents"
      aria-pressed={activePanel === "toc"}
    ><Icon icon={List} /></button>
    <button
      class="icon"
      onclick={() => ui.openShortcuts()}
      aria-label="Keyboard shortcuts"
    ><Icon icon={CircleHelp} /></button>
  </header>

  <div class="stage">
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
      />
    {/if}

    {#if chapterLoading}
      <div class="loading" aria-hidden="true"></div>
    {/if}

    {#if error}
      <div class="error" role="alert">
        <p>{error}</p>
        <button onclick={() => loadChapter(currentChapter)}>Retry</button>
      </div>
    {/if}

    {#if activePanel === "toc" && book}
      <div class="panel left" role="dialog" aria-modal="true" aria-label="Table of contents" {@attach focusTrap}>
        <TocPanel toc={book.toc} onnavigate={handleTocNavigate} />
      </div>
      <button class="scrim" aria-label="Close" onclick={closePanel}></button>
    {/if}

    {#if activePanel === "bookmarks"}
      <div class="panel left" role="dialog" aria-modal="true" aria-label="Bookmarks" {@attach focusTrap}>
        <BookmarksPanel
          {bookmarks}
          onnavigate={navigateBookmark}
          ondelete={(id) => void removeBookmark(id)}
          onupdate={(id, label, comment) => void editBookmark(id, label, comment)}
          onclose={closePanel}
        />
      </div>
      <button class="scrim" aria-label="Close" onclick={closePanel}></button>
    {/if}

    {#if activePanel === "search"}
      <div class="panel right" role="dialog" aria-modal="true" aria-label="Search in book" {@attach focusTrap}>
        <SearchPanel
          {bookId}
          onresultclick={navigateToResult}
          onclose={closePanel}
        />
      </div>
      <button class="scrim" aria-label="Close" onclick={closePanel}></button>
    {/if}

    {#if activePanel === "settings"}
      <div class="panel right" role="dialog" aria-modal="true" aria-label="Settings" {@attach focusTrap}>
        <SettingsPanel onclose={closePanel} />
      </div>
      <button class="scrim" aria-label="Close" onclick={closePanel}></button>
    {/if}

  </div>

  <div class="progress" class:hidden={!chromeVisible} aria-hidden="true">
    <div class="fill" style:width={`${Math.round(chapterPercent * 100)}%`}></div>
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
    gap: 0.75rem;
    padding: 0.55rem 0.9rem;
    background: color-mix(in srgb, var(--bg) 92%, transparent);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 10%, transparent);
    transition: transform var(--dur) var(--ease-out), opacity var(--dur) var(--ease-out);
  }
  .bar.hidden {
    transform: translateY(-100%);
    opacity: 0;
    pointer-events: none;
    transition: transform var(--dur) var(--ease-in), opacity var(--dur) var(--ease-in);
  }
  .chrome-hidden {
    cursor: none;
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
    border-radius: 0.4rem;
    cursor: pointer;
    transition: background var(--dur-fast) var(--ease-out),
      color var(--dur-fast) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .icon:hover {
    background: color-mix(in srgb, var(--fg) 8%, transparent);
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
    font-size: 0.72rem;
    line-height: var(--lh-snug);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--muted, #6b6661);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .stage {
    position: absolute;
    inset: 0;
  }
  .loading {
    position: absolute;
    inset: 0;
    background: var(--bg);
    opacity: 0.6;
    pointer-events: none;
  }
  .error {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    background: var(--bg);
    text-align: center;
    padding: 1rem;
  }
  .error button {
    padding: 0.5rem 1rem;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.5rem;
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
  }
  .panel.right {
    right: 0;
    width: min(22rem, 90vw);
    border-left: 1px solid var(--hairline-strong);
  }
  .scrim {
    position: absolute;
    inset: 0;
    border: none;
    background: color-mix(in srgb, #000 25%, transparent);
    cursor: pointer;
    z-index: 6;
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
    height: 100%;
    background: var(--accent);
    transition: width var(--dur-fast) var(--ease-out);
  }
</style>
