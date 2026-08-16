// Suite for the share dialog. Only the side-effecting boundaries are stubbed
// -- uploadToGofile (spread over importOriginal so ApiError stays the real
// class and getDownloadUrl keeps its real URL), the clipboard, and toast.
// focusTrap is deliberately real: the focus invariant is a statement about
// who wins between the trap's fallback and the dialog's own intent, so
// stubbing it would assert nothing.
//
// The invariants, each of which regressed silently before this suite existed:
//   - Focus opens on the download action -- never on the header close
//     button, the first focusable in DOM order, where Enter dismisses the
//     dialog the reader had just opened.
//   - An Escape that ends an IME composition is not a dismissal; any other
//     Escape closes and is consumed before the page's own key handlers.
//   - The upload button is aria-disabled while busy, never disabled: a real
//     disabled attribute blurs the pressed button for the whole upload,
//     which can run for the 30-minute gofile timeout.
//   - Announcements come from one pre-mounted .sr-only region; the visible
//     error paragraph is inserted with its text and so carries no role.
//   - Closing mid-upload aborts the request, and the resulting AbortError is
//     not surfaced as an error.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { ApiError, getDownloadUrl, type BookMeta } from "~/api/client";

const stubs = vi.hoisted(() => ({
  uploadToGofile: vi.fn(),
  clipboard: vi.fn(),
  toasts: [] as string[],
}));

vi.mock("~/api/client", async (importOriginal) => {
  // importOriginal + spread, per Login.test.ts: a bare factory would replace
  // ApiError with a twin and turn every instanceof branch into dead code.
  const actual = await importOriginal<Record<string, unknown>>();
  return { ...actual, uploadToGofile: stubs.uploadToGofile };
});

vi.mock("~/lib/toast", () => ({
  toast: {
    show: (message: string) => {
      stubs.toasts.push(message);
    },
  },
}));

import ShareDialog from "~/components/library/ShareDialog";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function book(over: Partial<BookMeta> = {}): BookMeta {
  return {
    id: "bk-1",
    title: "The Left Hand of Darkness",
    author: "Ursula K. Le Guin",
    language: "en",
    publisher: "Ace",
    description: "",
    pubDate: "1969",
    hasCover: true,
    direction: "ltr",
    chapterCount: 24,
    progress: 0.42,
    updatedAt: "2026-08-05 00:00:00",
    ...over,
  };
}

describe("ShareDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let closes: number;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    closes = 0;
    stubs.uploadToGofile.mockReset();
    stubs.clipboard.mockReset();
    stubs.clipboard.mockResolvedValue(undefined);
    stubs.toasts.length = 0;
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: stubs.clipboard },
      configurable: true,
    });
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    Reflect.deleteProperty(navigator, "clipboard");
    vi.restoreAllMocks();
  });

  async function mount(): Promise<void> {
    dispose = render(
      () =>
        ShareDialog({
          book: book(),
          onclose: () => {
            closes += 1;
          },
        }),
      container,
    );
    await settle();
  }

  const sheet = (): HTMLElement =>
    container.querySelector<HTMLElement>(".sd-sheet")!;
  const closeBtn = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".sd-close")!;
  const downloadBtn = (): HTMLAnchorElement =>
    container.querySelector<HTMLAnchorElement>(".sd-download-btn")!;
  const uploadBtn = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".sd-upload-btn")!;
  const copyBtn = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".sd-copy-btn")!;
  const errorText = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".sd-error");
  const liveRegion = (): HTMLElement =>
    container.querySelector<HTMLElement>('.sr-only[role="alert"]')!;

  function key(target: EventTarget, init: KeyboardEventInit): void {
    target.dispatchEvent(
      new KeyboardEvent("keydown", {
        bubbles: true,
        cancelable: true,
        ...init,
      }),
    );
  }

  it("opens with focus on the download action, not the close button", async () => {
    await mount();

    expect(document.activeElement).toBe(downloadBtn());
    expect(document.activeElement).not.toBe(closeBtn());
  });

  it("closes on Escape, consuming it before the page's own handlers", async () => {
    // The page's window key listeners register before the dialog mounts.
    // Capture is what lets the dialog pre-empt them regardless of that order.
    const pageHandler = vi.fn();
    window.addEventListener("keydown", pageHandler);
    try {
      await mount();

      key(sheet(), { key: "Escape" });
      await settle();

      expect(closes).toBe(1);
      expect(pageHandler).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener("keydown", pageHandler);
    }
  });

  it("ignores an Escape that ends an IME composition", async () => {
    await mount();

    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    // Guards the environment as much as the component: if the flag does not
    // survive construction, this test proves nothing about the guard.
    expect(composing.isComposing).toBe(true);

    sheet().dispatchEvent(composing);
    await settle();
    expect(closes).toBe(0);

    key(sheet(), { key: "Escape" });
    await settle();
    expect(closes).toBe(1);
  });

  it("points the download action at the book's file endpoint", async () => {
    await mount();

    expect(downloadBtn().getAttribute("href")).toBe(getDownloadUrl("bk-1"));
    expect(downloadBtn().getAttribute("download")).toBe(
      "The Left Hand of Darkness.epub",
    );
  });

  it("keeps the upload button focusable while busy, and guards re-entry", async () => {
    let resolveUpload: ((value: { downloadPage: string }) => void) | undefined;
    stubs.uploadToGofile.mockImplementation(
      () =>
        new Promise<{ downloadPage: string }>((resolve) => {
          resolveUpload = resolve;
        }),
    );
    await mount();

    uploadBtn().click();
    await settle();

    expect(stubs.uploadToGofile).toHaveBeenCalledTimes(1);
    expect(uploadBtn().getAttribute("aria-disabled")).toBe("true");
    // aria-disabled, not disabled: the button keeps its place in the tab
    // order and stays focusable for the whole upload.
    expect(uploadBtn().hasAttribute("disabled")).toBe(false);
    uploadBtn().focus();
    expect(document.activeElement).toBe(uploadBtn());

    uploadBtn().click();
    await settle();
    expect(stubs.uploadToGofile).toHaveBeenCalledTimes(1);

    resolveUpload?.({ downloadPage: "https://gofile.io/d/abc" });
    await settle();
    expect(uploadBtn().getAttribute("aria-disabled")).toBe("false");
  });

  it("shows the link after a successful upload, and copies it", async () => {
    stubs.uploadToGofile.mockResolvedValue({
      downloadPage: "https://gofile.io/d/abc",
    });
    await mount();

    uploadBtn().click();
    await settle();

    const link = container.querySelector<HTMLAnchorElement>(".sd-result a");
    expect(link?.getAttribute("href")).toBe("https://gofile.io/d/abc");
    expect(link?.getAttribute("target")).toBe("_blank");
    expect(link?.getAttribute("rel")).toBe("noopener noreferrer");
    expect(uploadBtn().textContent).toContain("Upload again");

    copyBtn().click();
    await settle();

    expect(stubs.clipboard).toHaveBeenCalledWith("https://gofile.io/d/abc");
    expect(stubs.toasts).toEqual(["Link copied"]);
  });

  it("announces an ApiError from a region that exists before it has text", async () => {
    stubs.uploadToGofile.mockRejectedValue(
      new ApiError("gofile is down", 502, "server_error"),
    );
    await mount();

    // The region exists from first paint, before there is anything to say --
    // a region inserted in the same tick as its text is not announced.
    expect(liveRegion().textContent).toBe("");

    uploadBtn().click();
    await settle();

    expect(errorText()?.textContent).toBe("gofile is down");
    // The visible paragraph is inserted with its text, so it cannot be the
    // announcement channel; the pre-mounted region carries the message.
    expect(errorText()?.getAttribute("role")).toBeNull();
    expect(liveRegion().textContent).toBe("gofile is down");
    expect(stubs.toasts).toEqual([]);
  });

  it("falls back to a generic message for a non-ApiError", async () => {
    stubs.uploadToGofile.mockRejectedValue(new TypeError("fetch failed"));
    await mount();

    uploadBtn().click();
    await settle();

    expect(errorText()?.textContent).toBe("Upload to gofile failed.");
    expect(liveRegion().textContent).toBe("Upload to gofile failed.");
  });

  it("closing mid-upload aborts the request and surfaces nothing", async () => {
    let uploadSignal: AbortSignal | undefined;
    stubs.uploadToGofile.mockImplementation(
      (_id: string, signal: AbortSignal) =>
        new Promise((_, reject) => {
          uploadSignal = signal;
          signal.addEventListener("abort", () => {
            reject(
              new DOMException("The operation was aborted.", "AbortError"),
            );
          });
        }),
    );
    await mount();

    uploadBtn().click();
    await settle();
    expect(stubs.uploadToGofile).toHaveBeenCalledTimes(1);

    closeBtn().click();
    await settle();

    expect(closes).toBe(1);
    expect(uploadSignal?.aborted).toBe(true);
    // The late rejection is a user cancellation, not a failure: no error
    // text, no toast.
    expect(errorText()).toBeNull();
    expect(stubs.toasts).toEqual([]);
  });
});
