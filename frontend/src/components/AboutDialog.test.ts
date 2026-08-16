// Suite for the About dialog. It is ShortcutsHelp's structural sibling, so the
// invariants are the same ones that suite pins:
//   - It exists only while ui.about is set.
//   - Escape is consumed in the window CAPTURE phase while open, so a single
//     press cannot both close the dialog and let the reader's own bubble
//     handler navigate back to the library.
//   - An Escape that ends an IME composition is not a dismissal.
//   - The listener is attached only while open, and dropped on dispose even
//     if the dialog is still open.
//   - trap() owns focus: into the sheet on mount, back to the opener on close.
//   - "Keyboard shortcuts" HANDS OVER to the shortcuts sheet rather than
//     stacking a second focus trap on top of this one.
//   - The build line is read per open and torn down on close, and it never
//     turns a failed read or an unstamped build into visible noise.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type { VersionInfo } from "~/api/client";
import AboutDialog from "~/components/AboutDialog";
import { ui } from "~/lib/ui";

const stubs = vi.hoisted(() => ({
  getVersion: vi.fn<(signal?: AbortSignal) => Promise<VersionInfo>>(),
}));

vi.mock("~/api/client", () => ({ getVersion: stubs.getVersion }));

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("AboutDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let opener: HTMLButtonElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    // Stands in for the profile menu item that opens the dialog: focus
    // restoration is a statement about a real element.
    opener = document.createElement("button");
    document.body.appendChild(opener);
    stubs.getVersion.mockReset();
    stubs.getVersion.mockResolvedValue({
      version: "v1.2.0",
      buildDate: "2026-08-16T18:24:00Z",
    });
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    opener.remove();
    ui.closeOverlays();
    flush();
    vi.restoreAllMocks();
  });

  const sheet = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".about-sheet");

  function mount(): void {
    dispose = render(() => AboutDialog(), container);
    flush();
  }

  async function open(): Promise<void> {
    ui.openAbout();
    await settle();
  }

  function esc(isComposing = false): void {
    window.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        isComposing,
        bubbles: true,
        cancelable: true,
      }),
    );
  }

  it("renders only while the store says so", async () => {
    mount();
    expect(sheet()).toBeNull();

    await open();
    const el = sheet();
    expect(el).not.toBeNull();
    expect(el!.getAttribute("role")).toBe("dialog");
    expect(el!.getAttribute("aria-modal")).toBe("true");
    expect(el!.getAttribute("aria-label")).toBe("About Sayumi");

    ui.closeOverlays();
    await settle();
    expect(sheet()).toBeNull();
  });

  it("consumes Escape before window bubble handlers", async () => {
    mount();
    await open();

    const bubbled = vi.fn();
    window.addEventListener("keydown", bubbled);
    try {
      esc();
      await settle();
    } finally {
      window.removeEventListener("keydown", bubbled);
    }

    expect(ui.about).toBe(false);
    expect(sheet()).toBeNull();
    // The reader binds Escape on window in the bubble phase to go back to the
    // library. Capture + stopImmediatePropagation is what keeps one press
    // from doing both.
    expect(bubbled).not.toHaveBeenCalled();
  });

  it("ignores an Escape that ends an IME composition", async () => {
    mount();
    await open();

    esc(true);
    await settle();
    expect(sheet()).not.toBeNull();

    esc();
    await settle();
    expect(sheet()).toBeNull();
  });

  it("drops its listener on dispose, even while open", async () => {
    mount();
    await open();

    dispose?.();
    dispose = undefined;

    const bubbled = vi.fn();
    window.addEventListener("keydown", bubbled);
    try {
      esc();
      await settle();
    } finally {
      window.removeEventListener("keydown", bubbled);
    }

    // Nothing swallowed the key, so the disposed dialog left no listener
    // behind on window.
    expect(bubbled).toHaveBeenCalledTimes(1);
  });

  it("closes from the backdrop and from the close button", async () => {
    mount();
    await open();

    container.querySelector<HTMLButtonElement>(".backdrop-dismiss")!.click();
    await settle();
    expect(sheet()).toBeNull();

    await open();
    container.querySelector<HTMLButtonElement>(".about-close")!.click();
    await settle();
    expect(sheet()).toBeNull();
  });

  it("moves focus into the sheet and hands it back on close", async () => {
    mount();
    opener.focus();
    expect(document.activeElement).toBe(opener);

    await open();
    expect(container.contains(document.activeElement)).toBe(true);

    ui.closeOverlays();
    await settle();
    expect(document.activeElement).toBe(opener);
  });

  it("hands over to the shortcuts sheet instead of stacking on it", async () => {
    mount();
    await open();

    const buttons = Array.from(
      container.querySelectorAll<HTMLButtonElement>(".about-actions button"),
    );
    const shortcuts = buttons.find((b) =>
      (b.textContent ?? "").includes("Keyboard shortcuts"),
    );
    expect(shortcuts).toBeDefined();

    shortcuts!.click();
    await settle();
    expect(ui.shortcuts).toBe(true);
    expect(ui.about).toBe(false);
    expect(sheet()).toBeNull();
  });

  it("names the running build, to the calendar day", async () => {
    mount();
    await open();

    const build = container.querySelector<HTMLElement>(".about-build");
    expect(build).not.toBeNull();
    expect(build!.textContent).toContain("v1.2.0");
    // The server stamps an RFC 3339 instant; a clock time would read as
    // precision nobody asked for.
    expect(build!.textContent).toContain("2026-08-16");
    expect(build!.textContent).not.toContain("18:24");
  });

  it("re-reads the version per open and aborts on close", async () => {
    mount();
    await open();
    expect(stubs.getVersion).toHaveBeenCalledTimes(1);

    const signal = stubs.getVersion.mock.calls[0]?.[0];
    ui.closeOverlays();
    await settle();
    expect(signal?.aborted).toBe(true);

    // A restart can put a newer binary behind a tab that never reloads, so the
    // first answer is not cached for the life of the page.
    await open();
    expect(stubs.getVersion).toHaveBeenCalledTimes(2);
  });

  it("omits the build line when the server will not say", async () => {
    stubs.getVersion.mockRejectedValue(new Error("offline"));
    mount();
    await open();

    expect(sheet()).not.toBeNull();
    expect(container.querySelector(".about-build")).toBeNull();
  });

  it("omits the day for a binary built outside the scripts", async () => {
    stubs.getVersion.mockResolvedValue({
      version: "dev",
      buildDate: "unknown",
    });
    mount();
    await open();

    const build = container.querySelector<HTMLElement>(".about-build");
    expect(build).not.toBeNull();
    expect(build!.textContent).toContain("dev");
    expect(build!.textContent).not.toContain("unknown");
  });
});
