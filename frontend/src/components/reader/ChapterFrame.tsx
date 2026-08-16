// ChapterFrame: sandboxed srcdoc iframe + postMessage bridge. Instance state
// is plain `let` bindings by design — nothing rendered reads it. The iframe
// lives exactly as long as the component, so teardown is component-level.
import { onCleanup, onSettled } from "solid-js";
import { buildFrameSrcdoc } from "~/iframe/buildFrameHtml";
import { buildReaderFontFaces } from "~/lib/readerFontFaces";
import type { ChapterFrameAPI, KeyEvent } from "./frame-types";
import type {
  FrameToParentMessage,
  FrameModeState,
  ParentToFrameMessage,
  ReadingDirection,
  WritingMode,
} from "~/lib/frameMessages";
import { createFrameMessageQueue } from "./frameMessageQueue";

// srcdoc iframes report a null origin, so "*" is the only valid postMessage
// target. frame.ts uses the same target for the same reason.
const FRAME_TARGET_ORIGIN = "*";

// ---- inbound message validation -----------------------------------------
const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null;
const isNum = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v);
const isBool = (v: unknown): v is boolean => typeof v === "boolean";
const isStr = (v: unknown): v is string => typeof v === "string";
const isBoundary = (v: unknown): v is "start" | "end" =>
  v === "start" || v === "end";
const isRegion = (v: unknown): v is "left" | "center" | "right" =>
  v === "left" || v === "center" || v === "right";
const isDisplayMode = (v: unknown): v is FrameModeState["mode"] =>
  v === "scroll" || v === "paged" || v === "paged-two";
const isModeFallback = (v: unknown): v is FrameModeState["fallback"] =>
  v === null || v === "vertical-writing";

// ChapterData carries these as plain strings off the wire. The Go side emits a
// closed set (internal/epub/parser.go, internal/epub/chapter.go), so this is
// the last place a value becomes a protocol enum: anything unexpected falls
// back to the same default the backend would have sent.
const toReadingDirection = (v: string): ReadingDirection =>
  v === "rtl" ? "rtl" : "ltr";
const toWritingMode = (v: string): WritingMode =>
  v === "vertical-rl" || v === "vertical-lr" ? v : "horizontal-tb";

function isInbound(v: unknown): v is FrameToParentMessage {
  if (!isRecord(v) || !isStr(v.type)) return false;
  switch (v.type) {
    case "ready":
      return true;
    case "loaded":
      return isNum(v.seq);
    case "effective-mode":
      return (
        isNum(v.seq) && isDisplayMode(v.mode) && isModeFallback(v.fallback)
      );
    case "position":
      return (
        isNum(v.seq) &&
        isNum(v.chapterIndex) &&
        isNum(v.percent) &&
        (v.cfi === undefined || v.cfi === null || isStr(v.cfi))
      );
    case "at-boundary":
      return isNum(v.seq) && isBoundary(v.boundary);
    case "link-clicked":
      return isNum(v.seq) && isStr(v.href);
    case "key":
      return (
        isNum(v.seq) &&
        isStr(v.key) &&
        isStr(v.code) &&
        isBool(v.ctrlKey) &&
        isBool(v.shiftKey) &&
        isBool(v.altKey) &&
        isBool(v.metaKey)
      );
    case "click":
      return isNum(v.seq) && isRegion(v.region);
    case "load-error":
      return isNum(v.seq) && isStr(v.error);
    default:
      return false;
  }
}

function acceptedOrigin(origin: string): boolean {
  return origin === "null" || origin === window.location.origin;
}

interface Props {
  initialTheme: string;
  /**
   * Resolved palette for a custom theme (settings.iframe.themeVars), null for a
   * built-in. Required rather than optional: a caller that forgets it silently
   * reintroduces the white flash described at the srcdoc const below.
   */
  initialThemeVars: string | null;
  /** Book language, so the document is tagged before the first chapter lands. */
  initialLanguage: string | null;
  onapi?: (api: ChapterFrameAPI) => void;
  onloaded?: (seq: number) => void;
  onmodechange?: (state: FrameModeState) => void;
  onposition?: (chapterIndex: number, percent: number, cfi?: string) => void;
  onboundary?: (boundary: "start" | "end") => void;
  onlinkclicked?: (href: string) => void;
  onkey?: (e: KeyEvent) => void;
  onclickregion?: (region: "left" | "center" | "right") => void;
  onframeerror?: (code: string, message: string) => void;
}

export default function ChapterFrame(props: Props) {
  // srcdoc is built once at mount; the iframe document is static thereafter and
  // theme changes are pushed in via apply-settings, so seeding it with the
  // initial values is enough. Deliberately a plain const: the initial* props
  // are read once, at mount, and never tracked.
  //
  // initialThemeVars is what makes that true for custom themes as well. They
  // have no static html.theme-<id> rule in frame.css, so without the palette
  // inlined here the first paint falls through to frame.css's bare `html`
  // rule — the light palette — and a custom dark theme flashes white until the
  // first apply-settings arrives.
  const srcdoc = buildFrameSrcdoc({
    nonce: crypto.randomUUID(),
    theme: props.initialTheme,
    themeVars: props.initialThemeVars,
    language: props.initialLanguage,
  });

  // Non-reactive instance state (not rendered).
  let iframeEl: HTMLIFrameElement | undefined;
  let seq = 0;
  let ready = false;
  let loadedSeq = -1;
  const messageQueue = createFrameMessageQueue();
  const chapterMessageQueue = createFrameMessageQueue();

  function frameWindow(): Window | null {
    return iframeEl?.contentWindow ?? null;
  }

  function sendToFrame(message: ParentToFrameMessage): void {
    const target = frameWindow();
    if (!ready || !target) {
      messageQueue.enqueue(message);
      return;
    }
    target.postMessage(message, FRAME_TARGET_ORIGIN);
  }

  function flushQueue(queue = messageQueue): void {
    const target = frameWindow();
    if (!target) return;
    for (const message of queue.drain()) {
      target.postMessage(message, FRAME_TARGET_ORIGIN);
    }
  }

  // Chapter-scoped commands wait for the matching load's settings, layout, and
  // initial restore. This is the sole command-readiness owner; callers may issue
  // a command immediately after loadChapter without racing the outgoing DOM.
  function sendChapterCommand(message: ParentToFrameMessage): void {
    // An explicit sequence (search highlights can supply one) must identify the
    // controller's current load. Reject future as well as stale commands.
    if ("seq" in message && message.seq !== seq) return;
    if (loadedSeq !== seq) {
      chapterMessageQueue.enqueue(message);
      return;
    }
    sendToFrame(message);
  }

  function handleMessage(event: MessageEvent<unknown>): void {
    const target = frameWindow();
    if (!target || event.source !== target) return;
    if (!acceptedOrigin(event.origin)) return;
    if (!isInbound(event.data)) return;

    const m = event.data;
    switch (m.type) {
      case "ready":
        ready = true;
        // Embedded faces only, as an initial state so the frame can render
        // before any chapter arrives. This is NOT the authoritative source:
        // Read.tsx pushes the full set (embedded + user families) from
        // loadChapter on every chapter load, which always supersedes this
        // before content appears.
        sendToFrame({
          type: "set-font-faces",
          fontFaces: buildReaderFontFaces(),
        });
        flushQueue();
        break;
      case "loaded":
        if (m.seq === seq) {
          loadedSeq = m.seq;
          flushQueue(chapterMessageQueue);
          props.onloaded?.(m.seq);
        }
        break;
      case "effective-mode":
        if (m.seq === seq)
          props.onmodechange?.({ mode: m.mode, fallback: m.fallback });
        break;
      case "position":
        if (m.seq === seq)
          props.onposition?.(m.chapterIndex, m.percent, m.cfi ?? undefined);
        break;
      case "at-boundary":
        if (m.seq === seq) props.onboundary?.(m.boundary);
        break;
      case "link-clicked":
        if (m.seq === seq) props.onlinkclicked?.(m.href);
        break;
      case "key":
        if (m.seq === seq)
          props.onkey?.({
            key: m.key,
            code: m.code,
            ctrlKey: m.ctrlKey,
            shiftKey: m.shiftKey,
            altKey: m.altKey,
            metaKey: m.metaKey,
          });
        break;
      case "click":
        if (m.seq === seq) props.onclickregion?.(m.region);
        break;
      case "load-error":
        // A failed current load can never settle, so discard commands waiting
        // for it before surfacing the error.
        if (m.seq === seq) {
          chapterMessageQueue.clear();
          props.onframeerror?.("load-error", m.error);
        }
        break;
      default: {
        // A new FrameToParentMessage kind that is not handled above fails
        // type-checking here. It also needs a validator case in isInbound,
        // which would otherwise reject it before it reached this switch.
        const _exhaustive: never = m;
        void _exhaustive;
      }
    }
  }

  const api: ChapterFrameAPI = {
    loadChapter(options) {
      const {
        data,
        settings,
        scrollTarget,
        fragment,
        hasPrev,
        hasNext,
        restore,
        language,
      } = options;
      const nextSeq = ++seq;
      loadedSeq = -1;
      chapterMessageQueue.clear();
      sendToFrame({
        type: "load",
        seq: nextSeq,
        origin: window.location.origin,
        chapterIndex: data.chapterIndex,
        settings,
        html: data.html,
        css: data.css,
        fontFaceCSS: data.fontFaceCSS,
        direction: toReadingDirection(data.direction),
        writingMode: toWritingMode(data.writingMode),
        language: language || undefined,
        resourceBase: data.resourceBase ?? null,
        scrollTo: scrollTarget || "top",
        fragment: fragment || null,
        hasPrev,
        hasNext,
        restorePercent: restore?.percent ?? null,
        restoreCfi: restore?.cfi ?? null,
      });
    },
    applySettings: (settings) =>
      sendToFrame({ type: "apply-settings", settings }),
    // seq is read at call time, so these stamp the chapter the caller was
    // looking at. A command issued just before a chapter turn is dropped by
    // the frame instead of moving the chapter that replaced it.
    scrollTo: (percent) =>
      sendChapterCommand({ type: "scroll-to", seq, percent }),
    scrollToEnd: () => sendChapterCommand({ type: "scroll-to-end", seq }),
    scrollToFragment: (id) =>
      sendChapterCommand({ type: "scroll-to-fragment", seq, id }),
    scrollToCfi: (cfi) =>
      sendChapterCommand({ type: "scroll-to-cfi", seq, cfi }),
    requestPosition: () => sendChapterCommand({ type: "get-position" }),
    nextPage: () => sendChapterCommand({ type: "next-page", seq }),
    prevPage: () => sendChapterCommand({ type: "prev-page", seq }),
    highlightSearch: (charOffset, matchLen, query, forSeq) =>
      sendChapterCommand({
        type: "highlight-search",
        seq: forSeq ?? seq,
        charOffset,
        matchLen,
        query,
      }),
    clearHighlights: () => {
      chapterMessageQueue.discard("highlight-search");
      sendToFrame({ type: "clear-highlights" });
    },
    setFontFaces: (css) =>
      sendToFrame({ type: "set-font-faces", fontFaces: css }),
  };

  // visualViewport resize is the only signal the parent gets for a pinch or a
  // browser zoom, and the sandboxed frame cannot see it from the inside. Wait
  // for the gesture to settle, then ask the frame to repaint at the new scale
  // (see refresh-raster in frameMessages.ts for why it has to).
  const RASTER_REFRESH_SETTLE_MS = 150;
  // Pinching past the zoom limit keeps emitting resize at a scale that can no
  // longer change, so the settle timer would fire a second, pointless repaint
  // that reads as a flicker. Refresh only when the scale actually moved.
  const MIN_SCALE_DELTA = 0.01;
  let rasterRefreshTimer: ReturnType<typeof setTimeout> | null = null;
  let lastRefreshedScale = 1;

  function scheduleRasterRefresh(): void {
    if (rasterRefreshTimer !== null) clearTimeout(rasterRefreshTimer);
    rasterRefreshTimer = setTimeout(() => {
      rasterRefreshTimer = null;
      const scale = window.visualViewport?.scale ?? 1;
      if (Math.abs(scale - lastRefreshedScale) < MIN_SCALE_DELTA) return;
      lastRefreshedScale = scale;
      sendToFrame({ type: "refresh-raster" });
    }, RASTER_REFRESH_SETTLE_MS);
  }

  onSettled(() => props.onapi?.(api));

  onSettled(() => {
    // The teardown is returned, not registered via onCleanup: onCleanup
    // inside an onSettled callback throws CLEANUP_IN_FORBIDDEN_SCOPE in dev
    // builds, leaking the listeners and the raster timer there. Every sibling
    // settle handler uses this returned shape.
    window.addEventListener("message", handleMessage);
    const viewport = window.visualViewport;
    if (viewport) {
      lastRefreshedScale = viewport.scale;
      viewport.addEventListener("resize", scheduleRasterRefresh);
    }
    return () => {
      window.removeEventListener("message", handleMessage);
      if (viewport)
        viewport.removeEventListener("resize", scheduleRasterRefresh);
      if (rasterRefreshTimer !== null) {
        clearTimeout(rasterRefreshTimer);
        rasterRefreshTimer = null;
      }
    };
  });

  // The iframe is the component's root element and lives exactly as long as
  // the component, so its teardown is this component-level cleanup.
  onCleanup(() => {
    const w = iframeEl?.contentWindow;
    ready = false;
    loadedSeq = -1;
    messageQueue.clear();
    chapterMessageQueue.clear();
    if (w) w.postMessage({ type: "destroy" }, FRAME_TARGET_ORIGIN);
    iframeEl = undefined;
  });

  // sandbox: allow-scripts runs frame.ts. allow-popups (+ escape-sandbox) let
  // an in-book link open in a real new tab via window.open() in frame.ts —
  // without them the popup is silently blocked, so a left-click would do
  // nothing while a middle-click (the browser's native new-tab path) still
  // works.
  return (
    <iframe
      ref={(el) => {
        iframeEl = el;
      }}
      class="cf-frame"
      srcdoc={srcdoc}
      sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
      title="Book content"
    />
  );
}
