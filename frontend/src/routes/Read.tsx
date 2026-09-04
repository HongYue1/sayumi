// Read: the reader route — ChapterFrame wiring, side panels, progress
// tracking, keyboard nav.
//
// Solid 2.0 notes:
//   - Rendered state is signals; the large non-reactive instance state (api,
//     saveData, chapterCache, timers, pendingNav/pendingHighlight, …) stays
//     plain lets, as in ChapterFrame.
//   - The four side panels are clientOnly(loader, { lazy: true }) so the
//     import defers to first open; an idle-time prewarm (direct import(),
//     module-cache-deduped with clientOnly's own) keeps first open at a
//     one-microtask fallback. Specimen mode still prewarms Settings only.
//   - Window/document listeners attach in onSettled and tear down via its
//     returned cleanup; the moreOpen-conditional pointerdown listener is a
//     compute/apply createEffect keyed on moreOpen().
//   - The font-face push, the settings push, and the overflow menu's
//     outside-pointer listener are compute/apply createEffects — the apply
//     phase never tracks, which is exactly the guarantee untrack() would give.
//   - Panel enter animations are CSS keyframes (rdp-panel-in-left/right,
//     rdp-fade-in) with a prefers-reduced-motion kill switch owned by CSS.
//     Exit animations are dropped: nothing in the Solid 2 component set
//     plays an exit transition.
//   - No per-panel "couldn't load / Retry" UI: clientOnly exposes no error
//     state, and with the idle prewarm a chunk failure means the whole reader
//     chunk graph is broken anyway.
import {
  createEffect,
  createMemo,
  createSignal,
  onSettled,
  Show,
  untrack,
} from "solid-js";
import { clientOnly } from "@solidjs/web";
import {
  isProgressDuplicate,
  chooseBootProgress,
  findBookmarkAtPosition,
  calcBookProgress,
  PROGRESS_UNSET,
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
import { keyboardEventIsOwnedByTarget } from "~/lib/keyboard";
import { resolveHref, buildTocChapterEntries } from "~/lib/href";
import { getErrorMessage } from "~/lib/errors";
import { isReachable } from "~/lib/reachability";
import ChapterFrame from "~/components/reader/ChapterFrame";
import type {
  ChapterFrameAPI,
  KeyEvent,
} from "~/components/reader/frame-types";
import type { FrameModeState } from "~/lib/frameMessages";
import Icon from "~/lib/Icon";
import { trap } from "~/lib/focusTrap";
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
// Upper bound for the frame's answer to a position request on the way out.
// The round trip is a synchronous layout read plus two postMessages (~ms),
// so this only ever bites when the frame cannot answer at all (mid-restore
// reports are suppressed; an unready frame queues) — and then Back must not
// hang behind a chapter that will never report.
const POSITION_REQUEST_TIMEOUT_MS = 200;
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
  // Intentional one-time read of a signal-backed getter at component-body top
  // level: untrack marks it deliberate and silences STRICT_READ_UNTRACKED.
  // Correctness comes from App's keyed Show remounting Read per
  // book; the publisher is deliberately bound to the profile NAME (dead after
  // a switch), not a live subscription.
  const bootProfile = untrack(() => session.profile);
  const progressCacheKey = `sayumi:progress:${bootProfile ?? ""}:${bookId}`;
  // The reader doubles as a live typography preview: a sentinel bookId renders
  // a built-in specimen chapter entirely client-side (no book/progress/bookmark
  // server calls), so users can tune reading settings against rich sample text.
  const isSpecimen = bookId === SPECIMEN_BOOK_ID;
  // Bind reader-to-library updates to the profile generation that opened this
  // route. A delayed save from an old reader must never touch a later profile.
  const publishLibraryProgress = library.createReadingProgressPublisher(
    bootProfile,
    bookId,
  );

  // Reactive (rendered) state.
  const [book, setBook] = createSignal<BookDetail | null>(null);
  const [currentChapter, setCurrentChapter] = createSignal(0);
  const [chapterPercent, setChapterPercent] = createSignal(0);
  // The current position's anchor as a REACTIVE value: currentBookmarkId is a
  // memo, and memos compute eagerly at setup — reading saveData.cfi there hit
  // the TDZ (saveData is declared hundreds of lines below the memo), and a
  // plain-let read would also miss a cfi-only change at an unchanged percent.
  const [currentCfi, setCurrentCfi] = createSignal<string | undefined>(
    undefined,
  );
  const [chapterDirection, setChapterDirection] = createSignal("ltr");
  // Null until the frame applies its first settings payload. Afterwards this is
  // the authoritative mode actually rendering, including vertical fallbacks.
  const [frameMode, setFrameMode] = createSignal<FrameModeState | null>(null);
  const [chapterLoading, setChapterLoading] = createSignal(false);
  const [error, setError] = createSignal("");
  // True when the book itself (not just a chapter) failed to load, so Retry
  // re-fetches the book and the empty frame shows a book-level message.
  const [bookLoadFailed, setBookLoadFailed] = createSignal(false);
  const [activePanel, setActivePanel] = createSignal<Panel>("none");
  const [bookmarks, setBookmarks] = createSignal<Bookmark[]>([]);
  const [chromeVisible, setChromeVisible] = createSignal(true);
  const [moreOpen, setMoreOpen] = createSignal(false);

  const effectiveDisplayMode = createMemo(
    () => frameMode()?.mode ?? settings.value.displayMode,
  );
  const isPaged = createMemo(() => effectiveDisplayMode() !== "scroll");
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
  // A bookmark at the current reading position, if any: exact-anchor
  // match when both sides carry a cfi, the legacy percent bucket otherwise,
  // and the nearest match wins -- the toggle deletes what this returns.
  const currentBookmarkId = createMemo(
    () =>
      findBookmarkAtPosition(
        bookmarksByChapter().get(currentChapter()) ?? [],
        currentChapter(),
        chapterPercent(),
        currentCfi(),
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
  let bookLoaded = false;
  let initialLoadRequested = false;
  let saveData: ProgressData = { chapter: 0, percent: 0 };
  const chapterCache = new Map<number, ChapterData>();
  // In-flight chapter fetches, so a navigation racing its own prefetch joins
  // the shared request instead of issuing a duplicate fetch + EPUB decode.
  const chapterInflight = new Map<number, Promise<ChapterData>>();
  let chapterLoadInProgress = false;
  let pendingNav: {
    index: number;
    scrollTo: "top" | "end";
    fragment?: string;
    restore?: { percent: number; cfi?: string };
  } | null = null;
  // The most recent failed chapter navigation, so the error Retry re-attempts
  // the chapter that actually failed rather than the current one.
  let lastFailedNav: {
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
  // Waiter for a position request issued on the way out (handleBack). Resolved
  // by the next handlePosition report, or by its timeout — whichever first.
  let pendingPositionResolve: (() => void) | null = null;
  let pendingPositionTimer: ReturnType<typeof setTimeout> | undefined;
  let lastPersistedChapter = PROGRESS_UNSET;
  let lastPersistedPercent = PROGRESS_UNSET;
  // Part of the dedupe key, not just payload: a relayout can hold percent
  // while moving the anchor, and that write must not be swallowed.
  let lastPersistedCfi: string | undefined;
  let lastBoundaryTime = 0;
  // Serializes bookmark create/delete: the add path applies only after its
  // await, so an unguarded double-toggle duplicates — and an add that resolves
  // after a remove was meant to win ghosts the bookmark back.
  let bookmarkOpInFlight = false;
  let bookmarkToggleQueued = false;
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
    const next: Panel = activePanel() === p ? "none" : p;
    setPanel(next);
    // A panel is part of the chrome — keep it visible while open. Branch on
    // the computed value: an accessor read here still returns the pre-write
    // value until the flush, which silently inverted both arms.
    if (next !== "none") showChrome(false);
    else resetChromeTimer(next);
  }
  function closePanel(): void {
    if (activePanel() === "none") return;
    setPanel("none");
    // Arm against the state just written: resetChromeTimer's own accessor
    // read is still pre-write in this tick.
    resetChromeTimer("none");
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
  // Move focus into the more-tools menu on open. The first row's
  // self-focusing ref could never do it: refs run while the node is still
  // detached, so .focus() no-oped and the active element stayed on the
  // trigger -- leaving the roving keys in onMoreKeydown unreachable and
  // aria-expanded asserting a focus move that never happened. One microtask
  // after open, matching ThemeDropdown and ProfileMenu; the first row carries
  // the markup's tabindex="0" nomination.
  let moreGen = 0;
  createEffect(
    () => moreOpen(),
    (open) => {
      const gen = ++moreGen;
      if (!open) return undefined;
      queueMicrotask(() => {
        if (gen !== moreGen) return;
        const el = moreMenuEl;
        if (!el) return;
        const items = Array.from(
          el.querySelectorAll<HTMLButtonElement>(".rdp-mrow"),
        );
        const preferred = items.find(
          (it) => it.getAttribute("tabindex") === "0",
        );
        (preferred ?? items[0] ?? el).focus();
      });
      return undefined;
    },
  );

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
    // Narrow, don't cast — the same guard as Library's sort menu: a non-Node
    // target must stand down, not count as an outside click.
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (moreMenuEl?.contains(t) || moreBtn?.contains(t)) return;
    closeMore(false);
  }
  function onMoreKeydown(e: KeyboardEvent): void {
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMore();
      return;
    }
    if (e.key === "Tab") {
      // Tab must LEAVE the menu, never wrap inside it (WCAG 2.1.2 / the APG
      // Menu Button pattern) — the same close-on-Tab as every peer menu.
      closeMore();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End"
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
  function resetChromeTimer(panel: Panel = activePanel()): void {
    if (chromeHideTimer) {
      clearTimeout(chromeHideTimer);
      chromeHideTimer = undefined;
    }
    // Never auto-hide while a panel is open — or while focus is inside the
    // bar: hiding would flip the bar inert under a mid-Tab keyboard walk and
    // drop focus to <body>.
    const barHasFocus =
      document.activeElement instanceof HTMLElement &&
      document.activeElement.closest(".rdp-bar") !== null;
    if (panel === "none" && !barHasFocus) {
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
  // Latest iframe settings, written by the settings effect's apply phase — a
  // plain value for other apply phases, because a reactive read in an
  // untracked scope trips STRICT_READ_UNTRACKED (docs29 08).
  let lastIframe: IframeSettings = untrack(() => settings.iframe);
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
  // effect stays keyed solely on fontFaceCSS.
  createEffect(
    () => fontFaceCSS(),
    (faces) => {
      if (api && initialLoadRequested) {
        api.setFontFaces(faces);
        scheduleApplySettings(lastIframe);
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
      lastIframe = s;
      if (api && initialLoadRequested) scheduleApplySettings(s);
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
    window.addEventListener("focusin", handlePointerActivity);
    window.addEventListener("pointermove", handlePointerActivity);
    document.addEventListener("visibilitychange", handleVisibility);

    // onSettled's callback runs in a tracked-effect scope where onCleanup is
    // forbidden (CLEANUP_IN_FORBIDDEN_SCOPE, beta.29 dev) — RETURN the
    // cleanup instead; it runs once at owner disposal, exactly the teardown.
    return () => {
      window.removeEventListener("keydown", handleWindowKey);
      window.removeEventListener("focusin", handlePointerActivity);
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
        // Mirror the page-hide path: the cache is the crash-guard copy when
        // the beacon never lands. A successful save is what removes it (see
        // flushProgress), so its presence always means "newest known
        // position" — chooseBootProgress prefers it over the server.
        try {
          localStorage.setItem(progressCacheKey, JSON.stringify(saveData));
        } catch {
          // ignore
        }
        publishLibraryProgress(saveData.chapter, saveData.percent);
        beaconProgress(bookId, { ...saveData });
      }
      cancelProgressSave();
      fetchAbort?.abort();
      if (highlightTimer) clearTimeout(highlightTimer);
      if (chromeHideTimer) clearTimeout(chromeHideTimer);
      if (pendingPositionTimer) {
        clearTimeout(pendingPositionTimer);
        pendingPositionTimer = undefined;
      }
      pendingPositionResolve = null;
      if (applyRaf !== null) cancelAnimationFrame(applyRaf);
      panelPrewarm?.cancel();
    };
  });

  // The outside-pointer listener for the ⋯ menu exists only while the menu is
  // open.
  createEffect(
    () => moreOpen(),
    (open) => {
      if (!open) return undefined;
      window.addEventListener("pointerdown", onMoreOutside);
      return () => window.removeEventListener("pointerdown", onMoreOutside);
    },
  );

  async function boot(): Promise<void> {
    await settings.activate(bootProfile);

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
      lastPersistedCfi = saved.cfi;
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
        // The cache is writable by anything in this origin — validate before
        // trusting it. A non-integer/NaN chapter would slip the loadChapter
        // bounds guard (NaN comparisons are all false) and wedge the reader in
        // an error loop; a non-finite percent poisons the restore and toast.
        const cacheOk =
          Number.isSafeInteger(cached.chapter) &&
          cached.chapter >= 0 &&
          Number.isFinite(cached.percent) &&
          cached.percent >= 0 &&
          cached.percent <= 1 &&
          (cached.cfi === undefined || typeof cached.cfi === "string");
        if (cacheOk) saved = chooseBootProgress(saved, cached);
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
    void refreshBookmarks();
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
      if (data.chapterCount <= 0) {
        // Empty-spine EPUB: nothing to render — fail at the book level instead
        // of opening a blank reader (Chapter 1/0) whose progress writes 400.
        setBookLoadFailed(true);
        setError("This book has no readable chapters.");
        return;
      }
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
    void refreshBookmarks();
    void openBook(lastBootProgress);
  }

  function tryInitialLoad(): void {
    if (initialLoadRequested || !bookLoaded || !api) return;
    initialLoadRequested = true;
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
    // Join an in-flight fetch instead of issuing a duplicate — the prefetch
    // fired at the previous chapter's load may still be on the wire. Race the
    // caller's signal so an abort still resolves promptly (a superseded
    // navigation must not wait on the shared fetch).
    const inflight = chapterInflight.get(index);
    if (inflight) {
      if (!signal) return inflight;
      return Promise.race([
        inflight,
        new Promise<never>((_, reject) => {
          const onAbort = () =>
            reject(new DOMException("Aborted", "AbortError"));
          if (signal.aborted) onAbort();
          else signal.addEventListener("abort", onAbort, { once: true });
        }),
      ]);
    }
    const request = fetchChapter(
      bookId,
      index,
      CHAPTER_LOAD_RETRY_ATTEMPTS,
      signal,
    );
    chapterInflight.set(index, request);
    try {
      const data = await request;
      // A prefetch that completes after the user moved on must not refresh
      // itself as most-recently-used and evict the warm working set.
      if (Math.abs(index - currentChapter()) <= 1) {
        if (chapterCache.size >= MAX_CHAPTER_CACHE) {
          const lru = chapterCache.keys().next().value;
          if (lru !== undefined) chapterCache.delete(lru);
        }
        chapterCache.set(index, data);
      }
      return data;
    } finally {
      chapterInflight.delete(index);
    }
  }

  async function loadChapter(
    index: number,
    scrollTo: "top" | "end" = "top",
    fragment?: string,
    restore?: { percent: number; cfi?: string },
  ): Promise<void> {
    const b = book();
    if (!b || !api) return;
    // A poisoned boot cache or a bad frame report must never reach the fetch
    // URL: NaN slips past index < 0 || index >= n (NaN comparisons are false).
    if (!Number.isSafeInteger(index) || index < 0 || index >= b.chapterCount)
      return;

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

    // Disarm an armed highlight timer (it could still fire into the outgoing
    // chapter inside its 120ms window) — but keep pendingHighlight: a deferred
    // cross-chapter search highlight is set immediately before this call and
    // must survive to handleLoaded.
    if (highlightTimer) clearTimeout(highlightTimer);
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
      api.loadChapter({
        data,
        settings: settings.iframe,
        scrollTarget: restore ? "top" : scrollTo,
        fragment: restore ? undefined : fragment,
        hasPrev,
        hasNext,
        restore,
        language: b.language,
      });

      setCurrentChapter(index);
      setChapterPercent(nextPercent);
      // The anchor must move with the swap: the frame's first position
      // report only lands after the fonts-gated reveal, and until then a
      // stale previous-chapter cfi would send the bookmark toggle down the
      // exact-anchor path and mis-resolve it against the new chapter.
      setCurrentCfi(nextCFI);
      setChapterDirection(data.direction === "rtl" ? "rtl" : "ltr");
      // The post-swap boundary grace exists so wheel momentum can't chain-skip
      // a chapter the user hasn't seen render — stamp it only when a swap
      // actually happened, not on a failed/aborted load.
      lastChapterSwapAt = Date.now();
      lastFailedNav = null;
      saveData = { chapter: index, percent: nextPercent, cfi: nextCFI };

      if (!pendingNav) {
        if (hasNext) void fetchChapterWithRetry(index + 1).catch(() => {});
        if (hasPrev) void fetchChapterWithRetry(index - 1).catch(() => {});
      }
    } catch (err) {
      if (err instanceof Error && err.name === "AbortError") return;
      // Remember the failed destination so Retry re-attempts IT — retrying the
      // current chapter after a superseded error would reload the chapter
      // already on screen.
      lastFailedNav = { index, scrollTo, fragment, restore };
      setError(getErrorMessage(err, "Failed to load chapter"));
    } finally {
      chapterLoadInProgress = false;
      setChapterLoading(false);
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

    const { chapter, percent, cfi } = saveData;
    if (
      !force &&
      isProgressDuplicate(
        { chapter, percent, cfi },
        {
          chapter: lastPersistedChapter,
          percent: lastPersistedPercent,
          cfi: lastPersistedCfi,
        },
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
        lastPersistedCfi = payload.cfi;
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

  // Promise for the frame's current position, for exit paths that must persist
  // what the reader is looking at RIGHT NOW rather than the throttled
  // saveData (up to ~200ms stale after a scroll burst — exactly the
  // scroll-then-immediately-Back habit). The frame answers get-position with
  // a fresh read and drops its own pending straggler first, so the first
  // report back settles this. Skipped while a chapter load is in flight: the
  // position then is the load target and saveData already holds it.
  function requestFreshPosition(): Promise<void> {
    if (!api || chapterLoadInProgress) return Promise.resolve();
    return new Promise((resolve) => {
      pendingPositionResolve = resolve;
      api?.requestPosition();
      pendingPositionTimer = setTimeout(() => {
        pendingPositionTimer = undefined;
        pendingPositionResolve = null;
        resolve();
      }, POSITION_REQUEST_TIMEOUT_MS);
    });
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
    // ChapterFrame owns the pre-ready queue. Defer the request out of the
    // component callback's owned scope before it writes loading state.
    queueMicrotask(tryInitialLoad);
  }
  function handleModeChange(state: FrameModeState): void {
    setFrameMode(state);
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
    setCurrentCfi(keptCfi);
    saveData = { chapter: chapterIndex, percent: safePercent, cfi: keptCfi };
    // An exit path may be holding Back open for exactly this report (see
    // requestFreshPosition): settle it now that saveData is current.
    if (pendingPositionResolve) {
      const resolve = pendingPositionResolve;
      pendingPositionResolve = null;
      if (pendingPositionTimer) {
        clearTimeout(pendingPositionTimer);
        pendingPositionTimer = undefined;
      }
      resolve();
    }
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
    // A render failure arriving while a newer chapter is still FETCHING is
    // necessarily the outgoing chapter's (the new one has not reached the
    // frame yet): do not release the load latch mid-fetch, kill its spinner,
    // or show the stale error over the chapter being loaded.
    if (chapterLoadInProgress) return;
    // Stop the spinner and show the error UI with Retry instead of silently
    // swallowing it. (The latch is already released by loadChapter's finally
    // by the time a genuine current-chapter load-error can arrive.)
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
    // Persist the live position before leaving: a scroll burst followed by an
    // immediate Back would otherwise flush the pre-burst saveData (the frame
    // reports on a ~200ms trailing edge). The wait is one postMessage round
    // trip, bounded by the request timeout — then navigate either way.
    void (async () => {
      await requestFreshPosition();
      await flushProgress(true);
      router.navigate("/");
    })();
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
    if (chapterLoadInProgress || pendingNav) {
      // A navigation is queued or in flight: an immediate highlight lands in
      // the outgoing chapter and is wiped by the swap. Defer through the same
      // pendingHighlight path as a cross-chapter hit.
      pendingHighlight = {
        chapterIndex: result.chapterIndex,
        charOffset: result.charOffset,
        matchLen: result.matchLen,
        query,
      };
      if (result.chapterIndex !== currentChapter())
        void loadChapter(result.chapterIndex, "top");
      return;
    }
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
  /** Fetches the bookmark list; an in-flight mutation owns the newer state. */
  async function refreshBookmarks(): Promise<void> {
    try {
      const server = await getBookmarks(bookId);
      if (bookmarkOpInFlight) return;
      setBookmarks(server);
    } catch {
      showToast("Couldn't load bookmarks");
    }
  }

  async function toggleBookmark(): Promise<void> {
    // Re-entrant presses queue a single re-toggle that resolves when the
    // in-flight op settles — a second press during the add's await must not
    // start a second create (duplicate), and an add-then-remove must delete.
    if (bookmarkOpInFlight) {
      bookmarkToggleQueued = !bookmarkToggleQueued;
      return;
    }
    bookmarkOpInFlight = true;
    try {
      const existingId = currentBookmarkId();
      if (existingId) {
        const removed = bookmarks().find((b) => b.id === existingId);
        setBookmarks(bookmarks().filter((b) => b.id !== existingId));
        try {
          await deleteBookmark(bookId, existingId);
          showToast("Bookmark removed");
          // A toggle queued during the delete's flight means "undo the
          // delete" — re-create from the removed record (its server id is
          // gone). Done here, not via the finally's re-toggle, because
          // currentBookmarkId() is a memo and this runtime recomputes memos on
          // the scheduler queue — a same-continuation re-read sees the stale
          // pre-delete value (pinned in Read.test.ts).
          if (bookmarkToggleQueued && removed) {
            bookmarkToggleQueued = false;
            try {
              const reAdded = await createBookmark(bookId, {
                chapter: removed.chapter,
                percent: removed.percent,
                cfi: removed.cfi,
              });
              setBookmarks([...bookmarks(), reAdded]);
              showToast("Bookmark added");
            } catch (err) {
              showToast(getErrorMessage(err, "Failed to add bookmark"));
            }
          }
        } catch (err) {
          // Surgical rollback: re-add only the removed bookmark, never restore
          // a whole pre-flight snapshot over a concurrent op's newer state.
          if (removed) setBookmarks([...bookmarks(), removed]);
          showToast(getErrorMessage(err, "Failed to remove bookmark"));
        }
      } else {
        try {
          const bm = await createBookmark(bookId, {
            chapter: currentChapter(),
            percent: chapterPercent(),
            cfi: saveData.cfi,
          });
          setBookmarks([...bookmarks(), bm]);
          showToast("Bookmark added");
          // A toggle queued during the create's flight means "undo the add" —
          // delete by the id just created (memo staleness note above).
          if (bookmarkToggleQueued) {
            bookmarkToggleQueued = false;
            setBookmarks(bookmarks().filter((b) => b.id !== bm.id));
            try {
              await deleteBookmark(bookId, bm.id);
              showToast("Bookmark removed");
            } catch (err) {
              setBookmarks([...bookmarks(), bm]);
              showToast(getErrorMessage(err, "Failed to remove bookmark"));
            }
          }
        } catch (err) {
          showToast(getErrorMessage(err, "Failed to add bookmark"));
        }
      }
    } finally {
      bookmarkOpInFlight = false;
      if (bookmarkToggleQueued) {
        bookmarkToggleQueued = false;
        void toggleBookmark();
      }
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
    const removed = bookmarks().find((b) => b.id === id);
    setBookmarks(bookmarks().filter((b) => b.id !== id));
    try {
      await deleteBookmark(bookId, id);
    } catch (err) {
      if (removed) setBookmarks([...bookmarks(), removed]);
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
      const prior = prev.find((b) => b.id === id);
      if (prior)
        setBookmarks(bookmarks().map((b) => (b.id === id ? prior : b)));
      showToast(getErrorMessage(err, "Failed to update bookmark"));
    }
  }

  // Returns true when the key was acted on, so the caller can cancel its
  // default (keeps a letter shortcut from also being typed into a panel input).
  function handleKeyAction(e: {
    key: string;
    ctrlKey?: boolean;
    metaKey?: boolean;
    shiftKey?: boolean;
    altKey?: boolean;
  }): boolean {
    // !e.altKey: AltGr arrives as ctrl+alt on Windows and most Linux
    // layouts, where it types an ordinary character — claiming the chord here
    // opens the palette and steals the keystroke. App.tsx:44 carries the same
    // conjunct for the shell path; frame.ts carries it for the browser default.
    if (
      (e.ctrlKey || e.metaKey) &&
      !e.altKey &&
      (e.key === "k" || e.key === "K")
    ) {
      ui.togglePalette();
      return true;
    }
    if (e.ctrlKey || e.metaKey) return false;
    // A global overlay (command palette / shortcuts help) owns the keyboard
    // while open, so reader shortcuts (Esc → back, arrows, etc.) must stand
    // down — otherwise Esc would close the modal *and* navigate to the library.
    // The store owns the overlay list (ui.anyOverlayOpen); do not re-derive it.
    if (ui.anyOverlayOpen) return false;
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
        " ",
        "PageDown",
        "PageUp",
        "Home",
        "End",
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
        // (the iframe forwards keystrokes to handleKeyAction). Deliberately no
        // altKey exclusion: AltGr/Option is how "?" is typed on several
        // layouts, so guarding on it would silently remove the shortcut there
        // (the shell path's reasoning, App.tsx:39-41).
        ui.openShortcuts();
        return true;
      case "Escape":
        if (activePanel() !== "none") {
          setPanel("none");
          resetChromeTimer("none");
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
      case "PageDown":
      case " ":
        // The frame suppresses these keys' native scroll in paged mode so the
        // parent can drive the navigation (frame.ts PAGED_SCROLL_KEYS +
        // preventDefault) — so the parent must actually drive it. PageDown and
        // Space step forward; Shift+Space and PageUp step back.
        if (isPaged()) {
          if (e.key === " " && e.shiftKey) goPrev();
          else goNext();
          return true;
        }
        return false;
      case "PageUp":
        if (isPaged()) {
          goPrev();
          return true;
        }
        return false;
      case "ArrowDown":
      case "ArrowUp":
        // The frame suppresses these keys' native scroll in paged mode
        // (PAGED_SCROLL_KEYS), so the parent must drive them like the PageDown
        // family above. Vertical arrows are physical — Down steps forward, Up
        // steps back, no RTL mirror — and a vertical-writing chapter never
        // renders paged (the frame falls back to scroll frame-side), so
        // no per-mode axis check is owed here.
        if (isPaged()) {
          if (e.key === "ArrowDown") goNext();
          else goPrev();
          return true;
        }
        return false;
      case "Home":
      case "End":
        // Suppressed frame-side in paged mode; no first/last-page transport
        // exists, so acknowledge the key with a chapter step instead of a
        // silent swallow.
        if (isPaged()) {
          if (e.key === "Home") goPrev();
          else goNext();
          return true;
        }
        return false;
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

  function handleWindowKey(e: KeyboardEvent): void {
    // App's window handler owns Ctrl/Cmd+K on this path, and single ownership
    // is deliberate -- but the justification that used to sit here (that
    // handling it in both places double-toggled the palette, open then
    // instantly closed) was Svelte-rune behaviour and does not hold for this
    // build. Solid batches the write, so two handlers on one keydown both
    // read the pre-write value, both compute the same next state, and the
    // second toggle is MASKED: the palette ends up open. The
    // guard stays precisely because the double dispatch is now silent --
    // lib/ui.test.ts pins the masked-open semantics so a change to them
    // fails there instead of here.
    // handleKeyAction keeps its palette branch for iframe-forwarded keys,
    // which App never sees.
    if (keyboardEventIsOwnedByTarget(e, document.activeElement)) return;
    if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) return;
    // Cancel the default for keys we handle so a letter shortcut (f/s/t/b)
    // isn't also typed into a panel input that opens and grabs focus during
    // this same keystroke.
    if (handleKeyAction(e)) e.preventDefault();
  }

  function handleFrameKey(e: KeyEvent): void {
    // Parent capture listeners cannot observe an event raised in the iframe.
    // If focus ever reaches the book beneath a global overlay, the forwarded
    // Escape is therefore the overlay's only dismissal path. Close only the
    // overlay; a second Escape can then close a reader panel or leave the book.
    if (e.key === "Escape" && ui.anyOverlayOpen) {
      ui.closeOverlays();
      return;
    }
    handleKeyAction(e);
  }

  // Tap/click inside the reader iframe: in paged mode, edges turn the page and
  // the centre toggles the chrome; in scroll mode any tap toggles the chrome.
  // An open panel always closes first.
  function handleClickRegion(region: "left" | "center" | "right"): void {
    if (activePanel() !== "none") {
      setPanel("none");
      resetChromeTimer("none");
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
          <Icon icon={ArrowLeft} labelFromParent />
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
              <Icon
                icon={currentBookmarkId() ? BookmarkCheck : BookmarkIcon}
                labelFromParent
              />
            </button>
            <button
              class="rdp-icon rdp-fold"
              onClick={() => togglePanel("bookmarks")}
              aria-label="Bookmarks"
              aria-pressed={activePanel() === "bookmarks" ? "true" : "false"}
            >
              <Icon icon={BookMarked} labelFromParent />
            </button>
            <button
              class="rdp-icon rdp-fold"
              onClick={() => togglePanel("search")}
              aria-label="Search in book"
              aria-pressed={activePanel() === "search" ? "true" : "false"}
            >
              <Icon icon={Search} labelFromParent />
            </button>
          </Show>
          <button
            class={["rdp-icon", { "rdp-fold": !isSpecimen }]}
            onClick={() => togglePanel("settings")}
            aria-label="Settings"
            aria-pressed={activePanel() === "settings" ? "true" : "false"}
          >
            <Icon icon={Settings} labelFromParent />
          </button>
          <Show when={!isSpecimen}>
            <button
              class="rdp-icon"
              onClick={() => togglePanel("toc")}
              aria-label="Table of contents"
              aria-pressed={activePanel() === "toc" ? "true" : "false"}
            >
              <Icon icon={List} labelFromParent />
            </button>
          </Show>
          <button
            class={["rdp-icon", { "rdp-fold": !isSpecimen }]}
            onClick={() => ui.openShortcuts()}
            aria-label="Keyboard shortcuts"
          >
            <Icon icon={CircleHelp} labelFromParent />
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
                <Icon icon={Ellipsis} labelFromParent />
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
                    onClick={() => pickMore(() => togglePanel("search"))}
                  >
                    <Icon icon={Search} size={16} decorative />
                    Search in book
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => togglePanel("bookmarks"))}
                  >
                    <Icon icon={BookMarked} size={16} decorative />
                    Bookmarks
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => togglePanel("settings"))}
                  >
                    <Icon icon={Settings} size={16} decorative />
                    Settings
                  </button>
                  <button
                    class="rdp-mrow"
                    role="menuitem"
                    tabindex="-1"
                    onClick={() => pickMore(() => ui.openShortcuts())}
                  >
                    <Icon icon={CircleHelp} size={16} decorative />
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
              initialThemeVars={settings.iframe.themeVars}
              initialLanguage={book()?.language ?? null}
              onapi={handleApi}
              onloaded={handleLoaded}
              onmodechange={handleModeChange}
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
              <span class="sr-only">Loading chapter…</span>
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
                    : void loadChapter(
                        lastFailedNav?.index ?? currentChapter(),
                        lastFailedNav?.scrollTo ?? "top",
                        lastFailedNav?.fragment,
                        lastFailedNav?.restore,
                      )
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
                  aria-labelledby="toc-panel-title"
                  ref={trap()}
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
            ref={trap()}
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
          {/* Gated on book() like the TOC panel: the toolbar button and the F
              shortcut both open search before the detail fetch resolves, and
              indefinitely after it fails, where a chapterCount of 0 made the
              validator reject every row and report "No results" for a book it
              had never searched. */}
          <Show when={book()}>
            {(b) => (
              <>
                <div
                  class="rdp-panel rdp-right"
                  role="dialog"
                  aria-modal="true"
                  aria-label="Search in book"
                  ref={trap()}
                >
                  <SearchPanel
                    fallback={null}
                    bookId={bookId}
                    chapterCount={b().chapterCount}
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
              </>
            )}
          </Show>
        </Show>

        <Show when={activePanel() === "settings"}>
          <div
            class="rdp-panel rdp-right"
            role="dialog"
            aria-modal="true"
            aria-label="Settings"
            ref={trap()}
          >
            <SettingsPanel
              fallback={null}
              onclose={closePanel}
              effectiveMode={effectiveDisplayMode()}
              modeFallback={frameMode()?.fallback ?? null}
            />
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
          chip takes over as the whereabouts cue once the chrome tucks away.
          The percent is whole-book (same formula as the library tiles), not
          the chapter: the bottom bar already owns chapter progress in scroll
          mode, and the page pill owns it in paged modes. */}
      {!isSpecimen && (
        <Show when={book()}>
          {(b) => (
            <div
              class={["rdp-pos tnum", { "rdp-hidden": chromeVisible() }]}
              role="status"
              aria-label={`Chapter ${currentChapter() + 1} of ${b().chapterCount}, ${Math.round(calcBookProgress(currentChapter(), chapterPercent(), b().chapterCount) * 100)} percent`}
            >
              Ch {currentChapter() + 1}/{b().chapterCount} ·{" "}
              {Math.round(
                calcBookProgress(
                  currentChapter(),
                  chapterPercent(),
                  b().chapterCount,
                ) * 100,
              )}
              %
            </div>
          )}
        </Show>
      )}
    </div>
  );
}
