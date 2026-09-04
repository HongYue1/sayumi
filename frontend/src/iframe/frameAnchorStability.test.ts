// Anchor robustness: the shell must never anchor, the memo must yield to a
// moved caret, stored shell CFIs must fall back to percent, and continuous
// scrolling must force periodic reports. Like the other frame suites, this
// file is a single integration arm against one engine instance.
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

const CHAPTER_HTML = `<p id="a">${"x".repeat(50)}</p><p id="b">${"y".repeat(50)}</p>`;

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

it("never anchors the shell, yields the memo, and reports mid-run", async () => {
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
    loadChapter(1, "scroll", { restorePercent: null, restoreCfi: null });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded")).toHaveLength(1);

    const blockA = document.getElementById("a");
    const blockB = document.getElementById("b");
    const inner = document.getElementById("content-inner");
    if (!blockA || !blockB || !inner) throw new Error("fixture missing");
    blockA.getBoundingClientRect = () => rect(0, 0, 700, 100);
    blockB.getBoundingClientRect = () => rect(0, 0, 700, 100);

    // Phase 1 — shell rejection: the first probes hit #content-inner itself
    // (its paged padding, or a margin gap), which must not anchor. Pre-fix
    // this memoized cfi:1/1/1 and every later report carried it.
    let probeCalls = 0;
    const fromPoint = vi
      .spyOn(document, "elementFromPoint")
      .mockImplementation(() => (++probeCalls <= 2 ? inner : blockB));
    sent.length = 0;
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(500);
    const shelled = positions(sent);
    expect(shelled.length).toBeGreaterThan(0);
    // body > paged-clip(1) > content(1) > content-inner(1) > p#b(2).
    expect(shelled.at(-1)?.cfi).toBe("cfi:1/1/1/2");
    fromPoint.mockRestore();

    // Phase 2 — memo yields to a moved caret: the memo holds block B, but
    // the caret now sits in block A. Returning the memo would pair a fresh
    // percent with a stale block.
    const textA = blockA.firstChild as Text;
    Object.assign(document, {
      caretRangeFromPoint: () => {
        const range = document.createRange();
        range.setStart(textA, 10);
        range.collapse(true);
        return range;
      },
    });
    sent.length = 0;
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(500);
    expect(positions(sent).at(-1)?.cfi).toBe("cfi:1/1/1/1:10");
    Reflect.deleteProperty(document, "caretRangeFromPoint");

    // Phase 3 — stored shell CFI falls back to percent in scroll mode.
    // Tall chapter so the percent lands mid-way, not at an edge.
    Object.defineProperty(document.documentElement, "scrollHeight", {
      value: 1768,
      configurable: true,
    });
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    sent.length = 0;
    loadChapter(2, "scroll", {
      restorePercent: 0.5,
      restoreCfi: "cfi:1/1/1",
    });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded" && m.seq === 2)).toHaveLength(
      1,
    );
    // Pre-fix the inner element resolved and scrollIntoView'd the chapter
    // top; now the percent path drives: max(1768-768) * 0.5.
    expect(scrollTo.mock.calls.at(-1)?.[0]).toMatchObject({ top: 500 });

    // Phase 4 — stored shell CFI falls back to percent in paged mode.
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
    sent.length = 0;
    loadChapter(3, "paged", {
      restorePercent: 0.42,
      restoreCfi: "cfi:1/1/1",
    });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded" && m.seq === 3)).toHaveLength(
      1,
    );
    // pageForRatio(0.42, 4) rounds to page 1 — not the page-0 the resolved
    // inner element mapped to pre-fix.
    expect(content.scrollLeft).toBe(800);
    expect(positions(sent).at(-1)?.percent).toBeCloseTo(1 / 3, 10);

    // Phase 5 — a scroll run that never pauses still reports mid-run.
    sent.length = 0;
    loadChapter(4, "scroll", { restorePercent: null, restoreCfi: null });
    await vi.advanceTimersByTimeAsync(1_000);
    sent.length = 0;
    for (let i = 0; i < 12; i++) {
      window.dispatchEvent(new Event("scroll"));
      await vi.advanceTimersByTimeAsync(100);
    }
    // Leading + forced mid-run (+ trailing not yet fired): pre-fix only the
    // leading report exists until the run ends.
    expect(positions(sent).length).toBeGreaterThanOrEqual(3);
  } finally {
    incoming({ type: "destroy" });
  }
});
