// Suite for the fullscreen signal. The invariants worth pinning:
//   - The element requested is the document element, never a reader node: the
//     stage contains the sandboxed iframe, and fullscreening an ancestor of it
//     reparents the frame in Chromium, reloading srcdoc and losing the page.
//   - The signal follows the document's `fullscreenchange`, so Esc/F11/OS exits
//     are seen even though they never call us.
//   - A rejected request (no user activation) leaves the state alone instead
//     of throwing or desyncing the button icon.
import { afterEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import {
  fullscreenSupported,
  isFullscreen,
  toggleFullscreen,
} from "~/lib/fullscreen";

type Mutable = Record<string, unknown>;

/** Installs a property and returns the undo, so each test leaves happy-dom's
 *  document as it found it. */
function stub(target: object, key: string, value: unknown): () => void {
  const had = Object.prototype.hasOwnProperty.call(target, key);
  const prev = (target as Mutable)[key];
  Object.defineProperty(target, key, {
    value,
    configurable: true,
    writable: true,
  });
  return () => {
    if (had) {
      Object.defineProperty(target, key, {
        value: prev,
        configurable: true,
        writable: true,
      });
    } else {
      delete (target as Mutable)[key];
    }
  };
}

const undos: (() => void)[] = [];

function setFullscreenElement(el: Element | null): void {
  undos.push(stub(document, "fullscreenElement", el));
}

/** The document is the only source of truth, so state changes are announced
 *  the way a browser announces them. */
function announce(): void {
  document.dispatchEvent(new Event("fullscreenchange"));
  flush();
}

afterEach(() => {
  while (undos.length > 0) undos.pop()?.();
  const restore = stub(document, "fullscreenElement", null);
  announce();
  restore();
  vi.restoreAllMocks();
});

describe("fullscreen", () => {
  it("requests the document element, not a reader subtree", () => {
    const request = vi.fn(() => Promise.resolve());
    undos.push(stub(document.documentElement, "requestFullscreen", request));
    setFullscreenElement(null);

    toggleFullscreen();

    expect(request).toHaveBeenCalledTimes(1);
  });

  it("exits when the document already reports a fullscreen element", () => {
    const exit = vi.fn(() => Promise.resolve());
    const request = vi.fn(() => Promise.resolve());
    undos.push(stub(document, "exitFullscreen", exit));
    undos.push(stub(document.documentElement, "requestFullscreen", request));
    setFullscreenElement(document.documentElement);

    toggleFullscreen();

    expect(exit).toHaveBeenCalledTimes(1);
    expect(request).not.toHaveBeenCalled();
  });

  it("tracks fullscreenchange rather than our own calls", () => {
    setFullscreenElement(null);
    announce();
    expect(isFullscreen()).toBe(false);

    // A change we never triggered -- Esc, F11, the window controls.
    setFullscreenElement(document.documentElement);
    announce();
    expect(isFullscreen()).toBe(true);

    setFullscreenElement(null);
    announce();
    expect(isFullscreen()).toBe(false);
  });

  it("swallows a request the browser refuses", async () => {
    const request = vi.fn(() => Promise.reject(new Error("not allowed")));
    undos.push(stub(document.documentElement, "requestFullscreen", request));
    setFullscreenElement(null);

    expect(() => {
      toggleFullscreen();
    }).not.toThrow();
    await Promise.resolve();
    expect(isFullscreen()).toBe(false);
  });

  it("reports support from either the standard or the webkit entry point", () => {
    undos.push(stub(document, "fullscreenEnabled", false));
    undos.push(
      stub(document.documentElement, "webkitRequestFullscreen", undefined),
    );
    expect(fullscreenSupported()).toBe(false);

    undos.push(
      stub(document.documentElement, "webkitRequestFullscreen", () => {}),
    );
    expect(fullscreenSupported()).toBe(true);

    undos.push(stub(document, "fullscreenEnabled", true));
    expect(fullscreenSupported()).toBe(true);
  });
});
