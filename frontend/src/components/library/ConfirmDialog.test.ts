// Suite for the shared destructive confirmation. Nothing is mocked: the whole
// contract is DOM structure plus which callback fires, and the two hosts (the
// book delete, the custom-flair delete) only supply copy.
//
// What carries the weight here is everything the native confirm() it replaced
// could not express, and could not be tested for at all:
//   - the dialog is labelled and its description is the consequence, so the
//     text is announced with the dialog instead of waiting to be found;
//   - focus lands on Cancel, not on the destructive button, so the Enter that
//     opened the dialog cannot also accept it;
//   - Escape is handled from a window capture listener and consumed, so the
//     shelf's and the reader's own window key handlers never see it -- and a
//     composing Escape, which belongs to the IME candidate window, is left
//     entirely alone;
//   - that listener goes away with the dialog;
//   - the description id is per instance, because a hardcoded one would have
//     the second dialog describing the first one's consequence.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import ConfirmDialog from "~/components/library/ConfirmDialog";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function escapeKey(composing = false): KeyboardEvent {
  return new KeyboardEvent("keydown", {
    key: "Escape",
    isComposing: composing,
    bubbles: true,
    cancelable: true,
  });
}

describe("ConfirmDialog", () => {
  let confirmed = 0;
  let closed = 0;
  const mounted: Array<{ host: HTMLDivElement; dispose: () => void }> = [];

  function mount(over: { title?: string; message?: string } = {}): HTMLElement {
    const host = document.createElement("div");
    document.body.appendChild(host);
    const dispose = render(
      () =>
        ConfirmDialog({
          eyebrow: "Book",
          title: over.title ?? "Delete “Dune”",
          message:
            over.message ??
            "This deletes the .epub file from your Library folder. It cannot be undone.",
          confirmLabel: "Delete book",
          onconfirm: () => {
            confirmed += 1;
          },
          onclose: () => {
            closed += 1;
          },
        }),
      host,
    );
    mounted.push({ host, dispose });
    return host;
  }

  // Each case unmounts its own dialog rather than leaving several traps live at
  // once, which is a state the app never produces.
  function unmountLast(): void {
    const entry = mounted.pop();
    entry?.dispose();
    flush();
    entry?.host.remove();
  }

  function el(host: HTMLElement, sel: string): HTMLElement {
    const found = host.querySelector<HTMLElement>(sel);
    if (!found) throw new Error(`${sel} missing`);
    return found;
  }

  function actions(host: HTMLElement): HTMLButtonElement[] {
    return Array.from(
      host.querySelectorAll<HTMLButtonElement>(".cfm-actions button"),
    );
  }

  function action(host: HTMLElement, label: string): HTMLButtonElement {
    const found = actions(host).find((b) => b.textContent?.trim() === label);
    if (!found) throw new Error(`no ${label} action`);
    return found;
  }

  beforeEach(() => {
    confirmed = 0;
    closed = 0;
  });

  afterEach(() => {
    while (mounted.length > 0) unmountLast();
  });

  it("is a labelled modal whose description is the consequence", async () => {
    const host = mount();
    await settle();

    const sheet = el(host, ".cfm-sheet");
    expect(sheet.getAttribute("role")).toBe("dialog");
    expect(sheet.getAttribute("aria-modal")).toBe("true");
    expect(sheet.getAttribute("aria-label")).toBe("Delete “Dune”");

    const describedBy = sheet.getAttribute("aria-describedby");
    const message = describedBy ? document.getElementById(describedBy) : null;
    expect(message?.textContent).toContain("cannot be undone");

    // No form here, so every button says so rather than relying on the default.
    expect(
      Array.from(host.querySelectorAll("button")).every(
        (b) => b.type === "button",
      ),
    ).toBe(true);
  });

  it("mints its description id per instance", async () => {
    const ids: Array<string | null> = [];
    for (let i = 0; i < 2; i += 1) {
      const host = mount();
      await settle();
      ids.push(el(host, ".cfm-sheet").getAttribute("aria-describedby"));
      unmountLast();
    }

    expect(ids[0]).toBeTruthy();
    expect(ids[0]).not.toBe(ids[1]);
  });

  it("opens with focus on Cancel, so a stray Enter cannot delete", async () => {
    const host = mount();
    await settle();

    // Deliberate: left alone, the focus trap takes the first focusable in the
    // sheet, which is the header close button.
    expect(document.activeElement).toBe(action(host, "Cancel"));
    expect(actions(host).map((b) => b.textContent?.trim())).toEqual([
      "Cancel",
      "Delete book",
    ]);
  });

  it("runs the action once and then dismisses itself", async () => {
    const host = mount();
    await settle();

    action(host, "Delete book").click();
    await settle();

    // One exit path per outcome, so a host only drops its pending request.
    expect(confirmed).toBe(1);
    expect(closed).toBe(1);
  });

  it("dismisses from Cancel, the close button and the backdrop without acting", async () => {
    const dismissals: Array<(host: HTMLElement) => void> = [
      (host) => action(host, "Cancel").click(),
      (host) => el(host, ".cfm-close").click(),
      (host) => el(host, ".backdrop-dismiss").click(),
    ];

    for (const dismiss of dismissals) {
      const host = mount();
      await settle();
      dismiss(host);
      await settle();
      unmountLast();
    }

    expect(closed).toBe(3);
    expect(confirmed).toBe(0);
  });

  it("consumes Escape, ignores a composing one, and stops listening when gone", async () => {
    mount();
    await settle();

    const pageHandler = vi.fn();
    window.addEventListener("keydown", pageHandler);
    try {
      const target = document.activeElement ?? document.body;

      // A composing Escape belongs to the IME candidate window, so the dialog
      // does not touch it -- it reaches the page like any other key.
      target.dispatchEvent(escapeKey(true));
      await settle();
      expect(closed).toBe(0);
      expect(pageHandler).toHaveBeenCalledTimes(1);

      const survived = target.dispatchEvent(escapeKey());
      await settle();
      expect(closed).toBe(1);
      expect(confirmed).toBe(0);
      // The real one is consumed from a capture listener, so the page's own
      // window handlers never see it and cannot act on the same keystroke.
      expect(survived).toBe(false);
      expect(pageHandler).toHaveBeenCalledTimes(1);

      unmountLast();
      document.body.dispatchEvent(escapeKey());
      await settle();
      expect(closed).toBe(1);
    } finally {
      window.removeEventListener("keydown", pageHandler);
    }
  });
});
