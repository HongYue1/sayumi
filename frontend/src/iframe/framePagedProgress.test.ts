// Exercise the real message-driven frame with scroll-relative geometry.
// Happy-dom has no layout; the probes/rects below move with scrollLeft so a
// stale block or a CFI/percent mismatch cannot accidentally pass the test.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  FrameToParentMessage,
  IframeSettings,
  LoadMessage,
} from "~/lib/frameMessages";

const parentOrigin = "https://parent.example";
type Position = Extract<FrameToParentMessage, { type: "position" }>;

function incoming(data: unknown): void {
  const event = new MessageEvent("message", { data, origin: parentOrigin });
  Object.defineProperty(event, "source", { value: window.parent });
  window.dispatchEvent(event);
}

function settings(mode: IframeSettings["mode"]): IframeSettings {
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

const layouts = [
  { mode: "paged", rtl: false },
  { mode: "paged", rtl: true },
  { mode: "paged-two", rtl: false },
  { mode: "paged-two", rtl: true },
] as const;

describe.each(layouts)("paged reopen: $mode, rtl=$rtl", ({ mode, rtl }) => {
  let content: HTMLElement;
  let sent: FrameToParentMessage[];
  let width: number;
  let stride: number;
  let seq: number;
  let gutter: boolean;
  let spanning: boolean;
  let probes: Array<[number, number]>;

  function lastPosition(): Position {
    const position = sent.findLast((message) => message.type === "position");
    if (!position || position.type !== "position")
      throw new Error("position missing");
    return position;
  }

  function displayedPage(): number {
    return Math.round(Math.abs(content.scrollLeft) / stride);
  }

  async function reopen(
    position: Pick<Position, "percent" | "cfi">,
  ): Promise<void> {
    seq++;
    incoming({
      type: "load",
      seq,
      chapterIndex: 0,
      css: "",
      fontFaceCSS: "",
      direction: rtl ? "rtl" : "ltr",
      writingMode: "horizontal-tb",
      html: Array.from(
        { length: 4 },
        (_, i) => `<p id="p${i}">${"text ".repeat(20)}</p>`,
      ).join(""),
      resourceBase: null,
      scrollTo: "top",
      fragment: null,
      hasNext: false,
      hasPrev: false,
      restorePercent: position.percent,
      restoreCfi: position.cfi ?? null,
      origin: parentOrigin,
      settings: settings(mode),
    } satisfies LoadMessage);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(
      sent.some((message) => message.type === "loaded" && message.seq === seq),
    ).toBe(true);
  }

  beforeEach(async () => {
    vi.resetModules();
    vi.useFakeTimers();
    document.head.innerHTML =
      '<style id="font-face-css"></style><style id="book-css"></style><style id="override-css"></style>';
    document.body.innerHTML =
      '<div id="paged-clip"><div id="content"><div id="content-inner"></div></div></div>';
    content = document.getElementById("content")!;
    width = window.innerWidth;
    stride = width + (mode === "paged-two" ? 1 : 0);
    content.style.columnGap = mode === "paged-two" ? "1px" : "0px";
    content.style.columnCount = mode === "paged-two" ? "2" : "1";
    Object.defineProperties(content, {
      clientWidth: { value: width, configurable: true },
      scrollWidth: { value: width + stride * 3, configurable: true },
    });
    Object.defineProperty(document, "fonts", {
      configurable: true,
      value: { ready: Promise.resolve() },
    });
    sent = [];
    seq = 0;
    gutter = false;
    spanning = false;
    probes = [];
    vi.spyOn(window, "postMessage").mockImplementation((message) => {
      sent.push(message as FrameToParentMessage);
    });
    vi.spyOn(window, "matchMedia").mockReturnValue({
      matches: false,
    } as MediaQueryList);
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      function (this: HTMLElement) {
        if (this.id === "content" || this.id === "paged-clip")
          return new DOMRect(0, 24, width, window.innerHeight - 56);
        const match = /^p(\d)$/.exec(this.id);
        if (!match) return new DOMRect();
        const logicalX = Number(match[1]) * stride + 48;
        const blockWidth = spanning ? stride * 4 : width / 3;
        const scroll = Math.abs(content.scrollLeft);
        const left = rtl
          ? width - logicalX + scroll - blockWidth
          : logicalX - scroll;
        return new DOMRect(left, 48, blockWidth, 48);
      },
    );
    vi.spyOn(document, "elementFromPoint").mockImplementation((x, y) => {
      probes.push([x, y]);
      if (y < 24 || (gutter && Math.abs(x - width / 2) < 12))
        return document.getElementById("content-inner");
      return document.getElementById(`p${spanning ? 0 : displayedPage()}`);
    });
    await import("./frame");
    incoming({ type: "set-font-faces", fontFaces: "" });
  });

  afterEach(() => {
    incoming({ type: "destroy" });
    expect(vi.getTimerCount()).toBe(0);
    vi.useRealTimers();
    vi.restoreAllMocks();
    Reflect.deleteProperty(document, "caretRangeFromPoint");
    document.documentElement.className = "";
    document.documentElement.removeAttribute("style");
    document.head.innerHTML = "";
    document.body.innerHTML = "";
    Reflect.deleteProperty(document, "fonts");
  });

  it.each(["missing", "null"])(
    "saves page 3 after reopening page 2 when the caret probe is %s",
    async (caret) => {
      if (caret === "null")
        Object.assign(document, { caretRangeFromPoint: () => null });
      await reopen({ percent: 1 / 3, cfi: "" });
      expect(displayedPage()).toBe(1);
      incoming({ type: "get-position" });
      await reopen(lastPosition());
      expect(displayedPage()).toBe(1);

      incoming({ type: "next-page", seq });
      await vi.advanceTimersByTimeAsync(500);
      expect(displayedPage()).toBe(2);
      // This is the fresh-position request Read sends before saving on Escape.
      incoming({ type: "get-position" });
      const saved = lastPosition();
      expect(saved.percent).toBe(2 / 3);
      expect(saved.cfi).toBe("cfi:1/1/1/3");
      await reopen(saved);
      expect(displayedPage()).toBe(2);

      incoming({ type: "prev-page", seq });
      await vi.advanceTimersByTimeAsync(500);
      incoming({ type: "get-position" });
      await reopen(lastPosition());
      expect(displayedPage()).toBe(1);
    },
  );

  it("uses percent when an unmeasurable block starts on an earlier page", async () => {
    spanning = true;
    await reopen({ percent: 2 / 3, cfi: "" });
    incoming({ type: "get-position" });
    expect(lastPosition()).toMatchObject({ percent: 2 / 3, cfi: "" });
    await reopen(lastPosition());
    expect(displayedPage()).toBe(2);
  });

  it("probes inside the reading column rather than the spread gutter", async () => {
    gutter = mode === "paged-two";
    await reopen({ percent: 1 / 3, cfi: "" });
    incoming({ type: "get-position" });
    expect(lastPosition().cfi).toBe("cfi:1/1/1/2");
    if (gutter) {
      expect(
        probes.some(
          ([x, y]) => y >= 24 && (rtl ? x > width / 2 : x < width / 2),
        ),
      ).toBe(true);
    }
  });
});
