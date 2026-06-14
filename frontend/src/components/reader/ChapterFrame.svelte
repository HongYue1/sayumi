<script lang="ts">
  import { onMount } from "svelte";
  import { buildFrameSrcdoc } from "~/iframe/buildFrameHtml";
  import { buildReaderFontFaces } from "~/lib/readerFontFaces";
  import type { ChapterData } from "~/api/client";
  import type { IframeSettings } from "~/lib/settings.svelte";
  import type { ChapterFrameAPI, KeyEvent } from "./frame-types";

  interface Props {
    initialTheme: string;
    onapi?: (api: ChapterFrameAPI) => void;
    onready?: () => void;
    onloaded?: (seq: number) => void;
    onposition?: (chapterIndex: number, percent: number, cfi?: string) => void;
    onboundary?: (boundary: "start" | "end") => void;
    onlinkclicked?: (href: string) => void;
    onkey?: (e: KeyEvent) => void;
    onclickregion?: (region: "left" | "center" | "right") => void;
    onframeerror?: (code: string, message: string) => void;
    onpagechanged?: (current: number, total: number) => void;
  }

  let {
    initialTheme,
    onapi,
    onready,
    onloaded,
    onposition,
    onboundary,
    onlinkclicked,
    onkey,
    onclickregion,
    onframeerror,
    onpagechanged,
  }: Props = $props();

  // srcdoc iframes report a null origin, so "*" is the only valid postMessage
  // target. frame.ts uses the same target for the same reason.
  const FRAME_TARGET_ORIGIN = "*";

  // srcdoc is built once at mount; the iframe document is static thereafter and
  // theme changes are pushed in via apply-settings, so the initial theme is fine.
  // svelte-ignore state_referenced_locally
  const srcdoc = buildFrameSrcdoc(crypto.randomUUID(), initialTheme);

  // Non-reactive instance state (not rendered).
  let iframeEl: HTMLIFrameElement | null = null;
  let seq = 0;
  let ready = false;
  let messageQueue: unknown[] = [];

  function frameWindow(): Window | null {
    return iframeEl?.contentWindow ?? null;
  }

  function sendToFrame(message: unknown): void {
    const target = frameWindow();
    if (!ready || !target) {
      messageQueue.push(message);
      return;
    }
    target.postMessage(message, FRAME_TARGET_ORIGIN);
  }

  function flushQueue(): void {
    const target = frameWindow();
    if (!target || messageQueue.length === 0) return;
    for (const message of messageQueue) {
      target.postMessage(message, FRAME_TARGET_ORIGIN);
    }
    messageQueue = [];
  }

  // ---- inbound message validation -----------------------------------------
  type FrameInboundMessage =
    | { type: "ready" }
    | { type: "loaded"; seq: number }
    | { type: "position"; seq: number; chapterIndex: number; percent: number; cfi?: string | null }
    | { type: "at-boundary"; seq: number; boundary: "start" | "end" }
    | { type: "link-clicked"; href: string }
    | { type: "key"; seq: number; key: string; code: string; ctrlKey: boolean; shiftKey: boolean; altKey: boolean; metaKey: boolean }
    | { type: "click"; seq: number; region: "left" | "center" | "right" }
    | { type: "error"; seq: number; code: string; message: string }
    | { type: "load-error"; seq: number; error: string }
    | { type: "page-changed"; seq: number; current: number; total: number };

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

  function isInbound(v: unknown): v is FrameInboundMessage {
    if (!isRecord(v) || !isStr(v.type)) return false;
    switch (v.type) {
      case "ready":
        return true;
      case "loaded":
        return isNum(v.seq);
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
        return isStr(v.href);
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
      case "error":
        return isNum(v.seq) && isStr(v.code) && isStr(v.message);
      case "load-error":
        return isNum(v.seq) && isStr(v.error);
      case "page-changed":
        return isNum(v.seq) && isNum(v.current) && isNum(v.total);
      default:
        return false;
    }
  }

  function acceptedOrigin(origin: string): boolean {
    return origin === "null" || origin === window.location.origin;
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
        sendToFrame({ type: "set-font-faces", fontFaces: buildReaderFontFaces() });
        flushQueue();
        onready?.();
        break;
      case "loaded":
        if (m.seq === seq) onloaded?.(m.seq);
        break;
      case "position":
        if (m.seq === seq) onposition?.(m.chapterIndex, m.percent, m.cfi ?? undefined);
        break;
      case "at-boundary":
        if (m.seq === seq) onboundary?.(m.boundary);
        break;
      case "link-clicked":
        onlinkclicked?.(m.href);
        break;
      case "key":
        if (m.seq === seq)
          onkey?.({
            key: m.key,
            code: m.code,
            ctrlKey: m.ctrlKey,
            shiftKey: m.shiftKey,
            altKey: m.altKey,
            metaKey: m.metaKey,
          });
        break;
      case "click":
        if (m.seq === seq) onclickregion?.(m.region);
        break;
      case "error":
        if (m.seq === seq) onframeerror?.(m.code, m.message);
        break;
      case "load-error":
        // frame.ts reports chapter render failures as "load-error"; surface
        // them through the same callback so the reader can show its error UI.
        if (m.seq === seq) onframeerror?.("load-error", m.error);
        break;
      case "page-changed":
        if (m.seq === seq) onpagechanged?.(m.current, m.total);
        break;
    }
  }

  const api: ChapterFrameAPI = {
    loadChapter(data, settings, scrollTo, fragment, hasPrev, hasNext, restorePercent, restoreCfi, language) {
      const nextSeq = ++seq;
      sendToFrame({
        type: "load",
        seq: nextSeq,
        origin: window.location.origin,
        chapterIndex: data.chapterIndex,
        html: data.html,
        css: data.css,
        fontFaceCSS: data.fontFaceCSS,
        direction: data.direction,
        writingMode: data.writingMode,
        language: language || undefined,
        resourceBase: data.resourceBase ?? null,
        scrollTo: scrollTo || "top",
        fragment: fragment || null,
        hasPrev: hasPrev !== false,
        hasNext: hasNext !== false,
        restorePercent: restorePercent ?? null,
        restoreCfi: restoreCfi ?? null,
      });
      sendToFrame({ type: "apply-settings", settings });
    },
    applySettings: (settings) => sendToFrame({ type: "apply-settings", settings }),
    scrollTo: (percent) => sendToFrame({ type: "scroll-to", percent }),
    scrollToEnd: () => sendToFrame({ type: "scroll-to-end" }),
    scrollToFragment: (id) => sendToFrame({ type: "scroll-to-fragment", id }),
    scrollToCfi: (cfi) => sendToFrame({ type: "scroll-to-cfi", cfi }),
    requestPosition: () => sendToFrame({ type: "get-position" }),
    nextPage: () => sendToFrame({ type: "next-page" }),
    prevPage: () => sendToFrame({ type: "prev-page" }),
    goToPage: (page) => sendToFrame({ type: "go-to-page", page }),
    goToLastPage: () => sendToFrame({ type: "go-to-last-page" }),
    highlightSearch: (charOffset, matchLen, query, forSeq) =>
      sendToFrame({ type: "highlight-search", seq: forSeq ?? seq, charOffset, matchLen, query }),
    clearHighlights: () => sendToFrame({ type: "clear-highlights" }),
    setFontFaces: (css) => sendToFrame({ type: "set-font-faces", fontFaces: css }),
  };

  onMount(() => onapi?.(api));
</script>

<svelte:window onmessage={handleMessage} />

<iframe
  {@attach (el) => {
    iframeEl = el as HTMLIFrameElement;
    return () => {
      const w = iframeEl?.contentWindow;
      ready = false;
      messageQueue = [];
      if (w) w.postMessage({ type: "destroy" }, FRAME_TARGET_ORIGIN);
      iframeEl = null;
    };
  }}
  class="frame"
  {srcdoc}
  sandbox="allow-scripts"
  title="Book content"
></iframe>

<style>
  .frame {
    border: none;
    width: 100%;
    height: 100%;
    display: block;
    background: var(--bg);
  }
</style>
