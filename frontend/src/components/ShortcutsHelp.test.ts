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
import { readFileSync } from "node:fs";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import ShortcutsHelp from "~/components/ShortcutsHelp";
import { ui } from "~/lib/ui";

// Read.tsx owns the reader keymap; this sheet is a hand-written mirror of it.
// Read the route as text rather than importing it: the keymap is a closure
// inside the component, and the switch is the contract.
const readerSource = readFileSync("src/routes/Read.tsx", "utf8");

// Caps that qualify the cap beside them instead of naming a key of their own.
const MODIFIER_CAPS = new Set(["Shift", "Ctrl / \u2318"]);

// e.key -> the cap this sheet prints for it. Anything absent prints its own
// name, except single characters, which differ from their cap only in case
// ("t" and "T" are one documented row).
const CAP_FOR_KEY: Record<string, string> = {
  " ": "Space",
  Escape: "Esc",
  ArrowLeft: "\u2190",
  ArrowRight: "\u2192",
  ArrowUp: "\u2191",
  ArrowDown: "\u2193",
};

function capFor(key: string): string {
  return CAP_FOR_KEY[key] ?? (key.length === 1 ? key.toUpperCase() : key);
}

// The e.key values handleKeyAction's switch acts on. Scoped to that switch:
// the stand-down list above it names the same keys for a different purpose.
function boundKeys(): string[] {
  const handler = readerSource.indexOf("function handleKeyAction");
  const start = readerSource.indexOf("switch (e.key) {", handler);
  const end = readerSource.indexOf("\n    }", start);
  expect(handler, "handleKeyAction").toBeGreaterThan(-1);
  expect(start, "switch (e.key)").toBeGreaterThan(handler);
  expect(end, "end of switch").toBeGreaterThan(start);
  return Array.from(
    readerSource.slice(start, end).matchAll(/case "(.*)":/gu),
    (match) => match[1] ?? "",
  );
}

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
    expect(rows()).toHaveLength(19);
    expect(caps).toHaveLength(23);
  });

  it("documents every key the reader keymap binds", async () => {
    mount();
    ui.openShortcuts();
    await settle();

    // Nothing but this test connects the sheet to the keymap, and the pair had
    // already drifted: paged mode bound the vertical arrows and the sheet
    // documented neither. Derived from the switch on purpose -- a second
    // hardcoded list would drift exactly the same way.
    const bound = new Set(boundKeys().map(capFor));
    // Ctrl/Cmd+K is claimed before the switch, so the scan cannot see it.
    expect(readerSource).toContain('e.key === "k" || e.key === "K"');
    bound.add("K");

    const documented = keycaps().filter((cap) => !MODIFIER_CAPS.has(cap));

    expect([...new Set(documented)].sort()).toEqual([...bound].sort());
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

// A wrapped description used to swallow the whole line box, leaving the
// leader as two or three stray dots next to full-width neighbours.
describe("shortcut row leader", () => {
  const css = readFileSync("src/app.css", "utf8");
  const block = (selector: string): string =>
    new RegExp(selector + "\\s*\\{([^}]*)\\}").exec(css)?.[1] ?? "";

  it("keeps room for the dots when the description wraps", () => {
    const leader = block("\\.shortcuts-leader");
    const desc = block("\\.shortcuts-desc");
    const minWidth = Number.parseFloat(
      /min-width:\s*([\d.]+)rem/.exec(leader)?.[1] ?? "0",
    );
    const maxWidth = Number.parseFloat(
      /max-width:\s*([\d.]+)%/.exec(desc)?.[1] ?? "100",
    );

    expect(minWidth).toBeGreaterThanOrEqual(2);
    expect(maxWidth).toBeLessThan(100);
  });
});
