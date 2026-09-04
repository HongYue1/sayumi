// Text-offset anchors end to end. The frame engine is a self-starting IIFE,
// so (like frame.test.ts) this file is a single integration arm: sequential
// loads drive the capture, scroll restore, and paged restore phases against
// one engine instance. happy-dom has no caret APIs (exercising the element
// fallback) and no layout (geometry and range rects are stubbed), so the
// caret probe and the measuring reads are stubbed where the new behavior
// lives.
import { afterEach, expect, it, vi } from "vitest";
import type {
  FrameToParentMessage,
  IframeSettings,
  LoadMessage,
} from "~/lib/frameMessages";

const parentOrigin = "https://parent.example";

function incoming(data: unknown): void {
  const event = new MessageEvent("message", { data, origin: parentOrigin });
  Object.defineProperty(event, "source", { value: window.parent });
  window.dispatchEvent(event);
}

function iframeSettings(mode: IframeSettings["mode"]): IframeSettings {
  return {
    mode,
    fontSize: 30,
    fontFamily: "serif",
    preserveBookStyles: false,
    preserveBookFonts: false,
    lineHeight: null,
    paragraphSpacing: null,
    textIndent: null,
    letterSpacing: null,
    contentWidth: null,
    margins: { top: 24, bottom: 24, side: 40 },
    justify: false,
    hyphenation: false,
    theme: "light",
    themeVars: null,
    chapterTitleAlign: null,
    chapterTitleSize: null,
    chapterTitleSpacing: null,
    chapterTitleFontFamily: null,
    headingLetterSpacing: null,
    headerSizesEnabled: false,
    h1Size: null,
    h2Size: null,
    h3Size: null,
    h4Size: null,
    h5Size: null,
    h6Size: null,
    headerWeight: null,
    textWeight: null,
  };
}

function rect(
  left: number,
  top: number,
  right: number,
  bottom: number,
): DOMRect {
  return {
    left,
    right,
    top,
    bottom,
    width: right - left,
    height: bottom - top,
    x: left,
    y: top,
    toJSON: () => {},
  } as DOMRect;
}

const CHAPTER_HTML = `<p id="long">${"x".repeat(100)}</p>`;

function loadChapter(
  seq: number,
  mode: IframeSettings["mode"],
  restore: { restorePercent: number | null; restoreCfi: string | null },
): void {
  incoming({
    type: "load",
    seq,
    chapterIndex: 0,
    css: "",
    fontFaceCSS: "",
    direction: "ltr",
    writingMode: "horizontal-tb",
    html: CHAPTER_HTML,
    resourceBase: null,
    scrollTo: "top",
    fragment: null,
    hasNext: false,
    hasPrev: false,
    restorePercent: restore.restorePercent,
    restoreCfi: restore.restoreCfi,
    origin: parentOrigin,
    settings: iframeSettings(mode),
  } satisfies LoadMessage);
}

function positions(
  sent: FrameToParentMessage[],
): Array<Extract<FrameToParentMessage, { type: "position" }>> {
  return sent.filter(
    (m): m is Extract<FrameToParentMessage, { type: "position" }> =>
      m.type === "position",
  );
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  Reflect.deleteProperty(document, "caretRangeFromPoint");
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
  document.head.innerHTML = "";
  document.body.innerHTML = "";
  Reflect.deleteProperty(document, "fonts");
});

it("captures offsets and restores the offset point in both modes", async () => {
  vi.useFakeTimers();
  document.head.innerHTML = `
    <style id="font-face-css"></style>
    <style id="book-css"></style>
    <style id="override-css"></style>`;
  document.body.innerHTML =
    '<div id="paged-clip"><div id="content"><div id="content-inner"></div></div></div>';
  Object.defineProperty(document, "fonts", {
    configurable: true,
    value: { ready: Promise.resolve() },
  });
  const sent: FrameToParentMessage[] = [];
  vi.spyOn(window, "postMessage").mockImplementation((message) => {
    sent.push(message as FrameToParentMessage);
  });

  await import("./frame");
  try {
    incoming({ type: "set-font-faces", fontFaces: "" });

    // Phase 1 — capture: a scroll report carries the caret-measured offset.
    loadChapter(1, "scroll", { restorePercent: null, restoreCfi: null });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded")).toHaveLength(1);

    const long = document.getElementById("long");
    if (!long) throw new Error("chapter fixture missing");
    const text = long.firstChild as Text;
    long.getBoundingClientRect = () => rect(0, 0, 700, 100);
    // The caret probe is the only offset source: without this stub the
    // report degrades to the element path.
    Object.assign(document, {
      caretRangeFromPoint: () => {
        const range = document.createRange();
        range.setStart(text, 40);
        range.collapse(true);
        return range;
      },
    });
    sent.length = 0;
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(500);
    const reported = positions(sent);
    expect(reported.length).toBeGreaterThan(0);
    // body > paged-clip(1) > content(1) > content-inner(1) > p(1), offset 40.
    expect(reported.at(-1)?.cfi).toBe("cfi:1/1/1/1:40");
    Reflect.deleteProperty(document, "caretRangeFromPoint");

    // Phase 2 — scroll restore lands the offset point, not the block top.
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    // Installed before load: any element scrollIntoView during the restore
    // means the offset path lost to the element path.
    const scrollIntoView = vi
      .spyOn(Element.prototype, "scrollIntoView")
      .mockImplementation(() => {});
    // The offset resolves mid-block; the measuring read is stubbed because
    // happy-dom has no layout.
    const rangeRect = vi
      .spyOn(Range.prototype, "getBoundingClientRect")
      .mockReturnValue(rect(0, 500, 700, 500));
    loadChapter(2, "scroll", {
      restorePercent: null,
      restoreCfi: "cfi:1/1/1/1:70",
    });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded" && m.seq === 2)).toHaveLength(
      1,
    );
    // scrollTarget "top" scrolls to 0 first; the offset restore then lands
    // the measured point.
    const tops = scrollTo.mock.calls.map((call) => call[0]);
    expect(tops.at(-1)).toMatchObject({ top: 500 });
    expect(scrollIntoView).not.toHaveBeenCalled();

    // Phase 3 — paged restore lands the offset's page. Four 800px pages;
    // happy-dom reports 0 for both metrics, so the geometry is stubbed.
    const content = document.getElementById("content");
    if (!content) throw new Error("shell fixture missing");
    Object.defineProperty(content, "clientWidth", {
      value: 800,
      configurable: true,
    });
    Object.defineProperty(content, "scrollWidth", {
      value: 3200,
      configurable: true,
    });
    content.getBoundingClientRect = () => rect(0, 0, 800, 600);
    // The anchor block spans pages (union rect from its start); the offset
    // rect sits on page 2. Landing page 2 proves the range won over the
    // element, which maps by its start.
    const pagedLong = document.getElementById("content-inner")
      ?.firstElementChild as HTMLElement | null;
    if (pagedLong)
      pagedLong.getBoundingClientRect = () => rect(0, 0, 3200, 600);
    rangeRect.mockReturnValue(rect(1700, 0, 1750, 100));
    scrollIntoView.mockClear();
    sent.length = 0;
    loadChapter(3, "paged", {
      restorePercent: 0,
      restoreCfi: "cfi:1/1/1/1:70",
    });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded" && m.seq === 3)).toHaveLength(
      1,
    );
    expect(content.scrollLeft).toBe(1600);
    expect(positions(sent).at(-1)?.percent).toBe(2 / 3);
    expect(scrollIntoView).not.toHaveBeenCalled();
  } finally {
    incoming({ type: "destroy" });
  }
});
