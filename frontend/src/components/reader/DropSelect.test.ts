// DropSelect: trigger/menu wiring, roving keyboard, type-ahead, and the
// a11y contract. Mounts the real component with @solidjs/web's render;
// `flush` forces Solid 2.0's batched writes so assertions see committed
// state, and settled microtasks let the open-focus microtask land.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import { render } from "@solidjs/web";
import DropSelect, {
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

  it("stays shut while disabled", async () => {
    mount({ disabled: true });
    expect(trigger().disabled).toBe(true);
    trigger().click();
    await settle();
    expect(menu()).toBeNull();
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
