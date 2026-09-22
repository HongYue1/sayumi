// Direct frame-engine contract test. frame.ts is a self-starting IIFE, so the
// shell is installed before a dynamic import and the module is destroyed once
// this single integration arm has exercised the load/settings wire.
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

function press(target: Element, init: KeyboardEventInit): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    bubbles: true,
    cancelable: true,
    ...init,
  });
  target.dispatchEvent(event);
  return event;
}

function click(target: Element): MouseEvent {
  const event = new MouseEvent("click", {
    bubbles: true,
    cancelable: true,
  });
  target.dispatchEvent(event);
  return event;
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
    contentWidth: 65,
    margins: { top: 48, bottom: 48, side: 48 },
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

const verticalLoad: LoadMessage = {
  type: "load",
  seq: 1,
  chapterIndex: 0,
  css: "",
  fontFaceCSS: "",
  direction: "ltr",
  writingMode: "vertical-rl",
  html: "<p>vertical</p>",
  resourceBase: null,
  scrollTo: "top",
  fragment: null,
  hasNext: true,
  hasPrev: false,
  restorePercent: null,
  restoreCfi: null,
  origin: parentOrigin,
  settings: iframeSettings("paged"),
};

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
  document.head.innerHTML = "";
  document.body.innerHTML = "";
  Reflect.deleteProperty(document, "fonts");
});

it("uses and reports one effective scroll mode for a vertical paged request", async () => {
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
    incoming(verticalLoad);
    await vi.advanceTimersByTimeAsync(0);
    // "loaded" means the settings-driven layout and initial restore finished,
    // not merely that innerHTML was assigned.
    expect(sent.filter((m) => m.type === "loaded")).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(sent.filter((m) => m.type === "loaded")).toEqual([
      { type: "loaded", seq: 1 },
    ]);

    const root = document.documentElement;
    const css = document.getElementById("override-css")?.textContent ?? "";
    expect(root.classList.contains("paged")).toBe(false);
    expect(root.classList.contains("vertical-rl")).toBe(true);
    expect(css).not.toContain("overflow: hidden !important");
    expect(css).toContain("max-width: 65% !important");
    expect(sent.filter((m) => m.type === "effective-mode")).toEqual([
      {
        type: "effective-mode",
        seq: 1,
        mode: "scroll",
        fallback: "vertical-writing",
      },
    ]);

    // The frame rejects future sequence commands directly as a second boundary;
    // the controller normally holds them until the matching load settles.
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    incoming({ type: "scroll-to-end", seq: 2 });
    expect(scrollTo).not.toHaveBeenCalled();
    incoming({ type: "scroll-to-end", seq: 1 });
    expect(scrollTo).toHaveBeenCalledTimes(1);
    scrollTo.mockRestore();

    // The parent must drive this chapter as scroll: paged commands are inert.
    const beforePageCommand = sent.length;
    incoming({ type: "next-page", seq: 1 });
    expect(sent).toHaveLength(beforePageCommand);

    // Changing only the request/fallback still deserves a fresh report even
    // though the effective mode remains scroll.
    incoming({ type: "apply-settings", settings: iframeSettings("scroll") });
    const modeReports = sent.filter((m) => m.type === "effective-mode");
    expect(modeReports).toHaveLength(2);
    expect(modeReports[1]).toEqual({
      type: "effective-mode",
      seq: 1,
      mode: "scroll",
      fallback: null,
    });

    // The sanitizer deliberately preserves contenteditable. Re-enter paged
    // mode with horizontal content so this arm can pin both target ownership
    // and the defaults the frame must still suppress for ordinary shortcuts.
    incoming({
      ...verticalLoad,
      seq: 2,
      writingMode: "horizontal-tb",
      // A live update racing this load must override its embedded snapshot.
      settings: iframeSettings("scroll"),
      html: '<div contenteditable="true"><span id="editor">edit</span><span contenteditable="false" id="locked">locked</span></div><details><summary id="disclosure">More</summary><p>Details</p></details><p id="reader-text">read</p><mark id="authored-search-mark" data-search-mark="book-owned">book mark</mark><a id="mail-link" href="mailto:reader@example.com">mail</a><a id="tel-link" href="tel:+123456">telephone</a><a id="web-link" href="https://example.com/read">web</a><a id="blocked-link" href="ftp://example.com/book">blocked</a><a id="script-link" href="javascript:alert(1)">script</a><span id="part 1">target</span><a id="fragment-link" href="#part%201">fragment</a><a id="book-link" href="chapter%20two.xhtml#target">book</a>',
    });
    incoming({ type: "apply-settings", settings: iframeSettings("paged") });
    await vi.advanceTimersByTimeAsync(1_000);
    expect(root.classList.contains("paged")).toBe(true);

    const editor = document.getElementById("editor");
    const locked = document.getElementById("locked");
    const disclosure = document.getElementById("disclosure");
    const readerText = document.getElementById("reader-text");
    expect(editor?.isContentEditable).toBe(true);
    expect(locked?.isContentEditable).toBe(false);
    expect(readerText).not.toBeNull();
    if (!editor || !locked || !disclosure || !readerText)
      throw new Error("keyboard fixture missing");

    // The frame owns two security-sensitive chapter-content boundaries. A
    // book cannot mint the private marker used by search, and only the narrow
    // external-scheme allow-list may escape the reader document.
    const authoredMark = document.getElementById("authored-search-mark");
    expect(authoredMark?.hasAttribute("data-search-mark")).toBe(false);

    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    sent.length = 0;
    for (const id of ["mail-link", "tel-link", "web-link"]) {
      const link = document.getElementById(id);
      if (!link) throw new Error(`link fixture missing: ${id}`);
      expect(click(link).defaultPrevented).toBe(true);
    }
    expect(open.mock.calls).toEqual([
      ["mailto:reader@example.com", "_blank", "noopener,noreferrer"],
      ["tel:+123456", "_blank", "noopener,noreferrer"],
      ["https://example.com/read", "_blank", "noopener,noreferrer"],
    ]);

    for (const id of ["blocked-link", "script-link"]) {
      const link = document.getElementById(id);
      if (!link) throw new Error(`link fixture missing: ${id}`);
      expect(click(link).defaultPrevented).toBe(true);
    }
    expect(open).toHaveBeenCalledTimes(3);
    expect(sent.filter((m) => m.type === "link-clicked")).toEqual([]);

    const fragmentLink = document.getElementById("fragment-link");
    if (!fragmentLink) throw new Error("fragment link fixture missing");
    const getById = vi.spyOn(document, "getElementById");
    expect(click(fragmentLink).defaultPrevented).toBe(true);
    expect(getById).toHaveBeenCalledWith("part 1");
    expect(sent.filter((m) => m.type === "link-clicked")).toEqual([]);
    getById.mockRestore();

    const bookLink = document.getElementById("book-link");
    if (!bookLink) throw new Error("book link fixture missing");
    expect(click(bookLink).defaultPrevented).toBe(true);
    expect(sent.filter((m) => m.type === "link-clicked")).toEqual([
      {
        type: "link-clicked",
        seq: 2,
        href: "chapter%20two.xhtml#target",
      },
    ]);

    // The listener sits on the document, so a click can arrive with a target
    // that is not an Element. Reading closest() off it threw and took the
    // region turn down with the link handling.
    sent.length = 0;
    document.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    const targetlessClicks = sent.filter((m) => m.type === "click");
    expect(targetlessClicks).toHaveLength(1);
    expect(targetlessClicks[0]).toMatchObject({ type: "click", seq: 2 });

    sent.length = 0;

    const editableChord = press(editor, {
      key: "k",
      code: "KeyK",
      ctrlKey: true,
    });
    expect(sent.filter((m) => m.type === "key")).toHaveLength(0);
    expect(editableChord.defaultPrevented).toBe(false);

    const composingEscape = press(readerText, {
      key: "Escape",
      code: "Escape",
      isComposing: true,
    });
    expect(sent.filter((m) => m.type === "key")).toHaveLength(0);
    expect(composingEscape.defaultPrevented).toBe(false);

    const disclosureSpace = press(disclosure, { key: " ", code: "Space" });
    expect(sent.filter((m) => m.type === "key")).toHaveLength(0);
    expect(disclosureSpace.defaultPrevented).toBe(false);

    // contenteditable=false opts this nested island back out. Treating every
    // descendant of an editable host as owned would suppress real shortcuts.
    const lockedChord = press(locked, {
      key: "k",
      code: "KeyK",
      ctrlKey: true,
    });
    expect(lockedChord.defaultPrevented).toBe(true);

    const altGr = press(readerText, {
      key: "k",
      code: "KeyK",
      ctrlKey: true,
      altKey: true,
    });
    expect(altGr.defaultPrevented).toBe(false);

    const pageTurn = press(readerText, {
      key: "ArrowRight",
      code: "ArrowRight",
    });
    expect(pageTurn.defaultPrevented).toBe(true);

    const ordinary = press(readerText, { key: "t", code: "KeyT" });
    expect(ordinary.defaultPrevented).toBe(false);
    expect(sent.filter((m) => m.type === "key").map((m) => m.key)).toEqual([
      "k",
      "k",
      "ArrowRight",
      "t",
    ]);

    // Releasing a text selection is not a tap: the region message stays
    // unsent so finishing a drag in an edge zone cannot turn the page.
    sent.length = 0;
    const range = document.createRange();
    range.selectNodeContents(readerText);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    readerText.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    expect(sent.filter((m) => m.type === "click")).toHaveLength(0);
    selection?.removeAllRanges();
    readerText.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    expect(sent.filter((m) => m.type === "click")).toHaveLength(1);

    // Middle-click never fires click: without an auxclick guard it would open
    // the raw chapter URL in a new tab.
    sent.length = 0;
    const middle = new MouseEvent("auxclick", {
      bubbles: true,
      cancelable: true,
      button: 1,
    });
    bookLink.dispatchEvent(middle);
    expect(middle.defaultPrevented).toBe(true);
    expect(open).toHaveBeenCalledTimes(3);
    expect(sent.filter((m) => m.type === "link-clicked")).toEqual([]);

    // A pushed font list repaints without waiting for a settings change, so a
    // rescan under the selected family shows up on its own.
    incoming({ type: "set-font-faces", fontFaces: "@font-face{}" });
    expect(document.getElementById("font-face-css")?.textContent).toBe(
      "@font-face{}",
    );

    // Paged scroll-to routes through the paginator: the scroll math below
    // cannot move a multicol scroller, so without the branch these jumps
    // (chapter-open top, CFI-less bookmarks) silently stay put.
    sent.length = 0;
    const scrollToSpy = vi
      .spyOn(window, "scrollTo")
      .mockImplementation(() => {});
    incoming({ type: "scroll-to", seq: 2, percent: 1 });
    expect(scrollToSpy).not.toHaveBeenCalled();
    expect(sent.filter((m) => m.type === "position")).not.toHaveLength(0);
    scrollToSpy.mockRestore();

    // Paged wheel travel past the pull threshold turns the page; a dribble
    // and a zoom gesture do nothing.
    sent.length = 0;
    window.dispatchEvent(
      new WheelEvent("wheel", { bubbles: true, deltaY: 700 }),
    );
    expect(sent.filter((m) => m.type === "at-boundary")).toHaveLength(1);
    window.dispatchEvent(
      new WheelEvent("wheel", { bubbles: true, deltaY: 100 }),
    );
    // A zoom gesture never turns: happy-dom's WheelEvent drops modifier init,
    // so the ctrlKey rides on a plain event here.
    const zoom = new Event("wheel", { bubbles: true }) as Event & {
      deltaY: number;
      ctrlKey: boolean;
    };
    zoom.deltaY = 700;
    zoom.ctrlKey = true;
    window.dispatchEvent(zoom);
    expect(sent.filter((m) => m.type === "at-boundary")).toHaveLength(1);

    // A paged-to-scroll switch freezes an in-flight turn: its scrollLeft swap
    // would otherwise land mid-scroll-mode and report a paged percent.
    const pagedContent = document.getElementById("content")!;
    Object.defineProperty(pagedContent, "clientWidth", {
      value: 800,
      configurable: true,
    });
    Object.defineProperty(pagedContent, "scrollWidth", {
      value: 3200,
      configurable: true,
    });
    incoming({ type: "apply-settings", settings: iframeSettings("paged") });
    await vi.advanceTimersByTimeAsync(500);
    incoming({ type: "next-page", seq: 2 });
    expect(document.documentElement.classList.contains("page-turning")).toBe(
      true,
    );
    incoming({ type: "apply-settings", settings: iframeSettings("scroll") });
    expect(document.documentElement.classList.contains("page-turning")).toBe(
      false,
    );
    expect(pagedContent.scrollLeft).toBe(0);

    // The swap marks the chapter busy until a reveal clears it.
    incoming({ ...verticalLoad, seq: 3 });
    expect(document.getElementById("content")?.getAttribute("aria-busy")).toBe(
      "true",
    );
    await vi.advanceTimersByTimeAsync(1_000);
    expect(
      document.getElementById("content")?.getAttribute("aria-busy"),
    ).toBeNull();
  } finally {
    incoming({ type: "destroy" });
  }
});

it("forwards throttled mouse movement so the parent chrome can reveal itself", async () => {
  vi.useFakeTimers();
  // The suite above already imported the frame IIFE; a fresh registry is what
  // gives this test its own listeners.
  vi.resetModules();
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

  // happy-dom drops PointerEvent init fields, so pointerType rides on a plain
  // event here (same trick as the zoom wheel case above).
  const move = (pointerType: string): void => {
    const e = new Event("pointermove", { bubbles: true }) as Event & {
      pointerType: string;
    };
    e.pointerType = pointerType;
    document.dispatchEvent(e);
  };
  const pings = (): number =>
    sent.filter((m) => m.type === "pointer-activity").length;

  await import("./frame");
  try {
    move("mouse");
    expect(pings()).toBe(1);

    // Within the throttle window the parent learns nothing new.
    move("mouse");
    await vi.advanceTimersByTimeAsync(100);
    move("mouse");
    expect(pings()).toBe(1);

    await vi.advanceTimersByTimeAsync(300);
    move("mouse");
    expect(pings()).toBe(2);

    // A tap already sends "click"; forwarding its synthetic move too would
    // reveal the chrome the instant the tap asked to hide it.
    await vi.advanceTimersByTimeAsync(300);
    move("touch");
    expect(pings()).toBe(2);
  } finally {
    incoming({ type: "destroy" });
  }
});

it("stops justifying a narrow measure that cannot hyphenate", async () => {
  vi.useFakeTimers();
  vi.resetModules();
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
  vi.spyOn(window, "postMessage").mockImplementation(() => {});

  const overrides = (): string =>
    document.getElementById("override-css")?.textContent ?? "";

  await import("./frame");
  try {
    incoming({
      type: "apply-settings",
      settings: { ...iframeSettings("scroll"), justify: true },
    });
    expect(overrides()).toContain("text-align: justify");
    // Rivers open up on a phone-width measure with no break points, so the
    // narrow arm drops back to ragged right on its own.
    expect(overrides()).toMatch(
      /@media \(max-width: 480px\)[^}]*text-align: start/,
    );
    // The spread halves the same viewport, so it goes ragged twice as early.
    expect(overrides()).toMatch(
      /@media \(max-width: 960px\)[^}]*html\.paged-two body[^}]*text-align: start/,
    );

    // With hyphenation on there is somewhere to put the slack, so justified
    // text stays justified at every width.
    incoming({
      type: "apply-settings",
      settings: {
        ...iframeSettings("scroll"),
        justify: true,
        hyphenation: true,
      },
    });
    expect(overrides()).toContain("text-align: justify");
    expect(overrides()).not.toContain("text-align: start");
  } finally {
    incoming({ type: "destroy" });
  }
});
