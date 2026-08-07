import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createComponent, flush } from "solid-js";
import { render } from "@solidjs/web";
import type * as ApiClient from "~/api/client";
import type {
  ChapterFrameAPI,
  KeyEvent,
} from "~/components/reader/frame-types";
import type ChapterFrameReal from "~/components/reader/ChapterFrame";
import Read from "~/routes/Read";
import { settings } from "~/lib/settings";
import { ui } from "~/lib/ui";

type FrameProps = Parameters<typeof ChapterFrameReal>[0];

// The pivotal seam: ChapterFrame is a capturing stub. It records its props and
// hands back a vi.fn()-backed ChapterFrameAPI at mount; tests drive the frame
// by invoking the recorded handler props (onready, onloaded, onboundary, …).
const frame = vi.hoisted(() => ({
  latest: null as FrameProps | null,
  api: null as unknown as ChapterFrameAPI,
}));

// Props captured by the panel stubs (Toc/Search/Bookmarks) when they open.
let tocPanelLatest: { onnavigate: (href: string) => void } | null = null;
let searchPanelLatest: {
  onresultclick: (r: unknown, q: string) => void;
} | null = null;
let bookmarksPanelLatest: {
  bookmarks: ApiClient.Bookmark[];
  ondelete: (id: string) => void;
} | null = null;

function latestFrame(): FrameProps {
  if (!frame.latest) throw new Error("frame not mounted");
  return frame.latest;
}

/** Returns a frame handler prop, throwing if the stub never received it. */
function frameHandler<K extends keyof FrameProps>(
  name: K,
): NonNullable<FrameProps[K]> {
  const h = latestFrame()[name];
  if (h === undefined || h === null) throw new Error("frame handler missing");
  return h;
}

const api = vi.hoisted(() => ({
  getSettings: vi.fn(),
  saveSettings: vi.fn(),
  getBook: vi.fn(),
  getProgress: vi.fn(),
  saveProgress: vi.fn(),
  beaconProgress: vi.fn(),
  fetchChapter: vi.fn(),
  getBookmarks: vi.fn(),
  createBookmark: vi.fn(),
  updateBookmark: vi.fn(),
  deleteBookmark: vi.fn(),
  // Reached through the real fontRegistry singleton rather than imported by
  // Read.tsx directly: Read.tsx:639 calls fontRegistry.load(), which calls
  // getFonts(). Without these two the `...actual` spread below leaves them
  // live, and every mount fired a real request at the dev-server port.
  getFonts: vi.fn(),
  rescanFonts: vi.fn(),
}));
const showToast = vi.hoisted(() => vi.fn());
const navigate = vi.hoisted(() => vi.fn());
const applyTheme = vi.hoisted(() => vi.fn());
const reachable = vi.hoisted(() => vi.fn(() => true));

// Spread the real module so ApiError stays the exact class the store's
// instanceof checks use — a hand-rolled twin drifts (the real constructor
// takes a fourth `cause` argument).
vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});

vi.mock("~/components/reader/ChapterFrame", () => ({
  default: (props: FrameProps) => {
    frame.latest = props;
    props.onapi?.(frame.api);
    return null;
  },
}));

// Panels are stubs; Toc/Search/Bookmarks capture their props so tests can
// drive onnavigate / onresultclick / ondelete the way the real panels would.
vi.mock("~/components/reader/TocPanel", () => ({
  default: (props: { onnavigate: (href: string) => void }) => {
    tocPanelLatest = props;
    return null;
  },
}));
vi.mock("~/components/reader/SettingsPanel", () => ({ default: () => null }));
vi.mock("~/components/reader/SearchPanel", () => ({
  default: (props: { onresultclick: (r: unknown, q: string) => void }) => {
    searchPanelLatest = props;
    return null;
  },
}));
vi.mock("~/components/reader/BookmarksPanel", () => ({
  default: (props: {
    bookmarks: ApiClient.Bookmark[];
    ondelete: (id: string) => void;
  }) => {
    bookmarksPanelLatest = props;
    return null;
  },
}));

vi.mock("~/lib/router", () => ({ router: { navigate } }));
vi.mock("~/lib/reachability", () => ({
  isReachable: reachable,
  reportReachable: () => {},
  reportUnreachable: () => {},
  subscribeReachability: () => () => {},
}));
vi.mock("~/lib/theme", () => ({ applyTheme }));
vi.mock("~/lib/toast", () => ({ toast: { show: showToast } }));

/** A typed BookDetail fixture: chapterCount chapters, ltr, English. */
function book(chapterCount = 5): ApiClient.BookDetail {
  const spine: ApiClient.SpineEntry[] = [];
  const toc: ApiClient.TocEntry[] = [];
  for (let i = 0; i < chapterCount; i++) {
    spine.push({
      href: `ch${i}.xhtml`,
      id: `ch${i}`,
      mediaType: "application/xhtml+xml",
      linear: true,
    });
    toc.push({ title: `Chapter ${i + 1}`, href: `ch${i}.xhtml`, depth: 0 });
  }
  return {
    id: "book1",
    title: "Test Book",
    author: "Author",
    language: "en",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "ltr",
    chapterCount,
    progress: 0,
    spine,
    toc,
  };
}

/** A typed ChapterData fixture. */
function chapter(i: number): ApiClient.ChapterData {
  return {
    chapterIndex: i,
    html: `<p>chapter ${i}</p>`,
    css: "",
    fontFaceCSS: "",
    direction: "ltr",
    writingMode: "horizontal-tb",
  };
}

function bm(id: string, ch = 0, percent = 0): ApiClient.Bookmark {
  return {
    id,
    chapter: ch,
    percent,
    label: "",
    comment: "",
    createdAt: "2026",
  };
}

/** Flush the promise microtask queue. */
async function flushMicrotasks(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

function key(
  k: string,
  shiftKey = false,
  mods: Partial<Pick<KeyEvent, "ctrlKey" | "altKey" | "metaKey">> = {},
): KeyEvent {
  return {
    key: k,
    code: k,
    ctrlKey: mods.ctrlKey ?? false,
    shiftKey,
    altKey: mods.altKey ?? false,
    metaKey: mods.metaKey ?? false,
  };
}

function loadChapterCalls(): ApiClient.ChapterData[][] {
  return (frame.api.loadChapter as ReturnType<typeof vi.fn>).mock.calls;
}

async function settle(): Promise<void> {
  await flushMicrotasks();
  flush();
  await flushMicrotasks();
  flush();
}

let dispose: (() => void) | null = null;

// A fresh ChapterFrameAPI mock per test (the singletons persist, so the frame
// double is what pins per-test behavior).
beforeEach(() => {
  frame.latest = null;
  frame.api = {
    loadChapter: vi.fn(),
    applySettings: vi.fn(),
    scrollTo: vi.fn(),
    scrollToEnd: vi.fn(),
    scrollToFragment: vi.fn(),
    scrollToCfi: vi.fn(),
    requestPosition: vi.fn(),
    nextPage: vi.fn(),
    prevPage: vi.fn(),
    goToPage: vi.fn(),
    goToLastPage: vi.fn(),
    highlightSearch: vi.fn(),
    clearHighlights: vi.fn(),
    setFontFaces: vi.fn(),
  } satisfies ChapterFrameAPI;
});

beforeEach(() => {
  vi.resetAllMocks();
  vi.useRealTimers();
  localStorage.clear();
  reachable.mockReturnValue(true);
  api.getSettings.mockResolvedValue({});
  api.saveSettings.mockResolvedValue({});
  api.getProgress.mockResolvedValue({ chapter: 0, percent: 0 });
  api.getBook.mockImplementation(() => Promise.resolve(book()));
  api.fetchChapter.mockImplementation((_: string, i: number) =>
    Promise.resolve(chapter(i)),
  );
  api.getBookmarks.mockResolvedValue([]);
  api.saveProgress.mockResolvedValue({});
  api.beaconProgress.mockReturnValue(undefined);
  api.getFonts.mockResolvedValue([]);
  api.rescanFonts.mockResolvedValue([]);
  settings.update({ displayMode: "scroll" });
});

afterEach(() => {
  if (dispose) {
    dispose();
    dispose = null;
  }
  settings.update({ displayMode: "scroll" });
  vi.restoreAllMocks();
});

/**
 * Mounts Read for book1, runs boot to the point where the frame stub has
 * mounted, then drives the frame-ready handshake so the initial chapter loads.
 */
async function bootReader(): Promise<void> {
  const host = document.createElement("div");
  document.body.appendChild(host);
  dispose = render(() => createComponent(Read, { bookId: "book1" }), host);
  await settle();
  if (!frame.latest) await settle();
  frameHandler("onready")();
  await settle();
}

/** Mounts Read with getBook failing; waits for the book-level error UI. */
async function bootToBookError(): Promise<void> {
  const host = document.createElement("div");
  document.body.appendChild(host);
  dispose = render(() => createComponent(Read, { bookId: "book1" }), host);
  await vi.waitFor(() =>
    expect(document.querySelector('[role="alert"]')).not.toBeNull(),
  );
}

describe("Read boot and restore", () => {
  it("boots from server progress and fires exactly one initial load", async () => {
    api.getProgress.mockResolvedValue({ chapter: 2, percent: 0.5 });
    await bootReader();
    expect(frame.api.loadChapter).toHaveBeenCalledTimes(1);
    const [data, , scrollTo, , , , restorePercent] = (
      frame.api.loadChapter as ReturnType<typeof vi.fn>
    ).mock.calls[0];
    expect(data.chapterIndex).toBe(2);
    expect(scrollTo).toBe("top");
    expect(restorePercent).toBe(0.5);
    // A duplicate ready never re-loads.
    frameHandler("onready")();
    await settle();
    expect(frame.api.loadChapter).toHaveBeenCalledTimes(1);
  });

  it("prefers the page-hide cache over the server value", async () => {
    localStorage.setItem(
      "sayumi:progress::book1",
      JSON.stringify({ chapter: 3, percent: 0.8 }),
    );
    api.getProgress.mockResolvedValue({ chapter: 1, percent: 0.1 });
    await bootReader();
    const [data, , , , , , restorePercent] = (
      frame.api.loadChapter as ReturnType<typeof vi.fn>
    ).mock.calls[0];
    expect(data.chapterIndex).toBe(3);
    expect(restorePercent).toBe(0.8);
  });

  it("removes the legacy pre-profile cache key during boot", async () => {
    localStorage.setItem(
      "sayumi:progress:book1",
      JSON.stringify({ chapter: 4, percent: 0.9 }),
    );
    await bootReader();
    expect(localStorage.getItem("sayumi:progress:book1")).toBeNull();
    // …and the legacy value never wins the boot.
    expect(
      (frame.api.loadChapter as ReturnType<typeof vi.fn>).mock.calls[0][0]
        .chapterIndex,
    ).toBe(0);
  });

  it("ignores a malformed cache entry and boots from the server value", async () => {
    localStorage.setItem("sayumi:progress::book1", "{not json");
    api.getProgress.mockResolvedValue({ chapter: 1, percent: 0.25 });
    await bootReader();
    expect(
      (frame.api.loadChapter as ReturnType<typeof vi.fn>).mock.calls[0][0]
        .chapterIndex,
    ).toBe(1);
  });

  it("rejects a poisoned cache (NaN chapter) instead of wedging the reader", async () => {
    localStorage.setItem(
      "sayumi:progress::book1",
      JSON.stringify({ chapter: {}, percent: 0.5 }),
    );
    api.getProgress.mockResolvedValue({ chapter: 1, percent: 0.25 });
    await bootReader();
    // The cache fails validation, so the server value wins — and no NaN ever
    // reaches a fetch URL.
    expect(
      (frame.api.loadChapter as ReturnType<typeof vi.fn>).mock.calls[0][0]
        .chapterIndex,
    ).toBe(1);
    expect(
      api.fetchChapter.mock.calls.some((c) => String(c[1]).includes("NaN")),
    ).toBe(false);
  });

  it("boots at {0,0} with no error UI when the progress fetch fails", async () => {
    api.getProgress.mockRejectedValue(new Error("down"));
    await bootReader();
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(
      (frame.api.loadChapter as ReturnType<typeof vi.fn>).mock.calls[0][0]
        .chapterIndex,
    ).toBe(0);
  });

  it("shows a book-level error and retries getBook when the book fetch fails", async () => {
    api.getBook.mockRejectedValueOnce(new Error("boom"));
    await bootToBookError();
    expect(frame.api.loadChapter).not.toHaveBeenCalled();
    const alert = document.querySelector('[role="alert"]');
    if (!alert) throw new Error("alert missing");
    const retryBtn = Array.from(alert.querySelectorAll("button")).find((btn) =>
      btn.textContent?.includes("Retry"),
    );
    if (!retryBtn) throw new Error("retry button missing");
    retryBtn.click();
    await settle();
    // Retry re-opens the book (and re-fetches bookmarks), not a chapter.
    expect(api.getBook).toHaveBeenCalledTimes(2);
    expect(api.fetchChapter).not.toHaveBeenCalled();
    expect(api.getBookmarks).toHaveBeenCalledTimes(2);
  });

  it("says offline plainly when the book fetch fails while unreachable", async () => {
    reachable.mockReturnValue(false);
    api.getBook.mockRejectedValueOnce(new Error("boom"));
    await bootToBookError();
    expect(document.body.textContent).toContain(
      "Can't open this book while offline.",
    );
  });

  it("fails a 0-chapter book at the book level instead of opening a blank reader", async () => {
    api.getBook.mockResolvedValueOnce(book(0));
    await bootToBookError();
    expect(frame.api.loadChapter).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain(
      "This book has no readable chapters.",
    );
  });
});

describe("Read navigation", () => {
  it("drops a start-boundary on the first chapter and loads next on end-boundary", async () => {
    let now = 10000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    await bootReader();
    now = 11000; // past the 650ms post-swap grace
    frameHandler("onboundary")("start");
    await settle();
    expect(loadChapterCalls().length).toBe(1);
    // Even the no-op start-boundary stamped the cooldown — advance past it.
    now = 11500;
    frameHandler("onboundary")("end");
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(2));
    expect(loadChapterCalls()[1][0].chapterIndex).toBe(1);
  });

  it("swallows boundary events inside the cooldown and post-swap grace windows", async () => {
    let now = 10000;
    vi.spyOn(Date, "now").mockImplementation(() => now);
    await bootReader();
    now = 11000;
    frameHandler("onboundary")("end");
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(2));
    // Swap landed at t=11000. t=11200: inside the 400ms cooldown — dropped.
    now = 11200;
    frameHandler("onboundary")("end");
    await settle();
    expect(loadChapterCalls().length).toBe(2);
    // t=11600: cooldown over, but inside the 650ms post-swap grace — dropped.
    now = 11600;
    frameHandler("onboundary")("end");
    await settle();
    expect(loadChapterCalls().length).toBe(2);
    // t=11800: both windows expired — allowed.
    now = 11800;
    frameHandler("onboundary")("end");
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(3));
    expect(loadChapterCalls()[2][0].chapterIndex).toBe(2);
  });

  it("aborts a superseded navigation and loads only the pending chapter", async () => {
    // Chapter 1 fetches hang until aborted (prefetch joins the same request).
    api.fetchChapter.mockImplementation(
      (_id: string, i: number, _a?: number, signal?: AbortSignal) => {
        if (i !== 1) return Promise.resolve(chapter(i));
        return new Promise((resolve, reject) => {
          if (signal?.aborted) {
            reject(new DOMException("Aborted", "AbortError"));
            return;
          }
          signal?.addEventListener(
            "abort",
            () => reject(new DOMException("Aborted", "AbortError")),
            { once: true },
          );
        });
      },
    );
    await bootReader();
    frameHandler("onkey")(key("ArrowRight")); // loadChapter(1) hangs in fetch
    await settle();
    // A second navigation while the load is in flight: goNext would target
    // currentChapter()+1 — still 1 — so jump chapters via the TOC instead.
    frameHandler("onkey")(key("t"));
    await vi.waitFor(() => expect(tocPanelLatest).not.toBeNull());
    if (!tocPanelLatest) throw new Error("toc panel did not open");
    tocPanelLatest.onnavigate("ch2.xhtml"); // queues pendingNav for chapter 2
    await vi.waitFor(() =>
      expect(loadChapterCalls().some((c) => c[0].chapterIndex === 2)).toBe(
        true,
      ),
    );
    // The superseded chapter never reaches the frame…
    expect(loadChapterCalls().some((c) => c[0].chapterIndex === 1)).toBe(false);
    // …and its hung prefetch was joined, not duplicated.
    expect(api.fetchChapter.mock.calls.filter((c) => c[1] === 1).length).toBe(
      1,
    );
  });

  it("serves a revisited chapter from the LRU cache without refetching", async () => {
    await bootReader();
    frameHandler("onkey")(key("ArrowRight"));
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(2));
    frameHandler("onkey")(key("ArrowRight"));
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(3));
    const fetchesFor2 = api.fetchChapter.mock.calls.filter(
      (c) => c[1] === 2,
    ).length;
    frameHandler("onkey")(key("ArrowLeft")); // back to chapter 1: cache hit
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(4));
    expect(loadChapterCalls()[3][0].chapterIndex).toBe(1);
    expect(api.fetchChapter.mock.calls.filter((c) => c[1] === 1).length).toBe(
      1,
    );
    expect(fetchesFor2).toBe(1);
  });

  it("evicts the oldest chapter past the LRU cap — a TOC jump back refetches", async () => {
    await bootReader();
    for (let n = 1; n <= 4; n++) {
      frameHandler("onkey")(key("ArrowRight"));
      await vi.waitFor(() => expect(loadChapterCalls().length).toBe(n + 1));
    }
    expect(api.fetchChapter.mock.calls.filter((c) => c[1] === 0).length).toBe(
      1,
    );
    frameHandler("onkey")(key("t"));
    await vi.waitFor(() => expect(tocPanelLatest).not.toBeNull());
    if (!tocPanelLatest) throw new Error("toc panel did not open");
    tocPanelLatest.onnavigate("ch0.xhtml");
    await vi.waitFor(() =>
      expect(api.fetchChapter.mock.calls.filter((c) => c[1] === 0).length).toBe(
        2,
      ),
    );
  });
});

describe("Read keyboard", () => {
  it("ArrowRight loads the next chapter in scroll mode", async () => {
    await bootReader();
    frameHandler("onkey")(key("ArrowRight"));
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(2));
    expect(loadChapterCalls()[1][0].chapterIndex).toBe(1);
  });

  it("maps the frame-suppressed paged keys: Space/PageDown forward, Shift+Space/PageUp back", async () => {
    settings.update({ displayMode: "paged" });
    await bootReader();
    frameHandler("onkey")(key(" "));
    await settle();
    expect(frame.api.nextPage).toHaveBeenCalledTimes(1);
    frameHandler("onkey")(key("PageDown"));
    await settle();
    expect(frame.api.nextPage).toHaveBeenCalledTimes(2);
    frameHandler("onkey")(key(" ", true));
    await settle();
    expect(frame.api.prevPage).toHaveBeenCalledTimes(1);
    frameHandler("onkey")(key("PageUp"));
    await settle();
    expect(frame.api.prevPage).toHaveBeenCalledTimes(2);
    // …and none of them touched the chapter loader.
    expect(loadChapterCalls().length).toBe(1);
  });

  it("mirrors arrows in RTL paged mode (ArrowLeft is forward)", async () => {
    api.fetchChapter.mockImplementation((_: string, i: number) =>
      Promise.resolve({ ...chapter(i), direction: "rtl" }),
    );
    settings.update({ displayMode: "paged" });
    await bootReader();
    frameHandler("onkey")(key("ArrowLeft"));
    await settle();
    expect(frame.api.nextPage).toHaveBeenCalledTimes(1);
    frameHandler("onkey")(key("ArrowRight"));
    await settle();
    expect(frame.api.prevPage).toHaveBeenCalledTimes(1);
  });
});

describe("Read keyboard: forwarded modifier facts", () => {
  // The ui store is the real singleton in this file (not mocked): the palette
  // and shortcuts branches are asserted on its state, and each test closes
  // what it opened so later keyboard tests see a clean store.
  beforeEach(() => {
    ui.closeOverlays();
  });
  afterEach(() => {
    ui.closeOverlays();
  });

  it("leaves the palette closed for a frame-forwarded ctrl+alt+k (AltGr)", async () => {
    await bootReader();
    frameHandler("onkey")(key("k", false, { ctrlKey: true, altKey: true }));
    await settle();
    expect(ui.palette).toBe(false);
  });

  it("opens the palette for a frame-forwarded ctrl+k", async () => {
    await bootReader();
    frameHandler("onkey")(key("k", false, { ctrlKey: true }));
    await settle();
    expect(ui.palette).toBe(true);
  });

  it("keeps the alt-tolerant ? shortcut: alt+? opens the shortcuts sheet", async () => {
    await bootReader();
    frameHandler("onkey")(key("?", false, { altKey: true }));
    await settle();
    expect(ui.shortcuts).toBe(true);
  });
});

describe("Read progress", () => {
  it("forced-flushes the latest position on back and navigates to the library", async () => {
    await bootReader();
    frameHandler("onposition")(0, 0.3, undefined);
    await settle();
    frameHandler("onkey")(key("Escape")); // nothing open -> handleBack
    await vi.waitFor(() => expect(api.saveProgress).toHaveBeenCalledTimes(1));
    expect(api.saveProgress).toHaveBeenCalledWith("book1", {
      chapter: 0,
      percent: 0.3,
      cfi: undefined,
    });
    expect(navigate).toHaveBeenCalledWith("/");
  });

  it("beacons and writes the crash-guard cache on page hide, without a save", async () => {
    await bootReader();
    frameHandler("onposition")(0, 0.3, undefined);
    await settle();
    const hidden = () => {
      Object.defineProperty(document, "visibilityState", {
        value: "hidden",
        configurable: true,
      });
      document.dispatchEvent(new Event("visibilitychange"));
    };
    hidden();
    await vi.waitFor(() => expect(api.beaconProgress).toHaveBeenCalledTimes(1));
    expect(api.beaconProgress).toHaveBeenCalledWith("book1", {
      chapter: 0,
      percent: 0.3,
      cfi: undefined,
    });
    // The hide path beacons + caches; it never writes through saveProgress.
    expect(api.saveProgress).not.toHaveBeenCalled();
    const cached = localStorage.getItem("sayumi:progress::book1");
    expect(cached).not.toBeNull();
    expect(JSON.parse(cached ?? "")).toEqual({ chapter: 0, percent: 0.3 });
    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });

  it("writes the crash-guard cache and beacons on unmount", async () => {
    await bootReader();
    frameHandler("onposition")(0, 0.5, undefined);
    await settle();
    const d = dispose;
    dispose = null; // prevent the afterEach double-dispose
    if (!d) throw new Error("dispose missing");
    d();
    await settle();
    expect(api.beaconProgress).toHaveBeenCalledTimes(1);
    const cached = localStorage.getItem("sayumi:progress::book1");
    expect(cached).not.toBeNull();
    expect(JSON.parse(cached ?? "")).toEqual({ chapter: 0, percent: 0.5 });
  });

  // The scheduled (non-forced) flush is the only path that consults the
  // dedupe -- a forced flush skips it outright -- and it sits 15s behind a
  // position report. Without fake timers the anchor half of the dedupe key is
  // unreachable from this suite, so these two opt in for the duration.
  const SAVE_TICK_MS = 20_000;
  async function tick(): Promise<void> {
    await vi.advanceTimersByTimeAsync(SAVE_TICK_MS);
    await settle();
  }

  it("re-saves when only the anchor moved, and not when it did not", async () => {
    vi.useFakeTimers();
    try {
      await bootReader();
      frameHandler("onposition")(0, 0.3, "cfi:a");
      await tick();
      expect(api.saveProgress).toHaveBeenCalledTimes(1);
      expect(api.saveProgress).toHaveBeenLastCalledWith("book1", {
        chapter: 0,
        percent: 0.3,
        cfi: "cfi:a",
      });

      // Identical chapter, percent and anchor: still a duplicate.
      frameHandler("onposition")(0, 0.3, "cfi:a");
      await tick();
      expect(api.saveProgress).toHaveBeenCalledTimes(1);

      // Same chapter and percent, anchor moved. Paged percent is quantized to
      // page/(totalPages-1), so an anchor-preserving relayout can hold the
      // ratio while the anchoring block changes -- that write has to land, or
      // the next boot restores from a CFI that no longer matches the page.
      frameHandler("onposition")(0, 0.3, "cfi:b");
      await tick();
      expect(api.saveProgress).toHaveBeenCalledTimes(2);
      expect(api.saveProgress).toHaveBeenLastCalledWith("book1", {
        chapter: 0,
        percent: 0.3,
        cfi: "cfi:b",
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it("still persists the origin after a failed progress fetch", async () => {
    api.getProgress.mockRejectedValue(new Error("nope"));
    vi.useFakeTimers();
    try {
      await bootReader();
      // The boot seed never ran, so the sentinel is what keeps the origin
      // distinguishable from a real position at chapter 0, percent 0.
      frameHandler("onposition")(0, 0, undefined);
      await tick();
      expect(api.saveProgress).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not re-save the anchor it just booted from", async () => {
    api.getProgress.mockResolvedValue({
      chapter: 1,
      percent: 0.5,
      cfi: "cfi:boot",
    });
    vi.useFakeTimers();
    try {
      await bootReader();
      frameHandler("onposition")(1, 0.5, "cfi:boot");
      await tick();
      expect(api.saveProgress).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("Read search highlights", () => {
  async function openSearch(): Promise<void> {
    frameHandler("onkey")(key("f")); // f = search (s is settings)
    await vi.waitFor(() => expect(searchPanelLatest).not.toBeNull());
  }

  it("toasts on a stale/invalid result and never navigates", async () => {
    await bootReader();
    await openSearch();
    if (!searchPanelLatest) throw new Error("search panel missing");
    searchPanelLatest.onresultclick(
      { chapterIndex: "oops", charOffset: 0, matchLen: 1, snippet: "" },
      "q",
    );
    await settle();
    expect(showToast).toHaveBeenCalledWith(
      "Search result is no longer available",
    );
    expect(loadChapterCalls().length).toBe(1);
    expect(frame.api.highlightSearch).not.toHaveBeenCalled();
  });

  it("highlights a same-chapter result immediately, seq-less", async () => {
    await bootReader();
    await openSearch();
    if (!searchPanelLatest) throw new Error("search panel missing");
    searchPanelLatest.onresultclick(
      { chapterIndex: 0, charOffset: 10, matchLen: 5, snippet: "" },
      "q",
    );
    await settle();
    expect(frame.api.highlightSearch).toHaveBeenCalledWith(10, 5, "q");
    expect(loadChapterCalls().length).toBe(1);
  });

  it("defers a cross-chapter highlight and stamps it with the loaded seq", async () => {
    await bootReader();
    await openSearch();
    if (!searchPanelLatest) throw new Error("search panel missing");
    searchPanelLatest.onresultclick(
      { chapterIndex: 2, charOffset: 10, matchLen: 5, snippet: "" },
      "q",
    );
    await vi.waitFor(() => expect(loadChapterCalls().length).toBe(2));
    expect(loadChapterCalls()[1][0].chapterIndex).toBe(2);
    // Nothing highlighted before the frame reports the chapter loaded.
    expect(frame.api.highlightSearch).not.toHaveBeenCalled();
    frameHandler("onloaded")(9);
    await vi.waitFor(() =>
      expect(frame.api.highlightSearch).toHaveBeenCalledWith(10, 5, "q", 9),
    );
  });
});

describe("Read bookmarks", () => {
  it("toggle-adds a bookmark at the current position and toasts", async () => {
    api.createBookmark.mockResolvedValue(bm("b1", 0, 0));
    await bootReader();
    frameHandler("onkey")(key("b"));
    await vi.waitFor(() => expect(api.createBookmark).toHaveBeenCalledTimes(1));
    expect(api.createBookmark).toHaveBeenCalledWith("book1", {
      chapter: 0,
      percent: 0,
      cfi: undefined,
    });
    expect(showToast).toHaveBeenCalledWith("Bookmark added");
  });

  it("queues a second toggle during the add's flight and resolves it as a delete", async () => {
    let resolveCreate: (b: ApiClient.Bookmark) => void = () => {};
    api.createBookmark.mockImplementation(
      () =>
        new Promise((r) => {
          resolveCreate = r;
        }),
    );
    api.deleteBookmark.mockResolvedValue(undefined);
    await bootReader();
    frameHandler("onkey")(key("b")); // add in flight
    await settle();
    frameHandler("onkey")(key("b")); // queued, not a second create
    await settle();
    expect(api.createBookmark).toHaveBeenCalledTimes(1);
    resolveCreate(bm("b1", 0, 0));
    await vi.waitFor(() => expect(api.deleteBookmark).toHaveBeenCalledTimes(1));
    expect(api.deleteBookmark).toHaveBeenCalledWith("book1", "b1");
    expect(showToast).toHaveBeenCalledWith("Bookmark removed");
  });

  it("re-adds only the removed bookmark when a delete fails", async () => {
    api.getBookmarks.mockResolvedValue([bm("a"), bm("b", 1, 0.4)]);
    api.deleteBookmark.mockRejectedValue(new Error("down"));
    await bootReader();
    frameHandler("onkey")(key("B")); // Shift+B opens the bookmarks panel
    await vi.waitFor(() => expect(bookmarksPanelLatest).not.toBeNull());
    if (!bookmarksPanelLatest) throw new Error("bookmarks panel missing");
    bookmarksPanelLatest.ondelete("a");
    await vi.waitFor(() => expect(showToast).toHaveBeenCalled());
    flush();
    const ids = bookmarksPanelLatest.bookmarks.map((b) => b.id);
    expect(ids).toContain("a");
    expect(ids).toContain("b");
  });
});
