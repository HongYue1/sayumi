// DropSelect: trigger/menu wiring, roving keyboard, type-ahead, and the
// a11y contract. Mounts the real component with @solidjs/web's render;
// `flush` forces Solid 2.0's batched writes so assertions see committed
// state, and settled microtasks let the open-focus microtask land.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createSignal, flush } from "solid-js";
import { render } from "@solidjs/web";
import DropSelect, {
  shouldDropUp,
  type DropSelectGroup,
} from "~/components/reader/DropSelect";

const GROUPS: DropSelectGroup[] = [
  {
    label: "Built-in",
    options: [
      { value: "literata", label: "Literata" },
      { value: "atkinson", label: "Atkinson" },
    ],
  },
  {
    label: "Your fonts",
    options: [
      { value: "user:elena", label: "Elena" },
      { value: "user:eb", label: "EB Garamond" },
    ],
  },
];

const onSelect = vi.fn<(value: string) => void>();

let container: HTMLDivElement;
let dispose: (() => void) | undefined;

function mount(
  props: Partial<{
    value: string;
    disabled: boolean;
    groups: DropSelectGroup[];
  }> = {},
): void {
  container = document.createElement("div");
  document.body.append(container);
  dispose = render(
    () =>
      DropSelect({
        id: "test-select",
        label: "Test select",
        value: props.value ?? "literata",
        groups: props.groups ?? GROUPS,
        disabled: props.disabled,
        onSelect,
      }),
    container,
  );
  flush();
}

async function settle(): Promise<void> {
  await Promise.resolve();
  flush();
  await Promise.resolve();
  flush();
}

function trigger(): HTMLButtonElement {
  const el = container.querySelector<HTMLButtonElement>("#test-select");
  if (!el) throw new Error("trigger missing");
  return el;
}

function menu(): HTMLElement | null {
  return container.querySelector<HTMLElement>(".ds-menu");
}

function picks(): HTMLButtonElement[] {
  return [...container.querySelectorAll<HTMLButtonElement>(".ds-pick")];
}

function openMenu(): void {
  trigger().click();
  flush();
}

function key(
  target: Element,
  keyName: string,
  init: KeyboardEventInit = {},
): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    key: keyName,
    bubbles: true,
    cancelable: true,
    ...init,
  });
  target.dispatchEvent(event);
  flush();
  return event;
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.useRealTimers();
});

afterEach(() => {
  dispose?.();
  dispose = undefined;
  container.remove();
  vi.useRealTimers();
});

describe("DropSelect", () => {
  it("shows the current value label and opens the grouped menu on click", async () => {
    mount({ value: "user:elena" });
    expect(trigger().textContent).toContain("Elena");
    expect(trigger().getAttribute("aria-haspopup")).toBe("listbox");
    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    expect(menu()).toBeNull();

    openMenu();
    await settle();
    expect(trigger().getAttribute("aria-expanded")).toBe("true");
    const list = menu();
    expect(list?.getAttribute("role")).toBe("listbox");
    expect(
      [...container.querySelectorAll(".ds-group")].map((g) => g.textContent),
    ).toEqual(["Built-in", "Your fonts"]);
    expect(picks().map((b) => b.textContent)).toEqual([
      "Literata",
      "Atkinson",
      "Elena",
      "EB Garamond",
    ]);
    // Active option carries the selected state; focus moved into the menu.
    const active = picks().find(
      (b) => b.getAttribute("aria-selected") === "true",
    );
    expect(active?.textContent).toContain("Elena");
    expect(document.activeElement).toBe(active);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("refuses a dismissal that lands after the menu closed", async () => {
    mount();
    openMenu();
    await settle();

    const outside = document.createElement("button");
    document.body.append(outside);
    // Dispatched without `key`, which flushes: a flush between the two
    // dismissals would tear the window listener down and hide the repeat.
    const escape = (): void => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
    };

    escape();
    expect(document.activeElement).toBe(trigger());

    // open() still reads its pre-write value here, which is precisely the
    // window in which a second dismissal would pull focus back off whatever
    // had legitimately taken it.
    outside.focus();
    escape();
    expect(document.activeElement).toBe(outside);

    flush();
    expect(menu()).toBeNull();
    outside.remove();
  });

  it("selects on click, closes, and refocuses the trigger", async () => {
    mount();
    openMenu();
    await settle();
    picks()[1].click();
    flush();
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith("atkinson");
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("falls back to the raw value when nothing matches", () => {
    mount({ value: "user:gone" });
    expect(trigger().textContent).toContain("user:gone");
  });

  it("renders an empty group header with no items", async () => {
    mount({
      groups: [
        { label: "Built-in", options: [{ value: "a", label: "A" }] },
        { label: "Your fonts (none)", options: [] },
      ],
    });
    openMenu();
    await settle();
    expect(
      [...container.querySelectorAll(".ds-group")].map((g) => g.textContent),
    ).toEqual(["Built-in", "Your fonts (none)"]);
    expect(picks()).toHaveLength(1);
  });

  it("keeps the listbox owning groups only", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    // A listbox owns options and groups. The headings stay out of the tree,
    // and each group still carries its label through aria-labelledby, which
    // reads hidden text when the reference is direct.
    const heads = [...list.querySelectorAll("p.ds-group")];
    expect(heads).toHaveLength(2);
    for (const head of heads) {
      expect(head.getAttribute("aria-hidden")).toBe("true");
    }
    const owned = [...list.children].filter(
      (n) => n.getAttribute("aria-hidden") !== "true",
    );
    expect(owned.map((n) => n.getAttribute("role"))).toEqual([
      "group",
      "group",
    ]);
    expect(owned.map((n) => n.getAttribute("aria-labelledby"))).toEqual(
      heads.map((h) => h.id),
    );
  });

  it("walks options with arrows, Home, and End", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    // Entry focus is the active option (Literata, first).
    expect(document.activeElement?.textContent).toContain("Literata");
    key(list, "ArrowDown");
    expect(document.activeElement?.textContent).toContain("Atkinson");
    key(list, "ArrowDown");
    expect(document.activeElement?.textContent).toContain("Elena");
    key(list, "ArrowUp");
    expect(document.activeElement?.textContent).toContain("Atkinson");
    key(list, "End");
    expect(document.activeElement?.textContent).toContain("EB Garamond");
    key(list, "ArrowDown");
    expect(document.activeElement?.textContent).toContain("Literata");
    key(list, "Home");
    expect(document.activeElement?.textContent).toContain("Literata");
  });

  it("consumes navigation keys so reader shortcuts never fire underneath", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    let bubbled = 0;
    const spy = (): void => {
      bubbled += 1;
    };
    window.addEventListener("keydown", spy);
    // Window bubble listeners must not observe menu-owned keys; the reader's
    // page-turn and panel shortcuts live there.
    key(list, "ArrowDown");
    key(list, "e");
    const escape = key(list, "Escape");
    window.removeEventListener("keydown", spy);
    expect(bubbled).toBe(0);
    expect(escape.defaultPrevented).toBe(true);
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it("leaves a composing Escape alone", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    const composing = key(list, "Escape", { isComposing: true });
    expect(composing.isComposing).toBe(true);
    expect(composing.defaultPrevented).toBe(false);
    expect(menu()).not.toBeNull();
  });

  it("dismisses on outside pointerdown without stealing focus", async () => {
    mount();
    openMenu();
    await settle();
    expect(menu()).not.toBeNull();
    document.body.dispatchEvent(
      new PointerEvent("pointerdown", { bubbles: true }),
    );
    flush();
    expect(menu()).toBeNull();
    expect(document.activeElement).not.toBe(trigger());
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("jumps to type-ahead matches and cycles repeats", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    key(list, "e");
    expect(document.activeElement?.textContent).toContain("Elena");
    // Repeating the character cycles its matches.
    key(list, "e");
    expect(document.activeElement?.textContent).toContain("EB Garamond");
    key(list, "e");
    expect(document.activeElement?.textContent).toContain("Elena");
  });

  it("restarts type-ahead after a pause", async () => {
    vi.useFakeTimers();
    try {
      mount();
      openMenu();
      await settle();
      const list = menu();
      if (!list) throw new Error("menu missing");
      key(list, "l");
      expect(document.activeElement?.textContent).toContain("Literata");
      await vi.advanceTimersByTimeAsync(900);
      key(list, "a");
      expect(document.activeElement?.textContent).toContain("Atkinson");
    } finally {
      vi.useRealTimers();
    }
  });

  it("stays shut while disabled, and keeps its place in the tab order", async () => {
    mount({ disabled: true });
    // aria-disabled, never disabled: the trigger sits inside panels that trap
    // focus, so a real attribute blurs it the moment the mode flips underneath
    // and drops focus to body. toggle() owns the refusal instead.
    expect(trigger().disabled).toBe(false);
    expect(trigger().getAttribute("aria-disabled")).toBe("true");
    trigger().focus();
    expect(document.activeElement).toBe(trigger());

    trigger().click();
    await settle();
    expect(menu()).toBeNull();
    expect(trigger().getAttribute("aria-expanded")).toBe("false");
    // The refused activation leaves focus exactly where it was.
    expect(document.activeElement).toBe(trigger());
  });

  it("collapses the menu when disabled flips mid-interaction", async () => {
    // Getter props, not a re-invoked root: a real parent (SettingsPanel)
    // pushes new values into the live instance's reactive props, while
    // re-calling the component would remount it -- the path the disabled
    // effect guards is the former.
    const [disabled, setDisabled] = createSignal(false);
    container = document.createElement("div");
    document.body.append(container);
    dispose = render(
      () =>
        DropSelect({
          id: "flip-select",
          label: "Flip select",
          value: "literata",
          groups: GROUPS,
          get disabled() {
            return disabled();
          },
          onSelect,
        }),
      container,
    );
    flush();
    const bar = container.querySelector<HTMLButtonElement>("#flip-select")!;
    bar.click();
    flush();
    await settle();
    expect(container.querySelector(".ds-menu")).not.toBeNull();

    // The Show hides the listbox on disabled; the open state must follow or
    // the trigger lies about aria-expanded and the next toggle after
    // re-enable closes instead of opening.
    setDisabled(true);
    flush();
    await settle();
    expect(container.querySelector(".ds-menu")).toBeNull();
    expect(bar.getAttribute("aria-expanded")).toBe("false");

    setDisabled(false);
    flush();
    bar.click();
    flush();
    await settle();
    expect(container.querySelector(".ds-menu")).not.toBeNull();
  });

  it("consumes even unmatched type-ahead so shortcuts never fire", async () => {
    mount();
    openMenu();
    await settle();
    const list = menu();
    if (!list) throw new Error("menu missing");
    let bubbled = 0;
    const spy = (): void => {
      bubbled += 1;
    };
    window.addEventListener("keydown", spy);
    // "z" matches nothing: still menu-owned, still consumed.
    const unmatched = key(list, "z");
    window.removeEventListener("keydown", spy);
    expect(unmatched.defaultPrevented).toBe(true);
    expect(bubbled).toBe(0);
    expect(menu()).not.toBeNull();
  });

  it("opens from the collapsed trigger on arrows, Home, and End", async () => {
    mount();
    trigger().focus();
    let bubbled = 0;
    const spy = (): void => {
      bubbled += 1;
    };
    window.addEventListener("keydown", spy);
    // A closed trigger is a plain button, so without a local handler these
    // reach the reader's window shortcuts (page turns) instead of opening.
    for (const name of ["ArrowDown", "ArrowUp", "Home", "End"]) {
      const event = key(trigger(), name);
      await settle();
      expect(event.defaultPrevented).toBe(true);
      expect(menu()).not.toBeNull();
      // Entry focus lands on the active option through the open effect.
      expect(document.activeElement?.textContent).toContain("Literata");
      key(menu()!, "Escape");
      await settle();
      expect(menu()).toBeNull();
    }
    window.removeEventListener("keydown", spy);
    expect(bubbled).toBe(0);
    expect(document.activeElement).toBe(trigger());
  });

  it("pins the upward-flip trade-off", () => {
    // No room below, room above: flip.
    expect(shouldDropUp(20, 150, 240)).toBe(true);
    // Fits below: stay down even with more room above.
    expect(shouldDropUp(300, 400, 240)).toBe(false);
    // Exact fit below stays down.
    expect(shouldDropUp(240, 300, 240)).toBe(false);
    // Clips both ways: keep the downward anchor instead of clipping above.
    expect(shouldDropUp(50, 40, 240)).toBe(false);
  });

  it("opens upward when the panel clips a downward menu", async () => {
    mount();
    // The scrolling panel the settings dropdowns live in.
    container.style.overflowY = "auto";
    container.getBoundingClientRect = () => new DOMRect(0, 100, 300, 200);
    trigger().getBoundingClientRect = () => new DOMRect(0, 250, 300, 30);
    // Instance mocks above survive (trigger and panel never unmount); the
    // menu remounts per open, so its height comes from the prototype for the
    // open-time measure below.
    const menuRect = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockReturnValue(new DOMRect(0, 10, 300, 240));
    try {
      openMenu();
      await settle();
      // 240px of menu, 20px of room below, 150px above: flips up.
      expect(menu()?.classList.contains("ds-up")).toBe(true);
    } finally {
      menuRect.mockRestore();
    }
  });

  it("declares icon intents the development audit accepts", async () => {
    // The trigger chevron (labelFromParent) must sit under a named control
    // and the row check (decorative) beside exposed text: the Icon audit
    // warns otherwise, and warnings are gate failures.
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    try {
      mount();
      openMenu();
      await settle();
      picks()[1].click();
      flush();
      expect(warn).not.toHaveBeenCalled();
    } finally {
      warn.mockRestore();
    }
  });
});
