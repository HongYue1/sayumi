// ChapterFrame.test.ts — mounts the real component with @solidjs/web's render
// (not @solidjs/testing-library, whose dist imports the removed "solid-js/web"
// specifier — see Login.test.ts) against a stubbed iframe contentWindow.
//
// Two module mocks keep the suite hermetic:
//   - ~/iframe/buildFrameHtml imports `virtual:frame-script`, which
//     vitest.config.ts deliberately never resolves, so the srcdoc builder is
//     replaced with an options recorder.
//   - ~/lib/readerFontFaces is replaced so no font registry or API client is
//     pulled in.
// Frame->parent traffic is hand-dispatched MessageEvents; `source` is set via
// defineProperty because the MessageEvent constructor will not take a plain
// object for it. `flush` forces Solid 2.0's batched writes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Mock } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import ChapterFrame from "~/components/reader/ChapterFrame";
import type { ChapterData } from "~/api/client";
import type { IframeSettings } from "~/lib/settings";
import type { FrameModeState, ParentToFrameMessage } from "~/lib/frameMessages";
import type {
  ChapterFrameAPI,
  ChapterLoadOptions,
} from "~/components/reader/frame-types";

const srcdocCalls = vi.hoisted(() => ({
  options: [] as Array<Record<string, unknown>>,
}));

vi.mock("~/iframe/buildFrameHtml", () => ({
  buildFrameSrcdoc: (options: Record<string, unknown>) => {
    srcdocCalls.options.push(options);
    return "<!DOCTYPE html><html></html>";
  },
}));

vi.mock("~/lib/readerFontFaces", () => ({
  buildReaderFontFaces: () => "READER_FONT_FACES",
}));

const FONT_FACES = "READER_FONT_FACES";

function chapterData(index = 0): ChapterData {
  return {
    chapterIndex: index,
    html: `<p>chapter ${index}</p>`,
    css: "",
    fontFaceCSS: "",
    direction: "ltr",
    writingMode: "horizontal-tb",
  };
}

function iframeSettings(theme = "catppuccin"): IframeSettings {
  return {
    mode: "scroll",
    fontSize: 30,
    fontFamily: "Literata",
    preserveBookStyles: true,
    preserveBookFonts: false,
    lineHeight: null,
    paragraphSpacing: null,
    textIndent: null,
    letterSpacing: null,
    contentWidth: null,
    margins: { top: null, bottom: null, side: null },
    justify: true,
    hyphenation: true,
    theme,
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

function loadOptions(
  index = 0,
  settings = iframeSettings(),
): ChapterLoadOptions {
  return {
    data: chapterData(index),
    settings,
    hasPrev: index > 0,
    hasNext: true,
  };
}

describe("ChapterFrame", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let posts: ParentToFrameMessage[];
  let frameWindow: { postMessage: (m: ParentToFrameMessage) => void };
  let onready: Mock<() => void>;
  let onloaded: Mock<(seq: number) => void>;
  let onmodechange: Mock<(state: FrameModeState) => void>;
  let onposition: Mock<
    (chapterIndex: number, percent: number, cfi?: string) => void
  >;
  let onframeerror: Mock<(code: string, message: string) => void>;
  let api: ChapterFrameAPI;

  function fireFrameMessage(
    data: unknown,
    origin = "null",
    source: unknown = frameWindow,
  ): void {
    const ev = new MessageEvent("message", { data, origin });
    Object.defineProperty(ev, "source", { value: source });
    window.dispatchEvent(ev);
    flush();
  }

  function mount(): void {
    posts = [];
    frameWindow = {
      postMessage: (m) => {
        posts.push(m);
      },
    };
    onready = vi.fn<() => void>();
    onloaded = vi.fn<(seq: number) => void>();
    onmodechange = vi.fn<(state: FrameModeState) => void>();
    onposition =
      vi.fn<(chapterIndex: number, percent: number, cfi?: string) => void>();
    onframeerror = vi.fn<(code: string, message: string) => void>();
    container = document.createElement("div");
    document.body.append(container);
    // Mounting must not throw: the onSettled listener registration used to
    // nest onCleanup inside the settle callback, which dev builds reject with
    // CLEANUP_IN_FORBIDDEN_SCOPE (probe-verified against beta.29).
    dispose = render(
      () =>
        ChapterFrame({
          initialTheme: "catppuccin",
          initialThemeVars: null,
          initialLanguage: "en",
          onready,
          onloaded,
          onmodechange,
          onposition,
          onframeerror,
          onapi: (a) => {
            api = a;
          },
        }),
      container,
    );
    flush();
    const iframe = container.querySelector("iframe");
    expect(iframe).not.toBeNull();
    Object.defineProperty(iframe, "contentWindow", {
      configurable: true,
      value: frameWindow,
    });
  }

  function ready(): void {
    fireFrameMessage({ type: "ready" });
  }

  beforeEach(() => {
    srcdocCalls.options.length = 0;
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container?.remove();
  });

  it("mounts and delivers its imperative api without throwing", () => {
    mount();
    expect(typeof api.loadChapter).toBe("function");
    expect(srcdocCalls.options).toHaveLength(1);
    expect(srcdocCalls.options[0]).toMatchObject({
      theme: "catppuccin",
      themeVars: null,
      language: "en",
    });
  });

  it("queues a pre-ready load and flushes it behind set-font-faces on ready", () => {
    mount();
    api.loadChapter(loadOptions(0));
    expect(posts).toHaveLength(0);
    ready();
    expect(onready).toHaveBeenCalledTimes(1);
    expect(posts.map((m) => m.type)).toEqual([
      "set-font-faces",
      "load",
      "apply-settings",
    ]);
    expect(posts[0]).toMatchObject({ fontFaces: FONT_FACES });
    expect(posts[1]).toMatchObject({ seq: 1, chapterIndex: 0 });
  });

  it("coalesces two pre-ready loads: only the latest chapter flushes", () => {
    mount();
    api.loadChapter(loadOptions(0));
    api.loadChapter(loadOptions(1));
    ready();
    const loads = posts.filter((m) => m.type === "load");
    expect(loads).toHaveLength(1);
    expect(loads[0]).toMatchObject({ seq: 2, chapterIndex: 1 });
  });

  it("pairs every load with an apply-settings (the frame never reveals without it)", () => {
    mount();
    ready();
    posts.length = 0;
    const settings = iframeSettings();
    api.loadChapter(loadOptions(3, settings));
    expect(posts.map((m) => m.type)).toEqual(["load", "apply-settings"]);
    expect((posts[1] as { settings: unknown }).settings).toBe(settings);
  });

  it("maps every named load option onto the wire without boundary transposition", () => {
    mount();
    ready();
    posts.length = 0;
    api.loadChapter({
      data: { ...chapterData(2), resourceBase: "/api/books/b/resources" },
      settings: iframeSettings(),
      scrollTarget: "end",
      fragment: "note-4",
      hasPrev: false,
      hasNext: true,
      restore: { percent: 0.375, cfi: "cfi:2/4" },
      language: "ja",
    });
    const load = posts.find((m) => m.type === "load");
    expect(load).toMatchObject({
      type: "load",
      chapterIndex: 2,
      resourceBase: "/api/books/b/resources",
      scrollTo: "end",
      fragment: "note-4",
      hasPrev: false,
      hasNext: true,
      restorePercent: 0.375,
      restoreCfi: "cfi:2/4",
      language: "ja",
    });
  });

  it("delivers only live-seq inbound messages to props", () => {
    mount();
    ready();
    api.loadChapter(loadOptions(0)); // seq 1
    fireFrameMessage({ type: "loaded", seq: 1 });
    expect(onloaded).toHaveBeenCalledWith(1);
    fireFrameMessage({ type: "loaded", seq: 7 });
    expect(onloaded).toHaveBeenCalledTimes(1);
    fireFrameMessage({
      type: "position",
      seq: 1,
      chapterIndex: 0,
      percent: 0.5,
      cfi: "cfi:/2",
    });
    expect(onposition).toHaveBeenCalledWith(0, 0.5, "cfi:/2");
    fireFrameMessage({
      type: "position",
      seq: 1,
      chapterIndex: 0,
      percent: 0.9,
    });
    expect(onposition).toHaveBeenCalledWith(0, 0.9, undefined);
    // Malformed percent fails isInbound.
    fireFrameMessage({
      type: "position",
      seq: 1,
      chapterIndex: 0,
      percent: "lots",
    });
    expect(onposition).toHaveBeenCalledTimes(2);
    // Stale seq is dropped.
    fireFrameMessage({
      type: "position",
      seq: 0,
      chapterIndex: 0,
      percent: 0.1,
    });
    expect(onposition).toHaveBeenCalledTimes(2);
    // load-error routes to onframeerror.
    fireFrameMessage({ type: "load-error", seq: 1, error: "boom" });
    expect(onframeerror).toHaveBeenCalledWith("load-error", "boom");
  });

  it("validates and seq-filters effective-mode reports", () => {
    mount();
    ready();
    api.loadChapter(loadOptions(0)); // seq 1
    fireFrameMessage({
      type: "effective-mode",
      seq: 1,
      mode: "scroll",
      fallback: "vertical-writing",
    });
    expect(onmodechange).toHaveBeenCalledWith({
      mode: "scroll",
      fallback: "vertical-writing",
    });
    fireFrameMessage({
      type: "effective-mode",
      seq: 0,
      mode: "paged",
      fallback: null,
    });
    fireFrameMessage({
      type: "effective-mode",
      seq: 1,
      mode: "columns",
      fallback: null,
    });
    fireFrameMessage({
      type: "effective-mode",
      seq: 1,
      mode: "paged",
      fallback: "unknown",
    });
    expect(onmodechange).toHaveBeenCalledTimes(1);
  });

  it("drops messages from the wrong source or a foreign origin", () => {
    mount();
    ready();
    api.loadChapter(loadOptions(0));
    fireFrameMessage({ type: "loaded", seq: 1 }, "null", window);
    expect(onloaded).not.toHaveBeenCalled();
    fireFrameMessage({ type: "loaded", seq: 1 }, "https://evil.example");
    expect(onloaded).not.toHaveBeenCalled();
    fireFrameMessage({ type: "loaded", seq: 1 });
    expect(onloaded).toHaveBeenCalledTimes(1);
  });

  it("stamps positional commands with the live seq at call time", () => {
    mount();
    ready();
    posts.length = 0;
    api.scrollTo(0.25);
    expect(posts[0]).toMatchObject({
      type: "scroll-to",
      seq: 0,
      percent: 0.25,
    });
    api.loadChapter(loadOptions(0)); // seq 1
    api.nextPage();
    expect(posts[posts.length - 1]).toMatchObject({
      type: "next-page",
      seq: 1,
    });
    api.loadChapter(loadOptions(1)); // seq 2
    api.prevPage();
    expect(posts[posts.length - 1]).toMatchObject({
      type: "prev-page",
      seq: 2,
    });
  });

  it("posts destroy and detaches the window listener on unmount", () => {
    mount();
    ready();
    api.loadChapter(loadOptions(0));
    posts.length = 0;
    dispose?.();
    dispose = undefined;
    expect(posts.map((m) => m.type)).toEqual(["destroy"]);
    fireFrameMessage({
      type: "position",
      seq: 1,
      chapterIndex: 0,
      percent: 0.5,
    });
    expect(onposition).not.toHaveBeenCalled();
  });

  it("sends refresh-raster only when the visualViewport scale actually moves", () => {
    const listeners: Record<string, () => void> = {};
    const viewport = {
      scale: 1,
      addEventListener: (type: string, fn: () => void) => {
        listeners[type] = fn;
      },
      removeEventListener: () => {},
    };
    Object.defineProperty(window, "visualViewport", {
      configurable: true,
      value: viewport,
    });
    try {
      mount();
      ready();
      posts.length = 0;
      vi.useFakeTimers();
      // Same scale: the settle timer fires but nothing is sent.
      listeners.resize?.();
      vi.advanceTimersByTime(200);
      expect(posts).toHaveLength(0);
      // A real zoom: one refresh-raster.
      viewport.scale = 1.5;
      listeners.resize?.();
      vi.advanceTimersByTime(200);
      expect(posts.map((m) => m.type)).toEqual(["refresh-raster"]);
      // Pinching at the zoom limit keeps emitting resize at the same scale.
      posts.length = 0;
      listeners.resize?.();
      vi.advanceTimersByTime(200);
      expect(posts).toHaveLength(0);
    } finally {
      vi.useRealTimers();
      Reflect.deleteProperty(window, "visualViewport");
    }
  });
});
