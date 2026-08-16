// Suite for the library masthead's profile menu. Only the two singletons the
// component reaches for are stubbed -- session (network) and toast (global
// UI); the icons render for real, since the trigger's accessible name is
// assembled around them.
//
// The invariants, each of which regressed silently before this suite existed:
//   - Focus enters the menu on open. The first item's self-focusing ref could
//     never do it: Solid runs element refs while the node is still detached,
//     so .focus() no-oped, the active element stayed on the trigger, the
//     roving arrow keys were unreachable, and aria-expanded="true" asserted a
//     focus move that had not happened.
//   - Escape closes the menu from wherever focus sits, through a window
//     listener in the BUBBLE phase. Every overlay that can stack above this
//     menu (z-index 60 against its 21) registers a CAPTURE keydown listener
//     that calls stopImmediatePropagation; bubbling is what lets the topmost
//     surface keep its own Escape. A capture listener here would register
//     first, run first, and strand the dialog on top.
//   - An Escape that ends an IME composition is not a dismissal, on either
//     Escape path.
//   - Tab leaves the menu rather than wrapping inside it (WCAG 2.1.2 / APG).
//   - Focus leaving the menu dismisses it, but a null relatedTarget -- the
//     document itself losing focus -- does not.
//   - A sign-out whose request failed says so. Local teardown runs either
//     way, so the silent catch made the failure invisible.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import { ApiError } from "~/api/client";

const stubs = vi.hoisted(() => ({
  logout: vi.fn(),
  toasts: [] as string[],
  profile: "Alice" as string | null,
}));

vi.mock("~/lib/session", () => ({
  session: {
    get profile(): string | null {
      return stubs.profile;
    },
    logout: stubs.logout,
  },
}));

vi.mock("~/lib/toast", () => ({
  toast: {
    show: (message: string) => {
      stubs.toasts.push(message);
    },
  },
}));

import ProfileMenu from "~/components/library/ProfileMenu";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("ProfileMenu", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let outside: HTMLButtonElement;
  let clones: number;
  let deletes: number;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    // A real element outside the menu: focus-out dismissal is a statement
    // about where focus went, so relatedTarget has to be a live node.
    outside = document.createElement("button");
    document.body.appendChild(outside);
    clones = 0;
    deletes = 0;
    stubs.profile = "Alice";
    stubs.logout.mockReset();
    stubs.logout.mockResolvedValue(undefined);
    stubs.toasts.length = 0;
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    outside.remove();
    vi.restoreAllMocks();
  });

  async function mount(): Promise<void> {
    dispose = render(
      () =>
        ProfileMenu({
          onclone: () => {
            clones += 1;
          },
          ondelete: () => {
            deletes += 1;
          },
        }),
      container,
    );
    await settle();
  }

  const trigger = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".pm-trigger")!;
  const menu = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".pm-menu");
  const items = (): HTMLButtonElement[] =>
    Array.from(container.querySelectorAll<HTMLButtonElement>(".pm-item"));

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

  it("moves focus into the menu on open, onto the nominated item", async () => {
    await mount();
    await openMenu();

    const first = items()[0]!;
    expect(items()).toHaveLength(3);
    expect(first.getAttribute("tabindex")).toBe("0");
    expect(document.activeElement).toBe(first);
    expect(document.activeElement).not.toBe(trigger());
  });

  it("roves focus with the arrow keys once focus is inside", async () => {
    await mount();
    await openMenu();

    key(document.activeElement!, { key: "ArrowDown" });
    await settle();
    expect(document.activeElement).toBe(items()[1]);

    key(document.activeElement!, { key: "End" });
    await settle();
    expect(document.activeElement).toBe(items()[2]);

    key(document.activeElement!, { key: "Home" });
    await settle();
    expect(document.activeElement).toBe(items()[0]);
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
      key(items()[0]!, { key: "Escape" });
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

    items()[0]!.dispatchEvent(composing);
    await settle();
    expect(menu()).not.toBeNull();

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

    key(items()[0]!, { key: "Tab" });
    await settle();

    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("dismisses when focus leaves the menu entirely", async () => {
    await mount();
    await openMenu();

    focusOut(items()[0]!, outside);
    await settle();

    expect(menu()).toBeNull();
    // Focus was already on its way somewhere else; do not yank it back.
    expect(document.activeElement).not.toBe(trigger());
  });

  it("stays open when the document itself loses focus", async () => {
    await mount();
    await openMenu();

    focusOut(items()[0]!, null);
    await settle();

    expect(menu()).not.toBeNull();
  });

  it("stays open while focus moves between its own parts", async () => {
    await mount();
    await openMenu();

    focusOut(items()[0]!, items()[1]!);
    await settle();
    expect(menu()).not.toBeNull();

    focusOut(items()[1]!, trigger());
    await settle();
    expect(menu()).not.toBeNull();
  });

  it("closes and restores the trigger before running an action", async () => {
    await mount();
    await openMenu();

    items()[0]!.click();
    await settle();

    expect(clones).toBe(1);
    expect(menu()).toBeNull();
    // The dialog the action opens snapshots activeElement on mount, so the
    // trigger has to be restored before the action runs, never after.
    expect(document.activeElement).toBe(trigger());

    await openMenu();
    items()[1]!.click();
    await settle();

    expect(deletes).toBe(1);
    expect(menu()).toBeNull();
  });

  // The fixture is an ApiError because that is the only shape the client can
  // produce: apiLogout wraps every transport failure as
  // ApiError(networkErrorMessage(e), undefined, "network_error"). A bare Error
  // here would pin a message getErrorMessage deliberately refuses to display.
  it("reports a sign-out whose request failed", async () => {
    stubs.logout.mockRejectedValue(
      new ApiError("Can't reach the server", undefined, "network_error"),
    );
    await mount();
    await openMenu();

    items()[2]!.click();
    await settle();

    expect(stubs.logout).toHaveBeenCalledTimes(1);
    expect(stubs.toasts).toEqual(["Can't reach the server"]);
  });

  it("says nothing when sign-out succeeds", async () => {
    await mount();
    await openMenu();

    items()[2]!.click();
    await settle();

    expect(stubs.logout).toHaveBeenCalledTimes(1);
    expect(stubs.toasts).toEqual([]);
  });

  it("labels the menu with the trigger that names the profile", async () => {
    await mount();
    await openMenu();

    expect(trigger().id).toBe("pm-trigger");
    expect(menu()!.getAttribute("aria-labelledby")).toBe("pm-trigger");
    expect(menu()!.getAttribute("aria-label")).toBeNull();
    expect(trigger().getAttribute("aria-label")).toBe("Profile: Alice");
  });
});
