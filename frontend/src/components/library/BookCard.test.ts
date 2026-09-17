// Suite for the library grid card. Nothing is mocked: the flair catalogue is
// the real DEFAULT_FLAIRS and every callback is a plain recorder, because the
// card's whole contract is DOM structure plus which handler fires.
//
// The invariants that carry the weight here are all keyboard/AT structural,
// and each one regressed silently before this suite existed:
//   - Focus must enter the popover from the post-flush apply phase, deferred a
//     microtask. Refs fire while their node is still detached, so the items'
//     self-focusing refs were no-ops -- which in turn made the roving
//     arrow-key handler unreachable dead code. The focus tests pin the
//     ordering deliberately: the chip still holds focus synchronously after
//     the menu mounts, and the nominated item holds it one tick later.
//   - The card's other chip is a menu switch, not a dismissal. The window
//     click listener is capture-phase and swallows the dismissing click, so
//     without an explicit exemption for the peer chip a switch cost two
//     activations -- two Enters for a keyboard user, since activating a button
//     dispatches a click through that same listener.
//   - The swallow itself still has to hold for everything else on the card:
//     one click dismisses, and only a second one opens the book.
//   - Flair entries are menuitemcheckbox. Re-picking the current flair clears
//     it, which is a toggle a menuitemradio may not perform. The explicit
//     No flair entry is the discoverable half of that clear: checked exactly
//     when the book carries no flair, and a close-only no-op when activated
//     in that state.
//   - Driving the menus must not log STRICT_READ_UNTRACKED. The dismiss
//     effect's apply phase is an untracked scope, so it resolves its chips
//     through a plain lookup rather than a memo.
//   - The edge-flip is a decision about geometry, so it has to be revisited
//     when the geometry moves. A resize or a shelf scroll re-measures while
//     the popover is open, and the measurement is taken as if the menu were
//     unflipped, so a flip that is no longer needed is given up instead of
//     latching. Switching chip to chip is the case that never passes through
//     the closed state, and so the case a reset-on-close would miss.
//   - A dismissing click leaves focus wherever it legitimately landed, but a
//     click on something unfocusable orphans it on <body>: the item holding
//     focus is removed with the menu, so the chip takes it back.
// Each popover keeps its own ref, so the reset in closeMenu is hygiene with no
// observable behaviour today (a ref is always reassigned on the next open) and
// nothing here asserts it.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import BookCard from "~/components/library/BookCard";
import { DEFAULT_FLAIRS } from "~/lib/flairs";
import type { BookMeta } from "~/api/client";

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

describe("BookCard", () => {
  let container: HTMLDivElement;
  let dispose: (() => void) | undefined;
  let opened: string[];
  let removed: string[];
  let edited: string[];
  let shared: string[];
  let flaired: Array<[string, string | null]>;
  let logged: string[];
  const viewportRestores: Array<() => void> = [];
  let realError: typeof console.error;
  let realWarn: typeof console.warn;
  let realLog: typeof console.log;

  function mount(meta: BookMeta = book()): void {
    dispose = render(
      () =>
        BookCard({
          book: meta,
          flairs: DEFAULT_FLAIRS,
          index: 0,
          onopen: (id) => opened.push(id),
          onremove: (id) => removed.push(id),
          onedit: (id) => edited.push(id),
          onshare: (id) => shared.push(id),
          onsetflair: (bookId, flairId) => flaired.push([bookId, flairId]),
        }),
      container,
    );
  }

  function gear(): HTMLButtonElement {
    const el = container.querySelector<HTMLButtonElement>(".bc-actions-btn");
    if (!el) throw new Error("actions chip missing");
    return el;
  }

  function chip(): HTMLButtonElement {
    const el = container.querySelector<HTMLButtonElement>(".bc-flair-btn");
    if (!el) throw new Error("flair chip missing");
    return el;
  }

  function menu(): HTMLElement | null {
    return container.querySelector<HTMLElement>('[role="menu"]');
  }

  function items(): HTMLButtonElement[] {
    return Array.from(
      container.querySelectorAll<HTMLButtonElement>(".bc-menu-item"),
    );
  }

  function labels(): string[] {
    return items().map(
      (el) => el.querySelector(".bc-menu-label")?.textContent ?? "",
    );
  }

  function item(label: string): HTMLButtonElement {
    const found = items().find(
      (el) => el.querySelector(".bc-menu-label")?.textContent === label,
    );
    if (!found) throw new Error(`no menu item labelled ${label}`);
    return found;
  }

  // The focused item's label, so focus assertions read as menu entries rather
  // than as element identity.
  function focused(): string {
    const el = document.activeElement;
    if (!(el instanceof HTMLElement)) return "none";
    return el.querySelector(".bc-menu-label")?.textContent ?? el.className;
  }

  function press(key: string): void {
    const target = document.activeElement ?? document.body;
    target.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
  }

  // The DOM shim reports a fixed viewport and a zero rect for every element,
  // so the flip tests state the viewport they mean and let the rects stay
  // zero: a 4x4 viewport overflows for any popover, a roomy one for none.
  // innerWidth/innerHeight live on the prototype, so an own value property
  // shadows them and the restore removes it again (reversed, so nested stubs
  // unwind to the original rather than to each other).
  function setViewport(width: number, height: number): void {
    for (const [key, value] of [
      ["innerWidth", width],
      ["innerHeight", height],
    ] as const) {
      const prev = Object.getOwnPropertyDescriptor(window, key);
      viewportRestores.push(() => {
        if (prev) Object.defineProperty(window, key, prev);
        else Reflect.deleteProperty(window, key);
      });
      Object.defineProperty(window, key, {
        configurable: true,
        writable: true,
        value,
      });
    }
  }

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    opened = [];
    removed = [];
    edited = [];
    shared = [];
    flaired = [];
    logged = [];
    // Solid's dev diagnostics are console warnings, not throws, so they are
    // invisible to ordinary assertions unless captured.
    realError = console.error;
    realWarn = console.warn;
    realLog = console.log;
    const capture =
      (next: (...args: unknown[]) => void) =>
      (...args: unknown[]): void => {
        logged.push(args.map((a) => String(a)).join(" "));
        next(...args);
      };
    console.error = capture(realError as (...args: unknown[]) => void);
    console.warn = capture(realWarn as (...args: unknown[]) => void);
    console.log = capture(realLog as (...args: unknown[]) => void);
  });

  afterEach(() => {
    console.error = realError;
    console.warn = realWarn;
    console.log = realLog;
    dispose?.();
    dispose = undefined;
    flush();
    container.remove();
    for (const restore of viewportRestores.splice(0).toReversed()) restore();
  });

  it("renders the caption, the cover and the progress rail", async () => {
    mount();
    await settle();

    expect(container.querySelector(".bc-title")?.textContent).toBe(
      "The Left Hand of Darkness",
    );
    expect(container.querySelector(".bc-author")?.textContent).toBe(
      "Ursula K. Le Guin",
    );
    expect(container.querySelector(".bc-pct")?.textContent).toBe("42%");
    expect(
      container.querySelector(".bc-progress")?.getAttribute("aria-valuenow"),
    ).toBe("42");
    const img = container.querySelector<HTMLImageElement>(".bc-cover img");
    expect(img?.getAttribute("src")).toContain("/books/bk-1/cover?v=");
  });

  it("swaps in the woodcut placeholder when the cover fails", async () => {
    mount();
    await settle();

    const img = container.querySelector<HTMLImageElement>(".bc-cover img");
    expect(img).not.toBeNull();
    img?.dispatchEvent(new Event("error"));
    await settle();

    expect(container.querySelector(".bc-cover img")).toBeNull();
    expect(container.querySelector(".bc-ph-title")?.textContent).toBe(
      "The Left Hand of Darkness",
    );
  });

  it("moves focus into the actions menu one tick after it opens", async () => {
    mount();
    await settle();

    gear().focus();
    gear().click();
    flush();
    // The menu is mounted, but a ref-time focus would already have fired here.
    expect(menu()).not.toBeNull();
    expect(document.activeElement).toBe(gear());

    await settle();
    expect(labels()).toEqual(["Edit", "Share", "Delete"]);
    expect(focused()).toBe("Edit");
  });

  it("opens the flair menu on the checked entry", async () => {
    mount(book({ flairId: "finished" }));
    await settle();

    chip().click();
    await settle();

    expect(labels()).toEqual([
      "No flair",
      "Reading",
      "Finished",
      "Dropped",
      "Plan to Read",
    ]);
    expect(focused()).toBe("Finished");
    expect(item("Finished").getAttribute("aria-checked")).toBe("true");
    expect(item("Reading").getAttribute("aria-checked")).toBe("false");
  });

  it("opens the flair menu on the No flair entry when no flair is set", async () => {
    mount();
    await settle();

    chip().click();
    await settle();

    // The explicit clear affordance doubles as the checked state of record
    // when the book carries no flair, so the menu nominates it for focus.
    expect(focused()).toBe("No flair");
    expect(item("No flair").getAttribute("aria-checked")).toBe("true");
  });

  it("roves focus with the arrows, Home and End", async () => {
    mount();
    await settle();

    gear().click();
    await settle();
    expect(focused()).toBe("Edit");

    press("ArrowDown");
    expect(focused()).toBe("Share");
    press("ArrowDown");
    expect(focused()).toBe("Delete");
    // Wrapping in both directions is the menu keyboard model.
    press("ArrowDown");
    expect(focused()).toBe("Edit");
    press("ArrowUp");
    expect(focused()).toBe("Delete");
    press("Home");
    expect(focused()).toBe("Edit");
    press("End");
    expect(focused()).toBe("Delete");
  });

  it("closes on Escape and hands focus back to the owning chip", async () => {
    mount();
    await settle();

    gear().click();
    await settle();
    expect(focused()).toBe("Edit");

    press("Escape");
    await settle();
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(gear());
    expect(gear().getAttribute("aria-expanded")).toBe("false");
  });

  it("leaves a composing Escape to the IME", async () => {
    mount();
    await settle();

    gear().click();
    await settle();

    // Two handlers see this keystroke -- the menu's own keydown and the
    // window listener an open menu installs. Neither may read it as a
    // dismissal, so press() is bypassed to carry the composing flag.
    (document.activeElement ?? document.body).dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        isComposing: true,
        bubbles: true,
        cancelable: true,
      }),
    );
    await settle();

    expect(menu()).not.toBeNull();
  });

  it("closes on Tab so the key keeps leaving the menu", async () => {
    mount();
    await settle();

    gear().click();
    await settle();
    press("Tab");
    await settle();

    expect(menu()).toBeNull();
  });

  it("switches menus in a single activation on the peer chip", async () => {
    mount();
    await settle();

    chip().click();
    await settle();
    expect(container.querySelector(".bc-flair-menu")).not.toBeNull();

    gear().click();
    await settle();

    expect(container.querySelector(".bc-flair-menu")).toBeNull();
    expect(container.querySelector(".bc-actions-menu")).not.toBeNull();
    expect(gear().getAttribute("aria-expanded")).toBe("true");
    expect(chip().getAttribute("aria-expanded")).toBe("false");
    expect(focused()).toBe("Edit");
  });

  it("swallows the dismissing click instead of opening the book", async () => {
    mount();
    await settle();

    gear().click();
    await settle();

    const overlay =
      container.querySelector<HTMLButtonElement>(".bc-open-overlay");
    overlay?.click();
    await settle();
    expect(menu()).toBeNull();
    expect(opened).toEqual([]);

    // With the menu gone, the same click opens the book.
    overlay?.click();
    await settle();
    expect(opened).toEqual(["bk-1"]);
  });

  it("lets an outside click land on its target while closing the menu", async () => {
    mount();
    await settle();
    // The pass-through doctrine: only the card's own open-book overlay
    // swallows the dismissing click; every other target activates.
    const probe = document.createElement("button");
    let landed = 0;
    probe.addEventListener("click", () => {
      landed += 1;
    });
    document.body.appendChild(probe);
    try {
      gear().click();
      await settle();
      probe.click();
      await settle();
      expect(menu()).toBeNull();
      expect(landed).toBe(1);
    } finally {
      probe.remove();
    }
  });

  it("closes the menu when focus leaves the card", async () => {
    mount();
    await settle();
    gear().click();
    await settle();
    // Focus-out dismissal is a statement about where focus went, so
    // relatedTarget has to be a live node outside the card.
    const outside = document.createElement("button");
    document.body.appendChild(outside);
    try {
      items()[0].dispatchEvent(
        new FocusEvent("focusout", { bubbles: true, relatedTarget: outside }),
      );
      await settle();
      expect(menu()).toBeNull();
    } finally {
      outside.remove();
    }
  });

  it("stays open when the window itself loses focus", async () => {
    mount();
    await settle();
    gear().click();
    await settle();
    // A null relatedTarget is a window blur, not a leave — the menu stays.
    items()[0].dispatchEvent(
      new FocusEvent("focusout", { bubbles: true, relatedTarget: null }),
    );
    await settle();
    expect(menu()).not.toBeNull();
  });

  it("stays open on focus moves within the card", async () => {
    mount();
    await settle();
    gear().click();
    await settle();
    items()[0].dispatchEvent(
      new FocusEvent("focusout", { bubbles: true, relatedTarget: chip() }),
    );
    await settle();
    expect(menu()).not.toBeNull();
  });

  it("points each chip at the popover it owns", async () => {
    mount();
    await settle();

    expect(gear().getAttribute("aria-controls")).toBeNull();
    expect(chip().getAttribute("aria-controls")).toBeNull();

    gear().click();
    await settle();
    expect(gear().getAttribute("aria-controls")).toBe("bc-actions-menu-bk-1");
    expect(container.querySelector(".bc-actions-menu")?.id).toBe(
      "bc-actions-menu-bk-1",
    );

    chip().click();
    await settle();
    expect(chip().getAttribute("aria-controls")).toBe("bc-flair-menu-bk-1");
    expect(container.querySelector(".bc-flair-menu")?.id).toBe(
      "bc-flair-menu-bk-1",
    );
    expect(gear().getAttribute("aria-controls")).toBeNull();
  });

  it("exposes flair entries as checkboxes that can be unchecked", async () => {
    mount(book({ flairId: "finished" }));
    await settle();

    chip().click();
    await settle();
    expect(items().map((el) => el.getAttribute("role"))).toEqual([
      "menuitemcheckbox",
      "menuitemcheckbox",
      "menuitemcheckbox",
      "menuitemcheckbox",
      "menuitemcheckbox",
    ]);

    item("Finished").click();
    await settle();
    expect(flaired).toEqual([["bk-1", null]]);
    expect(menu()).toBeNull();
  });

  it("sets a different flair when another entry is picked", async () => {
    mount(book({ flairId: "finished" }));
    await settle();

    chip().click();
    await settle();
    item("Dropped").click();
    await settle();

    expect(flaired).toEqual([["bk-1", "dropped"]]);
  });

  it("hands the delete to its host instead of confirming in the card", async () => {
    mount();
    await settle();

    gear().click();
    await settle();
    item("Delete").click();
    await settle();

    // The card only asks. The confirmation is the host's themed dialog, which
    // has to render outside the card: `.bc-card` keeps a transform, so it is
    // the containing block for any fixed overlay mounted inside it.
    expect(removed).toEqual(["bk-1"]);
    // And the menu closes first, so that dialog's focus trap snapshots the
    // trigger and can hand focus back to it on unmount.
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(gear());
  });

  it("closes and restores focus before handing off to edit or share", async () => {
    mount();
    await settle();

    gear().click();
    await settle();
    item("Edit").click();
    await settle();
    expect(edited).toEqual(["bk-1"]);
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(gear());

    gear().click();
    await settle();
    item("Share").click();
    await settle();
    expect(shared).toEqual(["bk-1"]);
    expect(menu()).toBeNull();
  });

  it("drives both menus without logging an untracked-read diagnostic", async () => {
    mount(book({ flairId: "finished" }));
    await settle();

    gear().click();
    await settle();
    chip().click();
    await settle();
    press("ArrowDown");
    press("Escape");
    await settle();
    chip().click();
    await settle();
    item("Reading").click();
    await settle();

    expect(logged.filter((m) => m.includes("STRICT_READ_UNTRACKED"))).toEqual(
      [],
    );
  });

  it("clears the assignment from the explicit No flair entry", async () => {
    mount(book({ flairId: "finished" }));
    await settle();

    chip().click();
    await settle();
    const none = item("No flair");
    expect(none.getAttribute("aria-checked")).toBe("false");
    none.click();
    await settle();

    expect(flaired).toEqual([["bk-1", null]]);
    expect(menu()).toBeNull();
  });

  it("No flair closes without firing when nothing is set", async () => {
    mount();
    await settle();

    chip().click();
    await settle();
    const none = item("No flair");
    expect(none.getAttribute("aria-checked")).toBe("true");
    none.click();
    await settle();

    // Already clear: the entry is the state of record, not another toggle.
    expect(flaired).toEqual([]);
    expect(menu()).toBeNull();
  });

  it("re-measures the flip while the menu stays open", async () => {
    mount();
    await settle();
    setViewport(1024, 768);

    gear().click();
    await settle();
    const pop = container.querySelector<HTMLElement>(".bc-actions-menu");
    if (!pop) throw new Error("actions menu missing");
    expect(pop.classList.contains("flip-y")).toBe(false);

    // The card moves under the open popover. Nothing reactive changed, so
    // only a listener can notice that the menu no longer fits.
    setViewport(4, 4);
    window.dispatchEvent(new Event("resize"));
    await settle();
    expect(pop.classList.contains("flip-y")).toBe(true);

    // And back again: the measurement is taken as if the menu were unflipped,
    // so room recovered is a flip given up. Scroll is captured rather than
    // bubbled, because it is the shelf that scrolls, not the window.
    setViewport(1024, 768);
    container.dispatchEvent(new Event("scroll"));
    await settle();
    expect(pop.classList.contains("flip-y")).toBe(false);
  });

  it("opens the second menu unflipped when the chips are switched", async () => {
    mount();
    await settle();
    setViewport(4, 4);

    chip().click();
    await settle();
    const flairPop = container.querySelector<HTMLElement>(".bc-flair-menu");
    expect(flairPop?.classList.contains("flip-x")).toBe(true);

    // A switch never passes through the closed state, so a flip retired only
    // on close rides into the next menu even when it has room to spare.
    setViewport(1024, 768);
    gear().click();
    await settle();
    const actionsPop = container.querySelector<HTMLElement>(".bc-actions-menu");
    expect(actionsPop?.classList.contains("flip-x")).toBe(false);
    expect(actionsPop?.classList.contains("flip-y")).toBe(false);
  });

  it("hands focus back to the chip when a dismissing click orphans it", async () => {
    mount();
    await settle();

    gear().focus();
    gear().click();
    await settle();
    expect(focused()).toBe("Edit");

    // The shelf background takes the click but cannot take focus, and the
    // item that holds it is removed with the menu. Escape restores the chip;
    // so does this, because the alternative is focus stranded on <body>.
    const shelf = document.createElement("div");
    document.body.appendChild(shelf);
    try {
      shelf.click();
      await settle();
      expect(menu()).toBeNull();
      expect(document.activeElement).toBe(gear());
    } finally {
      shelf.remove();
    }
  });
});
