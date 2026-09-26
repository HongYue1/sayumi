// Browser fullscreen for the reader, as a signal the chrome can render from.
//
// The Fullscreen API reports its state on document, not through an event
// target we own, so the truth lives in `fullscreenchange` -- not in whether
// our own button was the last thing clicked. Esc, F11, and the OS window
// controls all leave fullscreen without touching this module, and a button
// that tracked its own clicks would then show the wrong icon. One
// document-level listener, attached at import, keeps the signal honest for
// every reader mount.
//
// The whole document element goes fullscreen rather than the reader stage: the
// stage holds a sandboxed iframe, and fullscreening an element whose subtree
// contains the frame reparents it in Chromium, which reloads the srcdoc and
// loses the reading position.
import { createSignal } from "solid-js";

/** Vendor-prefixed shapes still shipped by Safari; typed rather than `any` so
 *  the calls below stay checked. */
interface WebkitDocument extends Document {
  webkitFullscreenElement?: Element | null;
  webkitExitFullscreen?: () => Promise<void> | void;
}
interface WebkitElement extends HTMLElement {
  webkitRequestFullscreen?: () => Promise<void> | void;
}

function current(): boolean {
  if (typeof document === "undefined") return false;
  const d = document as WebkitDocument;
  return (d.fullscreenElement ?? d.webkitFullscreenElement ?? null) !== null;
}

const [active, setActive] = createSignal(current());

/** True while the page is fullscreen, however it got there or left. */
export function isFullscreen(): boolean {
  return active();
}

/** True when the browser offers fullscreen at all; the button is hidden
 *  otherwise (iOS Safari on iPhone has no element fullscreen). */
export function fullscreenSupported(): boolean {
  if (typeof document === "undefined") return false;
  const el = document.documentElement as WebkitElement;
  return (
    document.fullscreenEnabled === true ||
    typeof el.webkitRequestFullscreen === "function"
  );
}

/**
 * Enter fullscreen, or leave it when already there.
 *
 * requestFullscreen rejects when the call is not user-activated (and Firefox
 * rejects a second request made while one is in flight). Rejections are
 * swallowed: the signal still mirrors the document, so the UI simply stays as
 * it was rather than desyncing.
 */
export function toggleFullscreen(): void {
  if (typeof document === "undefined") return;
  const d = document as WebkitDocument;
  if (current()) {
    const exit = d.exitFullscreen?.bind(d) ?? d.webkitExitFullscreen?.bind(d);
    void Promise.resolve(exit?.()).catch(() => {});
    return;
  }
  const el = document.documentElement as WebkitElement;
  const request =
    el.requestFullscreen?.bind(el) ?? el.webkitRequestFullscreen?.bind(el);
  void Promise.resolve(request?.()).catch(() => {});
}

if (typeof document !== "undefined") {
  // Module-lifetime listeners, never removed: this state outlives every
  // component that renders it, the same call it makes in lib/theme.ts. Both
  // event names are bound -- Safari fires only the prefixed one.
  const sync = (): void => {
    setActive(current());
  };
  document.addEventListener("fullscreenchange", sync);
  document.addEventListener("webkitfullscreenchange", sync);
}
