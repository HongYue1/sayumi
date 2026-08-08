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
};

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
  document.head.innerHTML = "";
  document.body.innerHTML = "";
});

it("uses and reports one effective scroll mode for a vertical paged request", async () => {
  vi.useFakeTimers();
  document.head.innerHTML = `
    <style id="font-face-css"></style>
    <style id="book-css"></style>
    <style id="override-css"></style>`;
  document.body.innerHTML =
    '<div id="paged-clip"><div id="content"><div id="content-inner"></div></div></div>';

  const sent: FrameToParentMessage[] = [];
  vi.spyOn(window, "postMessage").mockImplementation((message) => {
    sent.push(message as FrameToParentMessage);
  });

  await import("./frame");
  try {
    incoming({ type: "set-font-faces", fontFaces: "" });
    incoming(verticalLoad);
    incoming({ type: "apply-settings", settings: iframeSettings("paged") });
    await vi.advanceTimersByTimeAsync(0);

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
  } finally {
    incoming({ type: "destroy" });
  }
});
