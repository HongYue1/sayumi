// CommandPalette.test.ts -- mounts the real palette with @solidjs/web's render
// (the SearchPanel/SettingsPanel pattern; @solidjs/testing-library's dist
// still imports the removed "solid-js/web" specifier).
//
// Every collaborator is a module singleton replaced at the module boundary;
// each mock spreads its real module so unrelated named exports keep
// resolving. ui is deliberately NOT mocked: the palette's open/close wiring
// is the contract under test, and the real store is a two-signal module
// with no network. No fake timers: the palette arms none (queueMicrotask
// only), and rescan is the boundary mock itself, not a debounced real one.
// Composition owns Escape, Enter, and candidate-navigation keys before either
// the window capture listener or the query's command handler can consume them.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type * as ApiClient from "~/api/client";
import type * as CustomThemesModule from "~/lib/customThemes";
import type * as LibraryModule from "~/lib/library";
import type * as RouterModule from "~/lib/router";
import type * as SessionModule from "~/lib/session";
import type * as SettingsModule from "~/lib/settings";
import type { ThemeDef } from "~/lib/themes";
import { THEMES } from "~/lib/themes";
import { ui } from "~/lib/ui";
import CommandPalette from "~/components/CommandPalette";

const loadForProfile = vi.hoisted(() =>
  vi.fn<(profile: string | null) => Promise<void>>(),
);
const rescan = vi.hoisted(() => vi.fn<() => Promise<void>>());
const navigate = vi.hoisted(() => vi.fn<(path: string) => void>());
const logout = vi.hoisted(() => vi.fn<() => Promise<void>>());
const showToast = vi.hoisted(() => vi.fn<(message: string) => void>());
const updateSettings = vi.hoisted(() =>
  vi.fn<(patch: Partial<ApiClient.UserSettings>) => void>(),
);
const loadCustomThemes = vi.hoisted(() => vi.fn<() => Promise<void>>());

/** Test-owned state the fakes read; reset in beforeEach. */
const world = vi.hoisted(() => ({
  profile: "alice" as string | null,
  setBooks: (_list: ApiClient.BookMeta[]): void => {
    throw new Error("library fake not ready");
  },
  setThemes: (_list: ThemeDef[]): void => {
    throw new Error("customThemes fake not ready");
  },
}));

vi.mock("~/lib/library", async (importOriginal) => {
  const actual = await importOriginal<typeof LibraryModule>();
  const { createSignal } = await import("solid-js");
  const [books, setBooks] = createSignal<ApiClient.BookMeta[]>([]);
  world.setBooks = (list) => setBooks(list);
  return {
    ...actual,
    library: {
      get books() {
        return books();
      },
      loadForProfile,
      rescan,
    },
  };
});

vi.mock("~/lib/settings", async (importOriginal) => {
  const actual = await importOriginal<typeof SettingsModule>();
  return {
    ...actual,
    settings: { update: updateSettings },
  };
});

vi.mock("~/lib/router", async (importOriginal) => {
  const actual = await importOriginal<typeof RouterModule>();
  return { ...actual, router: { ...actual.router, navigate } };
});

vi.mock("~/lib/session", async (importOriginal) => {
  const actual = await importOriginal<typeof SessionModule>();
  return {
    ...actual,
    session: {
      get profile() {
        return world.profile;
      },
      logout,
    },
  };
});

vi.mock("~/lib/toast", () => ({ toast: { show: showToast } }));

vi.mock("~/lib/customThemes", async (importOriginal) => {
  const actual = await importOriginal<typeof CustomThemesModule>();
  const { createSignal } = await import("solid-js");
  const [list, setList] = createSignal<ThemeDef[]>([]);
  world.setThemes = (themes) => setList(themes);
  return {
    ...actual,
    customThemes: {
      get list() {
        return list();
      },
      load: loadCustomThemes,
    },
  };
});

let dispose: (() => void) | undefined;
let container: HTMLDivElement;

function mount(): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  dispose = render(() => CommandPalette(), container);
  flush();
}

function unmount(): void {
  ui.closeOverlays();
  flush();
  dispose?.();
  dispose = undefined;
  container.remove();
}

function open(): void {
  ui.togglePalette();
  flush();
}

/** Drains the microtasks the palette queues for focus and scrolling. */
async function settle(): Promise<void> {
  for (let i = 0; i < 4; i += 1) {
    await Promise.resolve();
    flush();
  }
}

function el(selector: string): HTMLElement {
  const found = container.querySelector<HTMLElement>(selector);
  if (!found) throw new Error(`no element matched ${selector}`);
  return found;
}

function all<T extends Element>(selector: string): T[] {
  return [...container.querySelectorAll<T>(selector)];
}

function book(over: Partial<ApiClient.BookMeta> = {}): ApiClient.BookMeta {
  return {
    id: "b1",
    title: "Book One",
    author: "",
    language: "en",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "ltr",
    chapterCount: 5,
    progress: 0,
    ...over,
  };
}

function input(): HTMLInputElement {
  return el(".cmd-input") as HTMLInputElement;
}

function options(): HTMLElement[] {
  return all<HTMLElement>("li.cmd");
}

function labels(): string[] {
  return options().map((o) => o.textContent ?? "");
}

function activeIndex(): number {
  return options().findIndex((o) => o.getAttribute("aria-selected") === "true");
}

function typeQuery(value: string): void {
  const field = input();
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
  flush();
}

function press(key: string): void {
  input().dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  flush();
}

beforeEach(() => {
  vi.clearAllMocks();
  showToast.mockReset();
  world.profile = "alice";
  world.setBooks([]);
  world.setThemes([]);
  loadForProfile.mockResolvedValue(undefined);
  loadCustomThemes.mockResolvedValue(undefined);
  rescan.mockResolvedValue(undefined);
  logout.mockResolvedValue(undefined);
  ui.closeOverlays();
  flush();
});

afterEach(() => {
  if (dispose) unmount();
});

describe("command palette", () => {
  it("renders nothing while closed", () => {
    mount();
    expect(all(".cmd-overlay")).toHaveLength(0);
  });

  it("opens with the query reset, the input focused, and lazy loads fired", async () => {
    mount();
    open();
    await settle();
    expect(el(".cmd-overlay")).toBeTruthy();
    expect(input().value).toBe("");
    expect(container.contains(document.activeElement)).toBe(true);
    expect(loadForProfile).toHaveBeenCalledWith("alice");
    expect(loadCustomThemes).toHaveBeenCalledTimes(1);
  });

  it("filters multi-word, order-insensitive, across label and hint", async () => {
    mount();
    open();
    await settle();
    typeQuery("theme dark");
    const found = labels();
    expect(found.length).toBeGreaterThan(0);
    expect(found.every((l) => l.includes("Theme:"))).toBe(true);
    expect(found.some((l) => l.includes("Light"))).toBe(false);
    typeQuery("library go");
    expect(labels()).toEqual(["Go to LibraryNavigate"]);
  });

  it("caps the empty-query list at 50 rows, commands then books then themes", async () => {
    world.setBooks(
      Array.from({ length: 60 }, (_, i) =>
        book({ id: `b${i}`, title: `Book ${i}` }),
      ),
    );
    mount();
    open();
    await settle();
    expect(options()).toHaveLength(50);
    // 5 commands + 45 books: the themes are beyond the cap until typed.
    expect(labels()[0]).toBe("Go to LibraryNavigate");
    expect(labels()[49]).toBe("Book 44Open book");
  });

  it("shows the empty fallback and ignores Enter when nothing matches", async () => {
    mount();
    open();
    await settle();
    typeQuery("zzzzzz");
    expect(options()).toHaveLength(0);
    expect(el(".cmd-empty").textContent).toBe("No matches");
    press("Enter");
    expect(ui.palette).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("wraps the selection on ArrowDown and ArrowUp", async () => {
    mount();
    open();
    await settle();
    const count = options().length;
    expect(activeIndex()).toBe(0);
    press("ArrowUp");
    expect(activeIndex()).toBe(count - 1);
    press("ArrowDown");
    expect(activeIndex()).toBe(0);
  });

  it("steps from the clamped selection after the filter shrinks", async () => {
    mount();
    open();
    await settle();
    press("ArrowDown");
    press("ArrowDown");
    press("ArrowDown");
    expect(activeIndex()).toBe(3);
    typeQuery("rescan");
    expect(activeIndex()).toBe(0);
    press("ArrowDown");
    // One match: stepping wraps back to 0 rather than chasing the stale 3.
    expect(activeIndex()).toBe(0);
  });

  it("keeps aria-activedescendant pointed at the selected option", async () => {
    mount();
    open();
    await settle();
    expect(input().getAttribute("aria-activedescendant")).toBe("cmd-opt-0");
    press("ArrowDown");
    expect(input().getAttribute("aria-activedescendant")).toBe("cmd-opt-1");
    typeQuery("zzzzzz");
    expect(input().getAttribute("aria-activedescendant")).toBeNull();
  });

  it("closes before running the command on Enter", async () => {
    mount();
    open();
    await settle();
    navigate.mockImplementationOnce(() => {
      // The close write must already be ISSUED when the command's side
      // effect runs; flush() commits it so the read-back is valid -- under
      // Solid 2.0 batching a synchronous read would still see the old value.
      // Once-only so the implementation cannot leak into the click tests
      // (clearAllMocks keeps implementations).
      flush();
      expect(ui.palette).toBe(false);
    });
    press("Enter");
    expect(navigate).toHaveBeenCalledWith("/");
    expect(ui.palette).toBe(false);
  });

  it("closes on a capture-phase Escape with immediate propagation stopped", async () => {
    mount();
    open();
    await settle();
    let otherHandlerSaw = false;
    const spy = (): void => {
      otherHandlerSaw = true;
    };
    window.addEventListener("keydown", spy, true);
    const escape = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    });
    input().dispatchEvent(escape);
    flush();
    window.removeEventListener("keydown", spy, true);
    expect(ui.palette).toBe(false);
    expect(escape.defaultPrevented).toBe(true);
    expect(otherHandlerSaw).toBe(false);
  });

  it("leaves a composing Escape with the palette query", async () => {
    mount();
    open();
    await settle();
    let captured = false;
    const captureSpy = (): void => {
      captured = true;
    };
    window.addEventListener("keydown", captureSpy, true);
    const composing = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    expect(composing.isComposing).toBe(true);

    input().dispatchEvent(composing);
    flush();
    window.removeEventListener("keydown", captureSpy, true);

    expect(ui.palette).toBe(true);
    expect(composing.defaultPrevented).toBe(false);
    expect(captured).toBe(true);
  });

  it("does not navigate or choose on composing query keys", async () => {
    mount();
    open();
    await settle();
    const arrow = new KeyboardEvent("keydown", {
      key: "ArrowDown",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    expect(arrow.isComposing).toBe(true);
    input().dispatchEvent(arrow);
    flush();
    expect(activeIndex()).toBe(0);
    expect(arrow.defaultPrevented).toBe(false);

    const enter = new KeyboardEvent("keydown", {
      key: "Enter",
      isComposing: true,
      bubbles: true,
      cancelable: true,
    });
    expect(enter.isComposing).toBe(true);
    input().dispatchEvent(enter);
    flush();

    expect(ui.palette).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
    expect(enter.defaultPrevented).toBe(false);
  });

  it("closes on the backdrop dismiss button", async () => {
    mount();
    open();
    await settle();
    el(".backdrop-dismiss").click();
    flush();
    expect(ui.palette).toBe(false);
  });

  it("follows the mouse on option hover", async () => {
    mount();
    open();
    await settle();
    options()[2].dispatchEvent(new MouseEvent("mouseenter", { bubbles: true }));
    flush();
    expect(activeIndex()).toBe(2);
  });

  it("runs a theme command through the settings owner", async () => {
    mount();
    open();
    await settle();
    typeQuery("theme sepia");
    const row = options()[0];
    expect(row.textContent).toContain("Theme:");
    row.click();
    flush();
    expect(ui.palette).toBe(false);
    const id = THEMES.find((t) => t.label === "Sepia")?.id;
    expect(id).toBeTruthy();
    expect(updateSettings).toHaveBeenCalledWith({ theme: id });
  });

  it("runs a book command with an encoded route", async () => {
    world.setBooks([book({ id: "a/b c", title: "Odd Id" })]);
    mount();
    open();
    await settle();
    typeQuery("odd id");
    expect(options()).toHaveLength(1);
    options()[0].click();
    flush();
    expect(navigate).toHaveBeenCalledWith("/read/a%2Fb%20c");
  });

  it("runs rescan and shortcuts commands through their own stores", async () => {
    mount();
    open();
    await settle();
    typeQuery("rescan");
    options()[0].click();
    flush();
    expect(rescan).toHaveBeenCalledTimes(1);
    expect(ui.palette).toBe(false);
    open();
    await settle();
    typeQuery("shortcuts");
    options()[0].click();
    flush();
    expect(ui.palette).toBe(false);
    expect(ui.shortcuts).toBe(true);
  });

  it("reports a sign-out request that fails after local teardown", async () => {
    logout.mockRejectedValue(new Error("network down"));
    mount();
    open();
    await settle();
    typeQuery("sign out");
    options()[0].click();
    await settle();
    expect(logout).toHaveBeenCalledTimes(1);
    expect(showToast).toHaveBeenCalledWith(
      "Could not reach the server to sign out",
    );
    expect(ui.palette).toBe(false);
  });

  it("wires the combobox contract on the input", async () => {
    mount();
    open();
    await settle();
    const field = input();
    expect(field.getAttribute("role")).toBe("combobox");
    expect(field.getAttribute("aria-controls")).toBe("cmd-list");
    expect(field.getAttribute("aria-autocomplete")).toBe("list");
    expect(field.getAttribute("aria-expanded")).toBe("true");
    expect(field.getAttribute("aria-activedescendant")).toBe("cmd-opt-0");
  });

  it("stops intercepting Escape once closed", async () => {
    mount();
    open();
    await settle();
    ui.closeOverlays();
    flush();
    let otherHandlerSaw = false;
    const spy = (): void => {
      otherHandlerSaw = true;
    };
    window.addEventListener("keydown", spy, true);
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    window.removeEventListener("keydown", spy, true);
    expect(otherHandlerSaw).toBe(true);
    expect(ui.palette).toBe(false);
  });
});
