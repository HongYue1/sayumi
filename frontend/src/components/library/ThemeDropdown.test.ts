// Suite for the masthead theme dropdown. Stubbed: settings (the store the
// swatches read and write) and the custom-theme registry (network). THEMES
// stays real -- the focus-entry
// tests are statements about the real catalogue. focusTrap is not in play
// here: this menu has no trap, which is exactly why focus used to stay on
// the trigger.
//
// The invariants, each of which regressed silently before this suite existed:
//   - Focus enters the menu on open, onto the swatch the markup nominates
//     with tabindex="0" (the active theme, or the first light swatch when a
//     custom theme is active). The swatches' self-focusing refs ran while
//     the node was still detached, so focus stayed on the trigger and the
//     menu-scoped roving arrow keys were unreachable dead code.
//   - Escape closes the menu from wherever focus sits, through a window
//     listener in the BUBBLE phase -- menus bubble, dialogs capture, the
//     same split as BookCard and ProfileMenu.
//   - An Escape that ends an IME composition is not a dismissal, on either
//     Escape path.
//   - Tab leaves the menu rather than wrapping inside it (WCAG 2.1.2 / APG).
//   - Focus leaving the dropdown dismisses it, but a null relatedTarget --
//     the document itself losing focus -- does not.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { THEMES } from "~/lib/themes";

const stubs = vi.hoisted(() => ({
  update: vi.fn(),
  loadCustom: vi.fn(),
  customGet: vi.fn(),
  state: {
    theme: "light",
    customLoaded: true,
  },
}));

vi.mock("~/lib/settings", () => ({
  settings: {
    get value() {
      return { theme: stubs.state.theme };
    },
    update: stubs.update,
  },
}));

vi.mock("~/lib/customThemes", () => ({
  customThemes: {
    get loaded(): boolean {
      return stubs.state.customLoaded;
    },
    get: stubs.customGet,
    load: stubs.loadCustom,
  },
}));

import ThemeDropdown from "~/components/library/ThemeDropdown";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("ThemeDropdown", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let outside: HTMLButtonElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    // A real element outside the dropdown: focus-out dismissal is a
    // statement about where focus went, so relatedTarget has to be a live
    // node.
    outside = document.createElement("button");
    document.body.appendChild(outside);
    stubs.state.theme = "light";
    stubs.state.customLoaded = true;
    stubs.update.mockReset();
    stubs.loadCustom.mockReset();
    stubs.loadCustom.mockResolvedValue(undefined);
    stubs.customGet.mockReset();
    stubs.customGet.mockReturnValue(undefined);
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    outside.remove();
    vi.restoreAllMocks();
  });

  async function mount(): Promise<void> {
    dispose = render(() => ThemeDropdown(), container);
    await settle();
  }

  const trigger = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".td-trigger")!;
  const menu = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".td-menu");
  const items = (): HTMLButtonElement[] =>
    Array.from(container.querySelectorAll<HTMLButtonElement>(".td-pick"));
  const swatch = (label: string): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(
      `.td-pick[aria-label="${label}"]`,
    )!;

  async function openMenu(): Promise<void> {
    trigger().click();
    await settle();
  }

  function key(target: EventTarget, init: KeyboardEventInit): void {
    target.dispatchEvent(
      new KeyboardEvent("keydown", {
        bubbles: true,
        cancelable: true,
        ...init,
      }),
    );
  }

  function focusOut(from: Element, related: EventTarget | null): void {
    from.dispatchEvent(
      new FocusEvent("focusout", { bubbles: true, relatedTarget: related }),
    );
  }

  it("opens with focus on the active theme's swatch", async () => {
    await mount();
    await openMenu();

    const nominated = items().find(
      (el) => el.getAttribute("tabindex") === "0",
    )!;
    expect(nominated.getAttribute("aria-label")).toBe("Light");
    expect(document.activeElement).toBe(nominated);
    expect(document.activeElement).not.toBe(trigger());
  });

  it("falls back to the first light swatch when a custom theme is active", async () => {
    stubs.state.theme = "custom:mine";
    stubs.customGet.mockReturnValue({
      id: "custom:mine",
      label: "Mine",
      group: "dark",
      bg: "#000000",
      fg: "#ffffff",
      accent: "#ff00ff",
    });
    await mount();
    await openMenu();

    // No built-in swatch is active, so the markup nominates the first light
    // swatch as the roving-focus entry point.
    expect(document.activeElement).toBe(swatch("Light"));
    expect(document.activeElement).toBe(items()[0]);
  });

  it("roves focus with the arrow keys once focus is inside", async () => {
    const dark = THEMES.find((t) => t.group === "dark")!;
    stubs.state.theme = dark.id;
    await mount();
    await openMenu();

    expect(document.activeElement).toBe(swatch(dark.label));

    key(document.activeElement!, { key: "ArrowDown" });
    await settle();
    const at = items().indexOf(swatch(dark.label));
    expect(document.activeElement).toBe(items()[at + 1]);

    key(document.activeElement!, { key: "Home" });
    await settle();
    expect(document.activeElement).toBe(items()[0]);

    key(document.activeElement!, { key: "End" });
    await settle();
    expect(document.activeElement).toBe(items()[items().length - 1]);
  });

  it("closes on Escape raised outside the menu, restoring the trigger", async () => {
    await mount();
    await openMenu();

    key(outside, { key: "Escape" });
    await settle();

    expect(menu()).toBeNull();
    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(trigger());
  });

  it("leaves Escape to a surface stacked above it", async () => {
    await mount();
    await openMenu();

    // A dialog opening above the menu registers a capture listener that
    // swallows Escape -- CommandPalette, ShortcutsHelp, ShareDialog,
    // EditBookDialog, ProfileDialog and CustomThemeDialog all do exactly
    // that. It necessarily registers second, so only bubbling keeps this
    // menu out of its way.
    const above = vi.fn((e: Event) => {
      e.stopImmediatePropagation();
    });
    window.addEventListener("keydown", above, true);
    try {
      key(document.activeElement!, { key: "Escape" });
      await settle();
    } finally {
      window.removeEventListener("keydown", above, true);
    }

    expect(above).toHaveBeenCalledTimes(1);
    expect(menu()).not.toBeNull();
  });

  it("ignores an Escape that ends an IME composition", async () => {
    await mount();
    await openMenu();

    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    // Guards the environment as much as the component: if the flag does not
    // survive construction, this test proves nothing about the guard.
    expect(composing.isComposing).toBe(true);

    // Menu path: focus sits on a swatch inside the menu.
    document.activeElement!.dispatchEvent(composing);
    await settle();
    expect(menu()).not.toBeNull();

    // Window path: raised from entirely outside the dropdown.
    key(outside, { key: "Escape", isComposing: true });
    await settle();
    expect(menu()).not.toBeNull();

    key(outside, { key: "Escape" });
    await settle();
    expect(menu()).toBeNull();
  });

  it("closes on Tab so focus can leave the menu", async () => {
    await mount();
    await openMenu();

    key(document.activeElement!, { key: "Tab" });
    await settle();

    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("dismisses when focus leaves the dropdown entirely", async () => {
    await mount();
    await openMenu();

    focusOut(document.activeElement!, outside);
    await settle();

    expect(menu()).toBeNull();
    // Focus was already on its way somewhere else; do not yank it back.
    expect(document.activeElement).not.toBe(trigger());
  });

  it("stays open when the document itself loses focus", async () => {
    await mount();
    await openMenu();

    focusOut(document.activeElement!, null);
    await settle();

    expect(menu()).not.toBeNull();
  });

  it("stays open while focus moves between its own parts", async () => {
    await mount();
    await openMenu();

    focusOut(document.activeElement!, trigger());
    await settle();
    expect(menu()).not.toBeNull();

    focusOut(trigger(), swatch("Sepia"));
    await settle();
    expect(menu()).not.toBeNull();
  });

  it("picking a swatch updates settings and closes", async () => {
    await mount();
    await openMenu();

    swatch("Sepia").click();
    await settle();

    expect(stubs.update).toHaveBeenCalledWith({ theme: "sepia" });
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("labels the menu with the trigger that names the current theme", async () => {
    await mount();
    await openMenu();

    expect(trigger().id).toBe("td-trigger");
    expect(menu()!.getAttribute("aria-labelledby")).toBe("td-trigger");
    expect(menu()!.getAttribute("aria-label")).toBeNull();
    expect(trigger().getAttribute("aria-label")).toBe(
      "Change theme (current: Light)",
    );
  });

  it("retries a failed registry load on open", async () => {
    stubs.state.customLoaded = false;
    stubs.loadCustom.mockImplementation(async () => {
      stubs.state.customLoaded = true;
    });
    await mount();
    await openMenu();
    await settle();

    expect(stubs.loadCustom).toHaveBeenCalledTimes(1);
  });

  it("does not re-fetch the registry when it is already loaded", async () => {
    await mount();
    await openMenu();

    expect(stubs.loadCustom).not.toHaveBeenCalled();
  });
});
