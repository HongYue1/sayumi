// Suite for the custom theme create/edit dialog. Stubbed: the customThemes
// store (network), settings (the store the dialog reads and writes), and
// theme state. THEMES, autoAccent and
// themeGroupFor stay real -- the seeding assertions are statements about the
// real catalogue. focusTrap is real: this dialog mounts inside its trap.
//
// The invariants, four of which regressed silently before this suite existed:
//   - Focus lands on the name field on open. A self-focusing ref runs
//     while the node is still detached, so the trap's fallback
//     took the first focusable in the sheet -- the header close button, where
//     Enter dismisses the dialog.
//   - An Escape that ends an IME composition is not a dismissal, even though
//     the dialog's window listener is capture-phase (it runs before Read's).
//   - Busy controls use aria-disabled, never a real disabled attribute: the
//     pressed control (or the field that submitted with Enter) must not blur
//     mid-request. save()/remove() guard the busy state handler-side.
//   - The two-click delete disarms on a 3s timer, mirroring SettingsPanel's
//     resetArmed -- an indefinitely armed delete is one stray click from
//     deleting a theme.
//   - The sheet portals to document.body. Rendered in place it lands inside the
//     settings panel, whose backdrop-filter blurs it AND makes the panel the
//     containing block for the position: fixed overlay, so the "whole screen"
//     veil covered the panel only. Nothing it renders is inside `container`.
//   - Colors are typeable, not picker-only: Firefox's native color input has no
//     hex/RGB entry at all. Only a COMPLETE color commits, so a half-typed
//     "#12" is neither applied nor rewritten under the caret.
//   - The picked palette publishes to lib/themePreview (the live preview both
//     painters follow) and is cleared on unmount -- the one point every
//     dismissal path (cancel, Escape, save, delete) converges on.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { themePreview } from "~/lib/themePreview";
import { THEMES, autoAccent, type ThemeDef } from "~/lib/themes";

const stubs = vi.hoisted(() => ({
  create: vi.fn(),
  update: vi.fn(),
  remove: vi.fn(),
  settingsUpdate: vi.fn(),
  state: {
    theme: "light",
  },
}));

vi.mock("~/lib/customThemes", () => ({
  customThemes: {
    create: stubs.create,
    update: stubs.update,
    remove: stubs.remove,
  },
}));

vi.mock("~/lib/settings", () => ({
  settings: {
    get value() {
      return { theme: stubs.state.theme };
    },
    update: stubs.settingsUpdate,
  },
}));

import CustomThemeDialog from "~/components/reader/CustomThemeDialog";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

const BASE: ThemeDef = {
  id: "light",
  label: "Light",
  group: "light",
  bg: "#ffffff",
  fg: "#111111",
  accent: "#2563eb",
};

const EDIT: ThemeDef = {
  id: "custom:abc",
  label: "Mine",
  group: "dark",
  bg: "#101010",
  fg: "#eeeeee",
  accent: "#60a5fa",
};

describe("CustomThemeDialog", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let onclose: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    onclose = vi.fn();
    stubs.state.theme = "light";
    stubs.create.mockReset();
    stubs.update.mockReset();
    stubs.remove.mockReset();
    stubs.settingsUpdate.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    dispose?.();
    dispose = undefined;
    container.remove();
    vi.restoreAllMocks();
  });

  async function mount(edit: ThemeDef | null = null): Promise<void> {
    dispose = render(
      () =>
        CustomThemeDialog({
          base: BASE,
          edit,
          onclose: onclose as () => void,
        }),
      container,
    );
    await settle();
  }

  // Every query goes through `document`, not `container`: the dialog portals
  // to document.body, so container only anchors the render root's owner.
  const nameField = (): HTMLInputElement =>
    document.querySelector<HTMLInputElement>(
      // :not(.ctd-color-text) keeps this unambiguous: each color row is a
      // .ctd-field wrapper with a text input of its own now.
      '.ctd-field input[type="text"]:not(.ctd-color-text)',
    )!;
  const submitBtn = (): HTMLButtonElement =>
    document.querySelector<HTMLButtonElement>(".ctd-actions .btn")!;
  const deleteBtn = (): HTMLButtonElement =>
    document.querySelector<HTMLButtonElement>(".ctd-danger-ghost")!;
  const autoCheck = (): HTMLInputElement =>
    document.querySelector<HTMLInputElement>(".ctd-check input")!;
  const picker = (label: string): HTMLInputElement =>
    document.querySelector<HTMLInputElement>(
      `input[aria-label="${label} color"]`,
    )!;
  const colorText = (label: string): HTMLInputElement =>
    document.querySelector<HTMLInputElement>(
      `input[aria-label="${label} color value, hex or rgb()"]`,
    )!;
  const accentField = (): HTMLInputElement | null =>
    document.querySelector<HTMLInputElement>(
      'input[aria-label="Accent color"]',
    );
  const errEl = (): HTMLElement | null =>
    document.querySelector<HTMLElement>("#theme-name-error");

  function typeInto(field: HTMLInputElement, value: string): void {
    field.value = value;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  it("focuses the name field on open (create mode)", async () => {
    await mount();
    expect(document.activeElement).toBe(nameField());
    expect(nameField().value).toBe("");
  });

  it("focuses the seeded name field on open (edit mode)", async () => {
    await mount(EDIT);
    expect(document.activeElement).toBe(nameField());
    expect(nameField().value).toBe("Mine");
  });

  it("lets an IME composition Escape pass, but closes on a real Escape", async () => {
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
    nameField().dispatchEvent(composing);
    await settle();
    expect(onclose).not.toHaveBeenCalled();
    expect(nameField().isConnected).toBe(true);

    window.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }),
    );
    await settle();
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("keeps focus on the pressed submit and guards re-entry while saving", async () => {
    stubs.create.mockImplementation(
      () =>
        new Promise<ThemeDef>((resolve) => {
          setTimeout(
            () =>
              resolve({
                id: "custom:new",
                label: "Fresh",
                group: "light",
                bg: "#ffffff",
                fg: "#111111",
                accent: "#2563eb",
              }),
            50,
          );
        }),
    );
    await mount();
    typeInto(nameField(), "Fresh");
    const btn = submitBtn();
    // happy-dom's click() does not move focus; a real user's pressed control
    // has focus, so place it there before activating.
    btn.focus();
    btn.click();
    await settle();
    // In flight: the button says so with aria-disabled, not a real disabled
    // attribute, and the pressed control keeps focus.
    expect(btn.getAttribute("aria-disabled")).toBe("true");
    expect(btn.hasAttribute("disabled")).toBe(false);
    expect(nameField().getAttribute("aria-disabled")).toBe("true");
    expect(nameField().hasAttribute("disabled")).toBe(false);
    expect(nameField().hasAttribute("readonly")).toBe(true);
    expect(document.activeElement).toBe(btn);
    // A second activation while busy reaches the handler and is guarded.
    btn.click();
    await settle();
    expect(stubs.create).toHaveBeenCalledTimes(1);
    // Settle the write: the dialog closes and applies the new theme.
    await new Promise((resolve) => setTimeout(resolve, 80));
    await settle();
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(stubs.settingsUpdate).toHaveBeenCalledWith({ theme: "custom:new" });
  });

  it("shows the name-cap error live and a blocked submit is never silent", async () => {
    await mount();
    // 61 code points, over the 60-char cap the server enforces at
    // internal/api/customthemes.go (maxThemeNameLen). maxlength is 120 units
    // of slack, so this types fine and only the validator can stop it.
    typeInto(nameField(), "x".repeat(61));
    const err = errEl();
    expect(err?.getAttribute("role")).toBe("alert");
    expect(err?.textContent).toContain("at most 60 characters");
    expect(nameField().getAttribute("aria-describedby")).toBe(
      "theme-name-error",
    );
    submitBtn().click();
    await settle();
    expect(stubs.create).not.toHaveBeenCalled();
    typeInto(nameField(), "Short name");
    expect(errEl()).toBeNull();
  });

  it("seeds the manual accent picker from the current auto suggestion", async () => {
    await mount();
    expect(accentField()).toBeNull();
    autoCheck().checked = false;
    autoCheck().dispatchEvent(new Event("change", { bubbles: true }));
    await settle();
    const accent = accentField();
    expect(accent).not.toBeNull();
    expect(accent!.value).toBe(autoAccent(BASE.bg, BASE.fg));
  });

  it("arms the delete on first click and disarms it on a 3s timer", async () => {
    vi.useFakeTimers();
    await mount(EDIT);
    const btn = deleteBtn();
    btn.click();
    await settle();
    expect(btn.textContent).toContain("Click again to delete");
    expect(stubs.remove).not.toHaveBeenCalled();
    // The armed state expires, mirroring SettingsPanel's resetArmed.
    vi.advanceTimersByTime(3100);
    await settle();
    expect(btn.textContent).not.toContain("Click again to delete");
    // Re-arm, then confirm inside the window: exactly one remove call.
    btn.click();
    await settle();
    btn.click();
    await settle();
    expect(stubs.remove).toHaveBeenCalledTimes(1);
    expect(stubs.remove.mock.calls[0]![0]).toBe("custom:abc");
  });

  it("falls back to a built-in of the same group when deleting the active theme", async () => {
    stubs.state.theme = "custom:abc";
    stubs.remove.mockResolvedValue(true);
    await mount(EDIT);
    deleteBtn().click();
    await settle();
    deleteBtn().click();
    await settle();
    const fallback = THEMES.find((th) => th.group === "dark")!.id;
    expect(stubs.settingsUpdate).toHaveBeenCalledWith({ theme: fallback });
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("stays open and re-arms the form when a create fails", async () => {
    stubs.create.mockResolvedValue(null);
    await mount();
    typeInto(nameField(), "Fresh");
    submitBtn().click();
    await settle();
    expect(stubs.create).toHaveBeenCalledTimes(1);
    expect(onclose).not.toHaveBeenCalled();
    expect(submitBtn().getAttribute("aria-disabled")).not.toBe("true");
  });

  it("portals the overlay to document.body and cleans it up", async () => {
    await mount();
    const overlay = document.querySelector(".ctd-overlay");
    expect(overlay).not.toBeNull();
    // The point of the portal: the settings panel subtree (here, `container`)
    // must not be an ancestor, or its backdrop-filter blurs this dialog and
    // clips the fixed overlay to the panel's box.
    expect(container.querySelector(".ctd-overlay")).toBeNull();
    expect(container.contains(overlay)).toBe(false);
    dispose?.();
    dispose = undefined;
    flush();
    // The portal owns its mount point: no orphan left on body.
    expect(document.querySelector(".ctd-overlay")).toBeNull();
  });

  it("accepts typed hex and rgb(), and ignores an incomplete value", async () => {
    await mount();
    const field = colorText("Background");
    expect(field.value).toBe("#ffffff");
    typeInto(field, "#0f0");
    expect(picker("Background").value).toBe("#00ff00");
    typeInto(field, "rgb(18, 52, 86)");
    expect(picker("Background").value).toBe("#123456");
    // Half-typed: nothing commits, and the field is NOT rewritten under the
    // caret -- it only reports itself invalid.
    typeInto(field, "#12");
    expect(field.value).toBe("#12");
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(picker("Background").value).toBe("#123456");
    // Blur is where it snaps back to the committed color.
    field.dispatchEvent(new Event("blur", { bubbles: true }));
    await settle();
    expect(field.value).toBe("#123456");
  });

  it("publishes a live preview palette and clears it on unmount", async () => {
    await mount();
    expect(themePreview()?.bg).toBe(BASE.bg);
    typeInto(colorText("Background"), "#101010");
    await settle();
    expect(themePreview()?.bg).toBe("#101010");
    // The group follows the background, so the draft previews as a dark theme.
    expect(themePreview()?.group).toBe("dark");
    // The draft id is not a real theme id: it must not collide with a built-in
    // (the reader derives html.theme-<id> from it) and must never be saved.
    expect(THEMES.some((th) => th.id === themePreview()!.id)).toBe(false);
    expect(stubs.settingsUpdate).not.toHaveBeenCalled();
    dispose?.();
    dispose = undefined;
    flush();
    expect(themePreview()).toBeNull();
  });
});
