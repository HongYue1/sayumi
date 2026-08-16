// Suite for the library masthead's card-size control.
//
// The popover machinery is ProfileMenu's, so these are the same invariants
// that regressed silently there before it had a suite:
//   - Focus enters the popover on open. A self-focusing ref cannot do it:
//     Solid runs refs while the node is still detached, so .focus() no-ops.
//   - Escape and an outside pointerdown dismiss it through WINDOW listeners.
//     A fixed scrim is not an option: the sticky masthead's backdrop-filter
//     establishes a containing block, so the scrim would be clipped to the
//     masthead and never cover the shelf.
//   - Escape is BUBBLE phase, leaving an overlay stacked above this popover
//     (they capture and call stopImmediatePropagation) in charge of its key.
//   - A null relatedTarget -- the document itself losing focus -- is not a
//     dismissal.
//   - Auto is aria-disabled rather than disabled, so resetting the size can
//     never move focus out of the popover the button lives in.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import CardSizeControl from "~/components/library/CardSizeControl";
import { cardSize, CARD_SIZE_SEED } from "~/lib/cardSize";

async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

describe("CardSizeControl", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let outside: HTMLButtonElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    // A real element outside the popover: focus-out and pointerdown dismissal
    // are statements about where the event went.
    outside = document.createElement("button");
    document.body.appendChild(outside);
    cardSize.reset();
    flush();
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    container.remove();
    outside.remove();
    cardSize.reset();
    flush();
    vi.restoreAllMocks();
  });

  const trigger = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".lib-size-trigger")!;
  const pop = (): HTMLElement | null =>
    container.querySelector<HTMLElement>(".lib-size-pop");
  const slider = (): HTMLInputElement =>
    container.querySelector<HTMLInputElement>(".lib-size-range")!;
  const auto = (): HTMLButtonElement =>
    container.querySelector<HTMLButtonElement>(".lib-size-auto")!;
  const readout = (): string =>
    container.querySelector(".lib-size-value")?.textContent ?? "";

  async function mount(): Promise<void> {
    dispose = render(() => CardSizeControl(), container);
    await settle();
  }

  async function openPop(): Promise<void> {
    trigger().click();
    await settle();
  }

  function esc(target: EventTarget, isComposing = false): void {
    target.dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        isComposing,
        bubbles: true,
        cancelable: true,
      }),
    );
  }

  // A plain Event, not a PointerEvent: the handler only reads e.target, and
  // constructing PointerEvent is not worth depending on in the test DOM.
  function pointerDown(target: EventTarget): void {
    target.dispatchEvent(new Event("pointerdown", { bubbles: true }));
  }

  function drag(px: number): void {
    const el = slider();
    el.value = String(px);
    el.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
  }

  it("opens on click, moving focus onto the slider", async () => {
    await mount();
    expect(pop()).toBeNull();
    expect(trigger().getAttribute("aria-expanded")).toBe("false");

    await openPop();
    expect(pop()).not.toBeNull();
    expect(trigger().getAttribute("aria-expanded")).toBe("true");
    expect(document.activeElement).toBe(slider());
  });

  it("names the current size on the trigger and in the readout", async () => {
    await mount();
    await openPop();

    // No preference yet, so the shelf stays fluid and the slider sits at the
    // seed -- the first drag should not jump.
    expect(trigger().getAttribute("aria-label")).toBe("Card size: Auto");
    expect(readout()).toBe("Auto");
    expect(slider().value).toBe(String(CARD_SIZE_SEED));

    drag(200);
    expect(cardSize.value).toBe(200);
    expect(readout()).toBe("200px");
    expect(trigger().getAttribute("aria-label")).toBe("Card size: 200px");
  });

  it("returns to the fluid default through Auto", async () => {
    await mount();
    await openPop();
    drag(200);
    await settle();
    expect(auto().getAttribute("aria-disabled")).toBe("false");

    auto().click();
    await settle();
    expect(cardSize.value).toBeNull();
    expect(readout()).toBe("Auto");

    // aria-disabled, never disabled: a real disabled attribute would have
    // blurred the button the instant the reset landed, dropping focus out of
    // the popover and closing it through the focus-out guard.
    expect(auto().getAttribute("aria-disabled")).toBe("true");
    expect(auto().hasAttribute("disabled")).toBe(false);
    expect(pop()).not.toBeNull();

    auto().click();
    await settle();
    expect(cardSize.value).toBeNull();
  });

  it("closes on Escape raised outside it, restoring the trigger", async () => {
    await mount();
    await openPop();

    esc(outside);
    await settle();
    expect(pop()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("leaves Escape to a surface stacked above it", async () => {
    await mount();
    await openPop();

    const above = vi.fn((e: Event) => {
      e.stopImmediatePropagation();
    });
    window.addEventListener("keydown", above, true);
    try {
      esc(slider());
      await settle();
    } finally {
      window.removeEventListener("keydown", above, true);
    }

    expect(above).toHaveBeenCalledTimes(1);
    expect(pop()).not.toBeNull();
  });

  it("ignores an Escape that ends an IME composition", async () => {
    await mount();
    await openPop();

    esc(outside, true);
    await settle();
    expect(pop()).not.toBeNull();

    esc(outside);
    await settle();
    expect(pop()).toBeNull();
  });

  it("dismisses on an outside pointerdown, not on its own", async () => {
    await mount();
    await openPop();

    pointerDown(slider());
    await settle();
    expect(pop()).not.toBeNull();

    pointerDown(trigger());
    await settle();
    expect(pop()).not.toBeNull();

    pointerDown(outside);
    await settle();
    expect(pop()).toBeNull();
    // Focus was not taken from wherever the pointer went.
    expect(document.activeElement).not.toBe(trigger());
  });

  it("stays open when the document itself loses focus", async () => {
    await mount();
    await openPop();

    slider().dispatchEvent(
      new FocusEvent("focusout", { bubbles: true, relatedTarget: null }),
    );
    await settle();
    expect(pop()).not.toBeNull();

    slider().dispatchEvent(
      new FocusEvent("focusout", { bubbles: true, relatedTarget: outside }),
    );
    await settle();
    expect(pop()).toBeNull();
  });
});
