// Suite for the modal shortcut reference. Nothing is mocked: the ui store and
// focusTrap are the real modules, and the sheet's only inputs are that store
// flag and window key events.
//
// Three invariants carry the weight here, and all are structural rather than
// visual:
//   - Esc must be consumed in the CAPTURE phase. App's global shortcut handler
//     and Read's reader keys are plain window BUBBLE listeners, so a
//     regression to bubble -- or a dropped stopImmediatePropagation -- would
//     let one Esc close the sheet AND navigate the reader back to the library.
//     Every test registers its bubble listener BEFORE the sheet opens, which
//     is the arrangement a bubble-phase implementation cannot win. A composing
//     Escape is the exception: it remains open and lets the event pass through.
//   - The dotted leader lives inside <dd>, because a div row inside a <dl> may
//     contain only <dt>s followed by <dd>s.
//   - Focus placement belongs to focusTrap, in a queued microtask. Refs fire
//     while their element is still detached, so a self-focusing ref is a
//     no-op. The focus test pins that
//     ordering deliberately: the opener still holds focus synchronously after
//     mount, the close button holds it one tick later, and the opener gets it
//     back on close.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import ShortcutsHelp from "~/components/ShortcutsHelp";
import { ui } from "~/lib/ui";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("ShortcutsHelp", () => {
  let container: HTMLDivElement;
  let opener: HTMLButtonElement;
  let dispose: (() => void) | undefined;
  let bubbled: string[] = [];

  function bubbleSpy(e: Event): void {
    bubbled.push((e as KeyboardEvent).key);
  }

  function mount(): void {
    dispose = render(() => ShortcutsHelp(), container);
  }

  function sheet(): HTMLElement | null {
    return container.querySelector(".shortcuts-sheet");
  }

  function rows(): HTMLElement[] {
    return Array.from(container.querySelectorAll(".shortcuts-row"));
  }

  function keycaps(): string[] {
    return Array.from(container.querySelectorAll("kbd")).map(
      (el) => el.textContent ?? "",
    );
  }

  function press(key: string): KeyboardEvent {
    const event = new KeyboardEvent("keydown", {
      key,
      bubbles: true,
      cancelable: true,
    });
    window.dispatchEvent(event);
    return event;
  }

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();
    bubbled = [];
    // Installed before the sheet opens on purpose: only a capture-phase
    // listener can beat a bubble listener that is already registered.
    window.addEventListener("keydown", bubbleSpy);
  });

  afterEach(() => {
    window.removeEventListener("keydown", bubbleSpy);
    dispose?.();
    dispose = undefined;
    ui.closeOverlays();
    flush();
    container.remove();
    opener.remove();
  });

  it("stays unmounted until the ui store opens it", async () => {
    mount();
    await settle();
    expect(sheet()).toBeNull();

    ui.openShortcuts();
    await settle();
    expect(sheet()).not.toBeNull();

    ui.closeOverlays();
    await settle();
    expect(sheet()).toBeNull();
  });

  it("keeps the leader inside the description, not between dt and dd", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    expect(container.querySelectorAll("dd > .shortcuts-leader")).toHaveLength(
      rows().length,
    );
    expect(
      container.querySelectorAll(".shortcuts-row > .shortcuts-leader"),
    ).toHaveLength(0);
    for (const row of rows()) {
      expect(Array.from(row.children).map((child) => child.tagName)).toEqual([
        "DT",
        "DD",
      ]);
    }
  });

  it("documents the paging keys the reader actually binds", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    const caps = keycaps();
    for (const key of ["Space", "PageDown", "PageUp", "Home", "End", "Shift"]) {
      expect(caps).toContain(key);
    }
    expect(rows()).toHaveLength(16);
    expect(caps).toHaveLength(19);
  });

  it("consumes Escape before window bubble handlers", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    const escape = press("Escape");
    await settle();
    expect(sheet()).toBeNull();
    expect(escape.defaultPrevented).toBe(true);
    expect(bubbled).toEqual([]);
  });

  it("does not consume a composing Escape", async () => {
    mount();
    ui.openShortcuts();
    await settle();
    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    expect(composing.isComposing).toBe(true);

    window.dispatchEvent(composing);
    await settle();

    expect(sheet()).not.toBeNull();
    expect(composing.defaultPrevented).toBe(false);
    expect(bubbled).toEqual(["Escape"]);
  });

  it("leaves other keys alone while open", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    press("Enter");
    await settle();
    expect(sheet()).not.toBeNull();
    expect(bubbled).toEqual(["Enter"]);
  });

  it("listens only while it is open", async () => {
    mount();
    await settle();
    press("Escape");
    await settle();
    expect(bubbled).toEqual(["Escape"]);

    ui.openShortcuts();
    await settle();
    ui.closeOverlays();
    await settle();

    bubbled = [];
    press("Escape");
    await settle();
    expect(bubbled).toEqual(["Escape"]);
  });

  it("drops its listener on dispose, even while open", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    dispose?.();
    dispose = undefined;
    await settle();

    press("Escape");
    await settle();
    // A leaked capture listener would swallow this Escape and clear the store.
    expect(bubbled).toEqual(["Escape"]);
    expect(ui.shortcuts).toBe(true);
  });

  it("focuses the close button on open and restores focus to the opener", async () => {
    mount();
    await settle();
    expect(document.activeElement).toBe(opener);

    ui.openShortcuts();
    flush();
    const closeBtn = container.querySelector(".shortcuts-close");
    expect(closeBtn).not.toBeNull();
    // Nothing in this component focuses during render, and that is deliberate:
    // a ref runs while its element is still detached, where focus() is a
    // no-op. Focus placement belongs to focusTrap's queued microtask, one tick
    // later -- so at this instant focus is still on the opener.
    expect(document.activeElement).toBe(opener);

    await settle();
    expect(document.activeElement).toBe(closeBtn);

    ui.closeOverlays();
    await settle();
    expect(document.activeElement).toBe(opener);
  });

  it("closes when the backdrop is clicked", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    container.querySelector<HTMLButtonElement>(".backdrop-dismiss")?.click();
    await settle();
    expect(sheet()).toBeNull();
  });
});
