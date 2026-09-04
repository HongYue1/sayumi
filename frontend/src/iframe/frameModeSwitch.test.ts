// Mode-switch position transfer. The parent changes modes with a bare
// apply-settings (no reload), so the frame must carry the position across
// the layout swap itself: entering paged mode from the scroll anchor, and
// returning to scroll from the paged anchor. Before the fix the paged side
// restarted from the stale currentPage (0 after the load reset) and the
// scroll side kept its pre-paged offset; the report that followed each
// switch then overwrote the parent's good position, and the next save
// persisted chapter-start zealously enough to look like "progress is never
// saved in paged modes".
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

function rect(left: number, right: number): DOMRect {
  return {
    left,
    right,
    top: 0,
    bottom: 100,
    width: right - left,
    height: 100,
    x: left,
    y: 0,
    toJSON: () => {},
  } as DOMRect;
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
  document.head.innerHTML = "";
  document.body.innerHTML = "";
  Reflect.deleteProperty(document, "fonts");
});

it("carries the position across scroll<->paged switches", async () => {
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

  // Four 800px pages once the multicol shell is up. happy-dom reports 0 for
  // both metrics, so the geometry is stubbed per element.
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
  content.getBoundingClientRect = () => rect(0, 800);

  const positions = (): Array<
    Extract<FrameToParentMessage, { type: "position" }>
  > =>
    sent.filter(
      (m): m is Extract<FrameToParentMessage, { type: "position" }> =>
        m.type === "position",
    );

  await import("./frame");
  try {
    incoming({ type: "set-font-faces", fontFaces: "" });
    const load: LoadMessage = {
      type: "load",
      seq: 1,
      chapterIndex: 0,
      css: "",
      fontFaceCSS: "",
      direction: "ltr",
      writingMode: "horizontal-tb",
      html: '<p id="p1">one</p><p id="anchor">two</p><p id="p3">three</p>',
      resourceBase: null,
      scrollTo: "top",
      fragment: null,
      hasNext: true,
      hasPrev: false,
      restorePercent: null,
      restoreCfi: null,
      origin: parentOrigin,
      settings: iframeSettings("scroll"),
    };
    incoming(load);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded")).toEqual([
      { type: "loaded", seq: 1 },
    ]);

    const anchor = document.getElementById("anchor");
    if (!anchor) throw new Error("anchor fixture missing");
    // Logical x 1700 with scrollLeft 0 sits on page 2 of 4 in both layouts:
    // the scroll probe reads it as the first visible block, the paged
    // paginator maps it back to page 2.
    anchor.getBoundingClientRect = () => rect(1700, 1900);
    const fromPoint = vi
      .spyOn(document, "elementFromPoint")
      .mockImplementation(() => anchor);
    void fromPoint;
    sent.length = 0;

    // scroll -> paged: the view must open on the anchor's page, and the
    // report that follows must carry its converted percent — not 0 from the
    // load-reset currentPage.
    incoming({ type: "apply-settings", settings: iframeSettings("paged") });
    expect(content.scrollLeft).toBe(1600);
    expect(positions()).toEqual([
      {
        type: "position",
        seq: 1,
        chapterIndex: 0,
        percent: 2 / 3,
        cfi: expect.stringMatching(/^cfi:/),
      },
    ]);
    expect(
      sent.filter((m) => m.type === "effective-mode" && m.mode === "paged"),
    ).toHaveLength(1);

    // One page turn in paged mode, then back to scroll: the scroll view must
    // return to the anchor, not to the pre-paged offset at the top.
    incoming({ type: "next-page", seq: 1 });
    expect(positions().at(-1)?.percent).toBe(1);
    sent.length = 0;
    const scrollIntoView = vi
      .spyOn(anchor, "scrollIntoView")
      .mockImplementation(() => {});
    incoming({ type: "apply-settings", settings: iframeSettings("scroll") });
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "instant" });
    const back = positions();
    expect(back).toHaveLength(1);
    expect(back[0]?.cfi).toEqual(expect.stringMatching(/^cfi:/));
  } finally {
    incoming({ type: "destroy" });
  }
});
