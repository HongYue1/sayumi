// Read: the reader route — ChapterFrame wiring, side panels, progress
// tracking, keyboard nav — Solid 2.0 port.
//
// Solid 2.0 notes:
//   - Rendered state is signals; the large non-reactive instance state (api,
//     saveData, chapterCache, timers, pendingNav/pendingHighlight, …) stays
//     plain lets, as in ChapterFrame.
//   - The four side panels are clientOnly(loader, { lazy: true }) so the
//     import defers to first open; an idle-time prewarm (direct import(),
//     module-cache-deduped with clientOnly's own) keeps first open at a
//     one-microtask fallback, matching the Svelte prewarm. Specimen mode
//     still prewarms Settings only.
//   - svelte:window/document listeners -> window/document addEventListener in
//     onSettled + onCleanup; the moreOpen-conditional pointerdown listener
//     becomes a compute/apply createEffect keyed on moreOpen().
//   - The three $effects become compute/apply createEffects; the apply phase
//     never tracks, which is exactly the guarantee the Svelte version used
//     untrack() for.
//   - fly/fade transitions -> CSS keyframe enters (rdp-panel-in-left/right,
//     rdp-fade-in) with a prefers-reduced-motion kill switch in CSS — the
//     Svelte JS honored it via PANEL_MS=0; CSS owns it now. Exit animations
//     are dropped: Solid 2.0 has no transition directives and docs29 has no
//     Transition component.
//   - No per-panel "couldn't load / Retry" UI: clientOnly exposes no error
//     state, and with the idle prewarm a chunk failure means the whole reader
//     chunk graph is broken anyway.
import {
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  onSettled,
  Show,
} from "solid-js";
import { clientOnly } from "@solidjs/web";
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
import { settings, type IframeSettings } from "~/lib/settings";
import { library } from "~/lib/library";
import { session } from "~/lib/session";
import { toast } from "~/lib/toast";
import { fontRegistry } from "~/lib/fontRegistry";
import { buildAllFontFaces } from "~/lib/readerFontFaces";
import { router } from "~/lib/router";
import {
  SPECIMEN_BOOK_ID,
  specimenBookDetail,
  specimenChapter,
} from "~/lib/specimen";
import { ui } from "~/lib/ui";
import { resolveHref, buildTocChapterEntries } from "~/lib/href";
import { getErrorMessage } from "~/lib/errors";
import { isReachable } from "~/lib/reachability";
import { applyTheme } from "~/lib/theme";
import ChapterFrame from "~/components/reader/ChapterFrame";
import type {
  ChapterFrameAPI,
  KeyEvent,
} from "~/components/reader/frame-types";
import Icon from "~/lib/Icon";
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
  Ellipsis,
} from "~/lib/icons";

// Reader side-panels split into their own chunks so they don't inflate the
// reader route's initial JS. lazy: true defers each import to first open;
// the idle prewarm below warms the module cache so the fallback (nothing, for
// one microtask) is all a cold open ever shows.
const TocPanel = clientOnly(() => import("~/components/reader/TocPanel"), {
  lazy: true,
});
const SettingsPanel = clientOnly(
  () => import("~/components/reader/SettingsPanel"),
  { lazy: true },
);
const SearchPanel = clientOnly(
  () => import("~/components/reader/SearchPanel"),
  { lazy: true },
);
const BookmarksPanel = clientOnly(
  () => import("~/components/reader/BookmarksPanel"),
  { lazy: true },
);

interface Props {
  bookId: string;
}

const PROGRESS_FLUSH_THROTTLE_MS = 5_000;
const PROGRESS_SAVE_INTERVAL_MS = 15_000;
const BOUNDARY_COOLDOWN_MS = 400;
// Extra boundary quiet period right after a chapter swap lands. The 400ms
// cooldown starts when the boundary FIRES, but the new chapter renders later
// (fetch + fonts-gated reveal), so leftover wheel momentum at a boundary
// could otherwise chain-skip a second chapter the reader never saw.
const POST_SWAP_BOUNDARY_GRACE_MS = 650;
const CHAPTER_LOAD_RETRY_ATTEMPTS = 3;
// Each cached chapter retains full decoded HTML + CSS, so this is the largest
// retained-memory knob in the reader. Active navigation only needs the
// current chapter ± 1 (3 chapters); 4 keeps one extra recently-read chapter
// for instant back-nav without holding several full chapters' worth of
// detached HTML/CSS strings in memory.
const MAX_CHAPTER_CACHE = 4;
const CHROME_AUTO_HIDE_MS = 4000;

type Panel = "none" | "toc" | "settings" | "search" | "bookmarks";

export default function Read(props: Props) {
  // bookId is read once at mount; App wraps Read in <Show keyed> on params.id
  // so a different book remounts this component rather than mutating bookId in
  // place. Scoped to the profile: two profiles reading the same book id must
  // not resume from (or clobber) each other's locally cached position.
  const bookId = props.bookId;
  const progressCacheKey = `sayumi:progress:${session.profile ?? ""}:${bookId}`;
  // The reader doubles as a live typography preview: a sentinel bookId renders
  // a built-in specimen chapter entirely client-side (no book/progress/bookmark
  // server calls), so users can tune reading settings against rich sample text.
  const isSpecimen = bookId === SPECIMEN_BOOK_ID;
  // Bind reader-to-library updates to the profile generation that opened this
  // route. A delayed save from an old reader must never touch a later profile.
  const publishLibraryProgress = library.createReadingProgressPublisher(
    session.profile,
    bookId,
  );

  // Reactive (rendered) state.
  const [book, setBook] = createSignal<BookDetail | null>(null);
  const [currentChapter, setCurrentChapter] = createSignal(0);
  const [chapterPercent, setChapterPercent] = createSignal(0);
  const [chapterDirection, setChapterDirection] = createSignal("ltr");
  const [chapterLoading, setChapterLoading] = createSignal(false);
  const [error, setError] = createSignal("");
  // True when the book itself (not just a chapter) failed to load, so Retry
  // re-fetches the book and the empty frame shows a book-level message.
  const [bookLoadFailed, setBookLoadFailed] = createSignal(false);
  const [activePanel, setActivePanel] = createSignal<Panel>("none");
  const [bookmarks, setBookmarks] = createSignal<Bookmark[]>([]);
  const [chromeVisible, setChromeVisible] = createSignal(true);
  const [moreOpen, setMoreOpen] = createSignal(false);

  const isPaged = createMemo(() => settings.value.displayMode !== "scroll");
  const isRTL = createMemo(() => chapterDirection() === "rtl");
  // When a modal panel (toc/settings/search/bookmarks) is open, the reader
  // chrome + iframe behind it must leave the tab + AT order. focusTrap only
  // traps Tab; `inert` also pulls the background out of the accessibility tree.
  const panelOpen = createMemo(() => activePanel() !== "none");
  // Combined reader @font-face CSS (embedded + user families), recomputed when
  // the user font registry or the per-family role mapping change.
  const fontFaceCSS = createMemo(() =>
    buildAllFontFaces(fontRegistry.families, settings.value.fontRoles),
  );
  // Bookmarks grouped by chapter, rebuilt only when the bookmark list itself
  // changes (add/remove/edit) rather than on every reading-position tick. This
  // lets currentBookmarkId scan just the current chapter's bookmarks instead of
  // the whole list on each chapterPercent change (~5/s while scrolling), so the
  // per-tick cost is independent of the total bookmark count.
  const bookmarksByChapter = createMemo(() => {
    const byChapter = new Map<number, Bookmark[]>();
    for (const b of bookmarks()) {
      const list = byChapter.get(b.chapter);
      if (list) list.push(b);
      else byChapter.set(b.chapter, [b]);
    }
    return byChapter;
  });
  // A bookmark at (or very near) the current reading position, if any.
  const currentBookmarkId = createMemo(
    () =>
      (bookmarksByChapter().get(currentChapter()) ?? []).find((b) =>
        isBookmarkAtPosition(b, currentChapter(), chapterPercent()),
      )?.id ?? null,
  );
  // Active TOC entry to highlight per chapter. Built once per book
  // (O(toc + spine)) rather than re-resolving every TOC entry each time the
  // panel opens — that per-open walk made the TOC slow to open in books with
  // many chapters. We track the entry object itself (not its href) so exactly
  // one node is highlighted even when two entries share an href (e.g. a
  // top-level "book title" entry pointing at the same file as a chapter).
  const tocChapterEntries = createMemo(() => {
    const b = book();
    return b ? buildTocChapterEntries(b.toc, b.spine) : null;
  });
  // O(1) lookup of the active entry for the current chapter. A chapter with no
  // TOC line of its own inherits the nearest preceding heading (filled forward
  // in buildTocChapterEntries).
  const activeTocEntry = createMemo(() => {
    const entries = tocChapterEntries();
    return entries ? (entries[currentChapter()] ?? null) : null;
  });
  // Chrome chapter title: reuse the same resolved TOC entry that drives the
  // sidebar highlight so the title and the highlighted line can't disagree, and
  // so we don't run a second full TOC walk per chapter change. activeTocEntry
  // already inherits the nearest preceding heading for chapters with no TOC
  // line of their own (fill-forward in buildTocChapterEntries).
  const chapterLabel = createMemo(() => activeTocEntry()?.title ?? "");
  // First spine index each TOC entry covers (tocChapterEntries fills forward,
  // so the first occurrence is the entry's own start). Drives the TOC's
  // read-rail: an entry whose start lies before the current chapter is read.
  const entryStartChapter = createMemo(() => {
    const entries = tocChapterEntries();
    if (!entries) return null;
    const starts = new Map<NonNullable<(typeof entries)[number]>, number>();
    entries.forEach((entry, i) => {
      if (entry && !starts.has(entry)) starts.set(entry, i);
    });
    return starts;
  });
  // Resolves a chapter index to its TOC heading, so bookmarks can show real
  // chapter names instead of "Chapter N". Plain function prop; the map is
  // built once per book.
  function chapterTitleFor(chapter: number): string | null {
    return tocChapterEntries()?.[chapter]?.title ?? null;
  }

  // ---- non-reactive instance state ----------------------------------------
  let api: ChapterFrameAPI | null = null;
  let frameReady = false;
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
  let lastChapterSwapAt = 0;
  let lastBootProgress: ProgressData = { chapter: 0, percent: 0 };

  function cancelPendingHighlight(): void {
    pendingHighlight = null;
    if (highlightTimer) {
      clearTimeout(highlightTimer);
      highlightTimer = undefined;
    }
  }

  // Single choke point for panel changes. Leaving the search panel drops its
  // in-book highlight as an explicit side effect of the transition, instead of
  // an effect that watches activePanel on every change.
  function setPanel(p: Panel): void {
    if (p === activePanel()) return;
    if (activePanel() === "search" && p !== "search") {
      cancelPendingHighlight();
      api?.clearHighlights();
    }
    setActivePanel(p);
  }
  function togglePanel(p: Panel): void {
    setPanel(activePanel() === p ? "none" : p);
    // A panel is part of the chrome — keep it visible while open.
    if (activePanel() !== "none") showChrome(false);
    else resetChromeTimer();
  }
  function closePanel(): void {
    if (activePanel() === "none") return;
    setPanel("none");
    resetChromeTimer();
  }
  function showToast(msg: string): void {
    toast.show(msg);
  }

  // ---- toolbar overflow ("⋯") menu — narrow viewports collapse the less-used
  // tools into it so the bar never outgrows a phone screen. -----------------
  let moreBtn: HTMLButtonElement | undefined;
  let moreMenuEl: HTMLElement | undefined;

  function closeMore(restoreFocus = true): void {
    if (!moreOpen()) return;
    setMoreOpen(false);
    if (restoreFocus) moreBtn?.focus();
    // Re-arm the chrome auto-hide that toggleMore paused while the menu was up.
    resetChromeTimer();
  }
  function toggleMore(): void {
    if (moreOpen()) {
      closeMore();
      return;
    }
    setMoreOpen(true);
    // Pin the chrome while the menu is open — the auto-hide timer keeps
    // running otherwise, and the bar vanishing under an open menu strands it.
    showChrome(false);
  }
  function pickMore(action: () => void): void {
    // Restore trigger focus before acting so a panel's focus trap snapshots
    // the trigger (mirrors ProfileMenu.pick()).
    closeMore(true);
    action();
  }
  function onMoreOutside(e: PointerEvent): void {
    const t = e.target as Node;
    if (moreMenuEl?.contains(t) || moreBtn?.contains(t)) return;
    closeMore(false);
  }
  function onMoreKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMore();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End" &&
      e.key !== "Tab"
    ) {
      return;
    }
    // Roving focus, matching the app's other menus.
    const menu = e.currentTarget as HTMLElement;
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>(".rdp-mrow"),
    );
    if (items.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const cur = items.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    switch (e.key) {
      case "Home":
        next = 0;
        break;
      case "End":
        next = items.length - 1;
        break;
      case "Tab":
        next = e.shiftKey
          ? cur < 0
            ? items.length - 1
            : (cur - 1 + items.length) % items.length
          : cur < 0
            ? 0
            : (cur + 1) % items.length;
        break;
      case "ArrowDown":
        next = cur < 0 ? 0 : (cur + 1) % items.length;
        break;
      default:
        next =
          cur < 0 ? items.length - 1 : (cur - 1 + items.length) % items.length;
    }
    items[next].focus();
  }

  // ---- chrome auto-hide ----------------------------------------------------
  function resetChromeTimer(): void {
    if (chromeHideTimer) {
      clearTimeout(chromeHideTimer);
      chromeHideTimer = undefined;
    }
    // Never auto-hide while a panel is open.
    if (activePanel() === "none") {
      chromeHideTimer = setTimeout(
        () => setChromeVisible(false),
        CHROME_AUTO_HIDE_MS,
      );
    }
  }
  function showChrome(arm = true): void {
    setChromeVisible(true);
    if (arm) resetChromeTimer();
    else if (chromeHideTimer) {
      clearTimeout(chromeHideTimer);
      chromeHideTimer = undefined;
    }
  }
  function toggleChrome(): void {
    if (chromeVisible()) {
      setChromeVisible(false);
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
    if (!chromeVisible()) {
      setChromeVisible(true);
      lastChromePoke = Date.now();
      resetChromeTimer();
      return;
    }
    const now = Date.now();
    if (now - lastChromePoke < 500) return;
    lastChromePoke = now;
    resetChromeTimer();
  }

  // Pushes the current @font-face CSS into the iframe. The frame only stores
  // it; a following applySettings() (or chapter load) re-injects it into the
  // DOM.
  function pushFontFaces(): void {
    api?.setFontFaces(fontFaceCSS());
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
  // settings without subscribing to them: the apply phase never tracks, so this
  // effect stays keyed solely on fontFaceCSS (the Svelte version's untrack).
  createEffect(
    () => fontFaceCSS(),
    (faces) => {
      if (api && initialLoadDone) {
        api.setFontFaces(faces);
        scheduleApplySettings(settings.iframe);
      }
      return undefined;
    },
  );

  // Push reader settings whenever they change (after the first load). Split out
  // from the font-face effect so a settings change no longer re-sends the
  // (large) font-face CSS to the iframe.
  createEffect(
    () => settings.iframe,
    (s) => {
      if (api && initialLoadDone) scheduleApplySettings(s);
      return undefined;
    },
  );

  // Keep the app chrome (reader bar, panels) in sync with the reading theme.
  // Gated on settings.loaded: before the server settings arrive, value.theme is
  // the compile-time default ("light"), and applying it would flash the shell
  // to light AND overwrite the localStorage palette cache the pre-paint
  // bootstrap relies on. The cached theme from index.html stays up instead.
  createEffect(
    () => settings.value.theme,
    (id) => {
      if (settings.loaded) applyTheme(id);
      return undefined;
    },
  );

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

  // Warm the side-panel chunks on idle so their first open resolves from the
  // module cache instead of hitting the network. clientOnly's own import()
  // dedupes against the module cache, so this is the whole mechanism — no
  // component state to set. Preview mode exposes only Settings; keep its three
  // unreachable panel chunks out of the specimen's idle-time work.
  function prewarmPanels(): void {
    if (isSpecimen) {
      void import("~/components/reader/SettingsPanel").catch(() => {});
      return;
    }
    void import("~/components/reader/TocPanel").catch(() => {});
    void import("~/components/reader/SettingsPanel").catch(() => {});
    void import("~/components/reader/SearchPanel").catch(() => {});
    void import("~/components/reader/BookmarksPanel").catch(() => {});
  }

  let panelPrewarm: { cancel: () => void } | null = null;

  onSettled(() => {
    void boot();
    resetChromeTimer();
    panelPrewarm = schedulePrewarm(prewarmPanels);

    window.addEventListener("keydown", handleWindowKey);
    window.addEventListener("pointermove", handlePointerActivity);
    document.addEventListener("visibilitychange", handleVisibility);

    onCleanup(() => {
      window.removeEventListener("keydown", handleWindowKey);
      window.removeEventListener("pointermove", handlePointerActivity);
      document.removeEventListener("visibilitychange", handleVisibility);
      // SPA route changes that don't go through handleBack or a page
      // visibilitychange (command-palette navigation, browser back, or a
      // keyed-bookId remount) unmount the reader without flushing, dropping up
      // to one save-interval of progress. Beacon the latest position on the
      // way out, fire-and-forget like the page-hide path. Send even an
      // unchanged position: leaving the reader is itself a last-read event,
      // and the backend coalescer collapses duplicate positions before the
      // WAL write.
      if (bookLoaded && !isSpecimen) {
        publishLibraryProgress(saveData.chapter, saveData.percent);
        beaconProgress(bookId, { ...saveData });
      }
      cancelProgressSave();
      fetchAbort?.abort();
      if (highlightTimer) clearTimeout(highlightTimer);
      if (chromeHideTimer) clearTimeout(chromeHideTimer);
      if (applyRaf !== null) cancelAnimationFrame(applyRaf);
      panelPrewarm?.cancel();
    });
  });

  // The outside-pointer listener for the ⋯ menu exists only while the menu is
  // open (the Svelte version toggled the handler binding on moreOpen).
  createEffect(
    () => moreOpen(),
    (open) => {
      if (!open) return undefined;
      window.addEventListener("pointerdown", onMoreOutside);
      return () => window.removeEventListener("pointerdown", onMoreOutside);
    },
  );

  async function boot(): Promise<void> {
    try {
      await settings.load();
    } catch {
      // keep defaults
    }

    // User font families are non-blocking for startup; faces re-push on load.
    void fontRegistry.load();

    // The specimen is fully client-side: no saved position and no bookmarks.
    if (isSpecimen) {
      await openBook({ chapter: 0, percent: 0 });
      return;
    }

    let saved: ProgressData = { chapter: 0, percent: 0 };
    try {
      saved = await getProgress(bookId);
      lastPersistedChapter = saved.chapter;
      lastPersistedPercent = saved.percent;
    } catch {
      // first periodic save will retry
    }

    // A remaining page-hide cache is newer than the last successful normal
    // save (which removes it), even when the user navigated backward.
    try {
      // One-time migration: drop the pre-profile-scoped key so a stale cache
      // written by another profile can never win chooseBootProgress here.
      localStorage.removeItem(`sayumi:progress:${bookId}`);
      const raw = localStorage.getItem(progressCacheKey);
      if (raw) {
        const cached: ProgressData = JSON.parse(raw);
        saved = chooseBootProgress(saved, cached);
      }
    } catch {
      // ignore malformed cache
    }

    lastBootProgress = saved;
    await openBook(saved);

    // Restoring a position is otherwise silent — say where the book resumed
    // so mid-book openings don't feel arbitrary. Only for a real position.
    const b = book();
    if (bookLoaded && b && (saved.chapter > 0 || saved.percent > 0.02)) {
      const ch = Math.max(0, Math.min(saved.chapter, b.chapterCount - 1));
      showToast(
        `Resumed at Ch ${ch + 1} · ${Math.round(saved.percent * 100)}%`,
      );
    }

    // Bookmarks are non-blocking for reader startup.
    getBookmarks(bookId)
      .then((bms) => setBookmarks(bms))
      .catch(() => {});
  }

  // Fetches the book metadata and starts the initial chapter render. Split out
  // of boot() so Retry can re-attempt just this step — the rest of boot
  // (settings, progress, fonts) has already run by then.
  async function openBook(saved: ProgressData): Promise<void> {
    setError("");
    setBookLoadFailed(false);
    if (isSpecimen) {
      setBook(specimenBookDetail());
      bookLoaded = true;
      setCurrentChapter(0);
      saveData = { chapter: 0, percent: 0 };
      tryInitialLoad();
      return;
    }
    try {
      const data = await getBook(bookId);
      setBook(data);
      bookLoaded = true;
      const chapter = Math.max(
        0,
        Math.min(saved.chapter, data.chapterCount - 1),
      );
      setCurrentChapter(chapter);
      saveData = { chapter, percent: saved.percent, cfi: saved.cfi };
      tryInitialLoad();
      scheduleProgressSave();
    } catch (err) {
      setBookLoadFailed(true);
      // Opening a book is the first request made after going offline from the
      // library, so an unreachable server is the common failure here. Say so
      // plainly instead of a generic "Failed to load book".
      setError(
        isReachable()
          ? getErrorMessage(err, "Failed to load book")
          : "Can't open this book while offline.",
      );
    }
  }

  function retryOpen(): void {
    void openBook(lastBootProgress);
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
    if (isSpecimen) return specimenChapter();
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
    const b = book();
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
    setChapterLoading(true);
    setError("");

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

      setCurrentChapter(index);
      setChapterPercent(nextPercent);
      setChapterDirection(data.direction === "rtl" ? "rtl" : "ltr");
      saveData = { chapter: index, percent: nextPercent, cfi: nextCFI };

      if (!pendingNav) {
        if (hasNext) void fetchChapterWithRetry(index + 1).catch(() => {});
        if (hasPrev) void fetchChapterWithRetry(index - 1).catch(() => {});
      }
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") return;
      setError(getErrorMessage(err, "Failed to load chapter"));
    } finally {
      chapterLoadInProgress = false;
      setChapterLoading(false);
      lastChapterSwapAt = Date.now();
      const nav = pendingNav;
      if (nav) {
        pendingNav = null;
        void loadChapter(nav.index, nav.scrollTo, nav.fragment, nav.restore);
      }
    }
  }

  // ---- progress persistence -----------------------------------------------
  function flushProgress(force = false): Promise<void> {
    if (!bookLoaded || isSpecimen) return Promise.resolve();
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
        publishLibraryProgress(payload.chapter, payload.percent);
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
    if (!bookLoaded || isSpecimen || progressTimer) return;
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
    if (!bookLoaded || isSpecimen) return;
    if (document.visibilityState === "hidden") {
      cancelProgressSave();
      try {
        localStorage.setItem(progressCacheKey, JSON.stringify(saveData));
      } catch {
        // ignore
      }
      publishLibraryProgress(saveData.chapter, saveData.percent);
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
    if (h.chapterIndex !== currentChapter()) return;
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
    const b = book();
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
    setChapterPercent(safePercent);
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
    if (now - lastChapterSwapAt < POST_SWAP_BOUNDARY_GRACE_MS) return;
    lastBoundaryTime = now;
    const b = book();
    if (!b) return;
    if (boundary === "end" && currentChapter() + 1 < b.chapterCount)
      void loadChapter(currentChapter() + 1, "top");
    else if (boundary === "start" && currentChapter() > 0)
      void loadChapter(currentChapter() - 1, "end");
  }
  function handleFrameError(_code: string, message: string): void {
    // An in-iframe render failure: stop the spinner and show the error UI with
    // Retry instead of silently swallowing it.
    chapterLoadInProgress = false;
    setChapterLoading(false);
    setError(message || "Failed to render this chapter.");
  }

  function handleLinkClicked(href: string): void {
    const b = book();
    if (!b) return;
    const resolved = resolveHref(href, b.spine, currentChapter());
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter()) {
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
    const b = book();
    if (!b) return;
    const resolved = resolveHref(href, b.spine);
    if (!resolved) return;
    if (resolved.chapterIndex === currentChapter()) {
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
    if (isPaged()) api?.prevPage();
    else if (book() && currentChapter() > 0)
      void loadChapter(currentChapter() - 1, "top");
  }
  function goNext(): void {
    if (isPaged()) api?.nextPage();
    else if (book() && currentChapter() + 1 < book()!.chapterCount)
      void loadChapter(currentChapter() + 1, "top");
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
    const b = book();
    if (!b || !isValidSearchResult(result, b)) {
      cancelPendingHighlight();
      api?.clearHighlights();
      showToast("Search result is no longer available");
      return;
    }

    cancelPendingHighlight();
    api?.clearHighlights();
    if (result.chapterIndex === currentChapter()) {
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
    const existingId = currentBookmarkId();
    if (existingId) {
      const prev = bookmarks();
      setBookmarks(bookmarks().filter((b) => b.id !== existingId));
      try {
        await deleteBookmark(bookId, existingId);
        showToast("Bookmark removed");
      } catch (err) {
        setBookmarks(prev);
        showToast(getErrorMessage(err, "Failed to remove bookmark"));
      }
      return;
    }
    try {
      const bm = await createBookmark(bookId, {
        chapter: currentChapter(),
        percent: chapterPercent(),
        cfi: saveData.cfi,
      });
      setBookmarks([...bookmarks(), bm]);
      showToast("Bookmark added");
    } catch (err) {
      showToast(getErrorMessage(err, "Failed to add bookmark"));
    }
  }

  function navigateBookmark(bm: Bookmark): void {
    setPanel("none");
    if (bm.chapter === currentChapter()) {
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
    const prev = bookmarks();
    setBookmarks(bookmarks().filter((b) => b.id !== id));
    try {
      await deleteBookmark(bookId, id);
    } catch (err) {
      setBookmarks(prev);
      showToast(getErrorMessage(err, "Failed to remove bookmark"));
    }
  }

  async function editBookmark(
    id: string,
    label: string,
    comment: string,
  ): Promise<void> {
    const prev = bookmarks();
    // No map-spread: build the optimistic list with a plain loop (the oxc
    // no-map-spread convention — see CommandPalette).
    const optimistic: Bookmark[] = [];
    for (const b of bookmarks()) {
      optimistic.push(b.id === id ? { ...b, label, comment } : b);
    }
    setBookmarks(optimistic);
    try {
      const updated = await updateBookmark(bookId, id, { label, comment });
      setBookmarks(bookmarks().map((b) => (b.id === id ? updated : b)));
    } catch (err) {
      setBookmarks(prev);
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
    // Same stand-down for the ⋯ overflow menu. Its own keydown handler covers
    // keys while focus is inside it, but focus can also sit on the trigger
    // button (mouse open) — without this, Escape would navigate back to the
    // library and letter shortcuts would toggle panels under the open menu.
    if (moreOpen()) {
      if (e.key === "Escape") {
        closeMore();
        return true;
      }
      return [
        "t",
        "T",
        "s",
        "S",
        "f",
        "F",
        "b",
        "B",
        "?",
        "ArrowLeft",
        "ArrowRight",
      ].includes(e.key);
    }
    // The specimen has no TOC, search, or bookmarks; ignore those shortcuts so
    // they can't open panels whose buttons are hidden in preview mode.
    if (
      isSpecimen &&
      (e.key === "t" ||
        e.key === "T" ||
        e.key === "f" ||
        e.key === "F" ||
        e.key === "b" ||
        e.key === "B")
    )
      return false;
    switch (e.key) {
      case "?":
        // The book renders in an iframe; once it has focus the window-level
        // handler in App never sees this key, so open the modal here
        // (the iframe forwards keystrokes to handleKeyAction).
        ui.openShortcuts();
        return true;
      case "Escape":
        if (activePanel() !== "none") {
          setPanel("none");
          resetChromeTimer();
        } else handleBack();
        return true;
      case "ArrowLeft":
        // In RTL paged mode, visual-left advances reading order, mirroring the
        // swipe handler so keyboard and touch agree.
        if (isPaged() && isRTL()) goNext();
        else goPrev();
        return true;
      case "ArrowRight":
        if (isPaged() && isRTL()) goPrev();
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
        void toggleBookmark();
        return true;
      case "B":
        // Shift+B opens the bookmarks panel (plain B toggles a bookmark).
        togglePanel("bookmarks");
        return true;
    }
    return false;
  }

  // True when the focused element consumes ordinary keystrokes, so reader
  // shortcuts must stand down. Deliberately NOT a blanket tagName check:
  // checkbox/button-flavored inputs don't type or use arrows, and swallowing
  // there left Escape/letters dead after clicking a settings toggle. Radio and
  // range DO consume arrows natively (group nav / slider), so they stay
  // guarded along with all text-entry types.
  function isKeyboardConsumer(el: Element | null): boolean {
    if (!el) return false;
    if (el instanceof HTMLElement && el.isContentEditable) return true;
    const tag = el.tagName;
    if (tag === "TEXTAREA" || tag === "SELECT") return true;
    if (tag !== "INPUT") return false;
    const type = (el as HTMLInputElement).type;
    return !["checkbox", "button", "submit", "reset", "file"].includes(type);
  }

  function handleWindowKey(e: KeyboardEvent): void {
    // App's window handler owns Ctrl/Cmd+K on this path; handling it here too
    // double-toggled the palette (open, then instantly closed).
    // handleKeyAction keeps its palette branch for iframe-forwarded keys,
    // which App never sees.
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) return;
    if (isKeyboardConsumer(document.activeElement)) return;
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
    if (activePanel() !== "none") {
      setPanel("none");
      resetChromeTimer();
      return;
    }
    if (isPaged()) {
      // Mirror reading order in RTL so tap edges agree with the keyboard
      // (ArrowLeft/ArrowRight) and the swipe handler: visual-left turns one
      // step *back* in reading order for LTR and *forward* for RTL.
      if (region === "left") {
        if (isRTL()) api?.nextPage();
        else api?.prevPage();
      } else if (region === "right") {
        if (isRTL()) api?.prevPage();
        else api?.nextPage();
      } else toggleChrome();
      return;
    }
    toggleChrome();
  }

  return (
    <div class={["rdp", { "rdp-chrome-hidden": !chromeVisible() }]}>
      <header
        class={["rdp-bar", { "rdp-hidden": !chromeVisible() }]}
        inert={panelOpen() || !chromeVisible()}
        aria-hidden={!chromeVisible() ? "true" : "false"}
      >
        <button
          class="rdp-icon"
          onClick={handleBack}
          aria-label="Back to library"
        >
          <Icon icon={ArrowLeft} />
        </button>
        <div class="rdp-title">
          <span class="rdp-book display">
            {book()?.title ?? (bookLoadFailed() ? "Unavailable" : "…")}
          </span>
          <Show when={book()}>
            {(b) => (
              <span class="rdp-chapter">
                {chapterLabel() || `Chapter ${currentChapter() + 1}`} ·{" "}
                <span class="tnum">
                  {currentChapter() + 1}/{b().chapterCount}
                </span>
              </span>
            )}
          </Show>
        </div>
        <div class="rdp-tools">
          <Show when={!isSpecimen}>
            <button
              class={["rdp-icon", { active: currentBookmarkId() !== null }]}
              onClick={() => void toggleBookmark()}
              aria-label={
                currentBookmarkId() ? "Remove bookmark" : "Add bookmark"
              }
              aria-pressed={currentBookmarkId() !== null ? "true" : "false"}
            >
              <Icon icon={currentBookmarkId() ? BookmarkCheck : BookmarkIcon} />
            </button>
            <button
              class="rdp-icon rdp-fold"
              onClick={() => togglePanel("bookmarks")}
              aria-label="Bookmarks"
              aria-pressed={activePanel() === "bookmarks" ? "true" : "false"}
            >
              <Icon icon={BookMarked} />
            </button>
            <button
              class="rdp-icon rdp-fold"
              onClick={() => togglePanel("search")}
              aria-label="Search in book"
              aria-pressed={activePanel() === "search" ? "true" : "false"}
            >
              <Icon icon={Search} />
            </button>
          </Show>
          <button
            class={["rdp-icon", { "rdp-fold": !isSpecimen }]}
            onClick={() => togglePanel("settings")}
            aria-label="Settings"
            aria-pressed={activePanel() === "settings" ? "true" : "false"}
          >
            <Icon icon={Settings} />
          </button>
          <Show when={!isSpecimen}>
            <button
              class="rdp-icon"
              onClick={() => togglePanel("toc")}
              aria-label="Table of contents"
              aria-pressed={activePanel() === "toc" ? "true" : "false"}
            >
              <Icon icon={List} />
            </button>
          </Show>
          <button
            class={["rdp-icon", { "rdp-fold": !isSpecimen }]}
            onClick={() => ui.openShortcuts()}
            aria-label="Keyboard shortcuts"
          >
            <Icon icon={CircleHelp} />
          </button>
          <Show when={!isSpecimen}>
            {/* Narrow viewports: the folded tools live here instead. */}
            <div class="rdp-more-dd">
              <button
                ref={(el) => {
                  moreBtn = el;
                }}
                class="rdp-icon rdp-more"
                aria-haspopup="menu"
                aria-expanded={moreOpen() ? "true" : "false"}
                aria-label="More tools"
                onClick={toggleMore}
              >
                <Icon icon={Ellipsis} />
              </button>
              <Show when={moreOpen()}>
                <div
                  ref={(el) => {
                    moreMenuEl = el;
                  }}
                  class="rdp-more-menu paper"
                  role="menu"
                  tabindex="-1"
                  aria-label="More tools"
                  onKeyDown={onMoreKeydown}
                >
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="0"
                    ref={(el) => {
                      el.focus();
                    }}
                    onClick={() => pickMore(() => togglePanel("search"))}
                  >
                    <Icon icon={Search} size={16} />
                    Search in book
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => togglePanel("bookmarks"))}
                  >
                    <Icon icon={BookMarked} size={16} />
                    Bookmarks
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => togglePanel("settings"))}
                  >
                    <Icon icon={Settings} size={16} />
                    Settings
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => ui.openShortcuts())}
                  >
                    <Icon icon={CircleHelp} size={16} />
                    Keyboard shortcuts
                  </button>
                </div>
              </Show>
            </div>
          </Show>
        </div>
      </header>

      <div class="rdp-stage">
        <div class="rdp-stage-content" inert={panelOpen()}>
          <Show when={book()}>
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
          </Show>

          <Show when={chapterLoading()}>
            <div class="rdp-loading" role="status" aria-live="polite">
              <span class="rdp-loading-mark" aria-hidden="true">
                ❦
              </span>
              <span class="rdp-sr-only">Loading chapter…</span>
            </div>
          </Show>

          <Show when={error()}>
            <div class="rdp-error" role="alert">
              <p class="rdp-error-title display">Something went wrong.</p>
              <p>{error()}</p>
              <button
                class="btn-ghost press"
                onClick={() =>
                  bookLoadFailed()
                    ? retryOpen()
                    : void loadChapter(currentChapter())
                }
              >
                Retry
              </button>
            </div>
          </Show>
        </div>

        {/* Side panels: clientOnly + lazy (imports deferred to first open,
            idle-prewarmed). focusTrap on the wrapper via ref + onCleanup. */}
        <Show when={activePanel() === "toc"}>
          <Show when={book()}>
            {(b) => (
              <>
                <div
                  class="rdp-panel rdp-left"
                  role="dialog"
                  aria-modal="true"
                  aria-label="Table of contents"
                  ref={(el) => onCleanup(focusTrap(el))}
                >
                  <TocPanel
                    fallback={null}
                    toc={b().toc}
                    activeEntry={activeTocEntry()}
                    entryChapter={entryStartChapter()}
                    currentChapter={currentChapter()}
                    positionLabel={`Ch ${currentChapter() + 1} of ${b().chapterCount}`}
                    onnavigate={handleTocNavigate}
                    onclose={closePanel}
                  />
                </div>
                <button
                  class="rdp-scrim"
                  aria-label="Close panel backdrop"
                  tabindex="-1"
                  onClick={closePanel}
                />
              </>
            )}
          </Show>
        </Show>

        <Show when={activePanel() === "bookmarks"}>
          <div
            class="rdp-panel rdp-left"
            role="dialog"
            aria-modal="true"
            aria-label="Bookmarks"
            ref={(el) => onCleanup(focusTrap(el))}
          >
            <BookmarksPanel
              fallback={null}
              bookmarks={bookmarks()}
              chapterTitle={chapterTitleFor}
              onnavigate={navigateBookmark}
              ondelete={(id) => void removeBookmark(id)}
              onupdate={(id, label, comment) =>
                void editBookmark(id, label, comment)
              }
              onclose={closePanel}
            />
          </div>
          <button
            class="rdp-scrim"
            aria-label="Close panel backdrop"
            tabindex="-1"
            onClick={closePanel}
          />
        </Show>

        <Show when={activePanel() === "search"}>
          <div
            class="rdp-panel rdp-right"
            role="dialog"
            aria-modal="true"
            aria-label="Search in book"
            ref={(el) => onCleanup(focusTrap(el))}
          >
            <SearchPanel
              fallback={null}
              bookId={bookId}
              chapterCount={book()?.chapterCount ?? 0}
              onresultclick={navigateToResult}
              onclose={closePanel}
            />
          </div>
          <button
            class="rdp-scrim"
            aria-label="Close panel backdrop"
            tabindex="-1"
            onClick={closePanel}
          />
        </Show>

        <Show when={activePanel() === "settings"}>
          <div
            class="rdp-panel rdp-right"
            role="dialog"
            aria-modal="true"
            aria-label="Settings"
            ref={(el) => onCleanup(focusTrap(el))}
          >
            <SettingsPanel fallback={null} onclose={closePanel} />
          </div>
          {/* Settings only: an invisible click-catcher instead of the veil, so
              the typography controls live-preview against the undimmed page. */}
          <button
            class="rdp-scrim rdp-quiet"
            aria-label="Close panel backdrop"
            tabindex="-1"
            onClick={closePanel}
          />
        </Show>
      </div>

      {/* Scroll mode has no page-indicator pill, so the progress bar is the only
          positional cue: keep it visible even when the chrome auto-hides. Paged
          mode shows the indicator pill, so the bar can hide with the chrome. */}
      <div
        class={[
          "rdp-progress",
          { "rdp-hidden": !chromeVisible() && isPaged() },
        ]}
        aria-hidden="true"
      >
        <div
          class="rdp-fill"
          style={{ "--progress-scale": chapterPercent() }}
        />
      </div>

      {/* Book-level position at a glance. Shown only while the chrome is hidden:
          with the bar up, the title eyebrow already reports the chapter, so the
          chip takes over as the whereabouts cue once the chrome tucks away. */}
      {!isSpecimen && (
        <Show when={book()}>
          {(b) => (
            <div
              class={["rdp-pos tnum", { "rdp-hidden": chromeVisible() }]}
              aria-hidden="true"
            >
              Ch {currentChapter() + 1}/{b().chapterCount} ·{" "}
              {Math.round(chapterPercent() * 100)}%
            </div>
          )}
        </Show>
      )}
    </div>
  );
}
