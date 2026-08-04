// SettingsPanel.test.ts -- mounts the real panel with @solidjs/web's render
// (the ChapterFrame.test.ts pattern; @solidjs/testing-library's dist still
// imports the removed "solid-js/web" specifier).
//
// The panel's collaborators are module singletons that would otherwise reach
// the network, so each is replaced at the module boundary. Every mock spreads
// its real module: unrelated named imports elsewhere in the graph (the real
// settings store, CustomThemeDialog) keep resolving, and ApiError stays the
// exact class the store's instanceof checks use -- settings.test.ts explains
// why a hand-rolled twin proves nothing.
//
// The settings fake keeps the reactive contract the panel depends on: `value`
// is a signal, so the property reads at each s().foo call site track just as
// they do against the real store node, while `loaded` is test-driven. The
// store's own semantics (merge, dirty retry, 4xx rollback) belong to
// settings.test.ts; these tests are about the panel.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render } from "@solidjs/web";
import { flush } from "solid-js";
import type * as ApiClient from "~/api/client";
import type * as CustomThemesModule from "~/lib/customThemes";
import type * as FontRegistryModule from "~/lib/fontRegistry";
import type * as RouterModule from "~/lib/router";
import type * as SettingsModule from "~/lib/settings";
import type * as ThemeModule from "~/lib/theme";
import type * as ToastModule from "~/lib/toast";
import { DEFAULT_USER_SETTINGS, settings } from "~/lib/settings";
import { SPECIMEN_BOOK_ID } from "~/lib/specimen";
import SettingsPanel from "~/components/reader/SettingsPanel";

const api = vi.hoisted(() => ({
  getPresets: vi.fn<() => Promise<ApiClient.SettingsPreset[]>>(),
  createPreset:
    vi.fn<
      (data: {
        name: string;
        settings: ApiClient.UserSettings;
      }) => Promise<ApiClient.SettingsPreset>
    >(),
  deletePreset: vi.fn<(id: string) => Promise<void>>(),
}));
const applyTheme = vi.hoisted(() => vi.fn<(id: string) => void>());
const showToast = vi.hoisted(() => vi.fn<(message: string) => void>());
const navigate = vi.hoisted(() => vi.fn<(path: string) => void>());
const rescanFonts = vi.hoisted(() => vi.fn<() => Promise<boolean>>());

/** Test-owned state the fakes read; reset in beforeEach. */
const world = vi.hoisted(() => ({
  settingsLoaded: true,
  themesLoaded: false,
  fontsLoaded: true,
  route: { path: "/read/book-1", params: { id: "book-1" } } as {
    path: string;
    params: Record<string, string>;
  },
  updates: [] as Partial<ApiClient.UserSettings>[],
  resets: 0,
  setSettings: (patch: Partial<ApiClient.UserSettings>): void => {
    throw new Error(`settings fake not ready: ${Object.keys(patch).join(",")}`);
  },
  setFamilies: (list: ApiClient.UserFontFamily[]): void => {
    throw new Error(`font registry fake not ready: ${list.length} families`);
  },
}));

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});

vi.mock("~/lib/settings", async (importOriginal) => {
  const actual = await importOriginal<typeof SettingsModule>();
  const { createSignal } = await import("solid-js");
  const [value, setValue] = createSignal<ApiClient.UserSettings>({
    ...actual.DEFAULT_USER_SETTINGS,
  });
  world.setSettings = (patch) => setValue({ ...value(), ...patch });
  return {
    ...actual,
    settings: {
      get value() {
        return value();
      },
      get loaded() {
        return world.settingsLoaded;
      },
      update(patch: Partial<ApiClient.UserSettings>) {
        world.updates.push(patch);
        setValue({ ...value(), ...patch });
      },
      resetToDefaults() {
        world.resets += 1;
        setValue({ ...actual.DEFAULT_USER_SETTINGS });
      },
    },
  };
});

vi.mock("~/lib/fontRegistry", async (importOriginal) => {
  const actual = await importOriginal<typeof FontRegistryModule>();
  const { createSignal } = await import("solid-js");
  const [families, setFamilies] = createSignal<ApiClient.UserFontFamily[]>([]);
  world.setFamilies = (list) => setFamilies(list);
  return {
    ...actual,
    fontRegistry: {
      get families() {
        return families();
      },
      get loaded() {
        return world.fontsLoaded;
      },
      get: (id: string) => families().find((f) => f.id === id),
      rescan: rescanFonts,
      cssValue: () => null,
    },
  };
});

vi.mock("~/lib/customThemes", async (importOriginal) => {
  const actual = await importOriginal<typeof CustomThemesModule>();
  return {
    ...actual,
    customThemes: {
      list: [],
      get loaded() {
        return world.themesLoaded;
      },
      load: () => {
        world.themesLoaded = true;
        return Promise.resolve(true);
      },
    },
  };
});

vi.mock("~/lib/theme", async (importOriginal) => {
  const actual = await importOriginal<typeof ThemeModule>();
  return { ...actual, applyTheme };
});

vi.mock("~/lib/toast", async (importOriginal) => {
  const actual = await importOriginal<typeof ToastModule>();
  return { ...actual, toast: { ...actual.toast, show: showToast } };
});

vi.mock("~/lib/router", async (importOriginal) => {
  const actual = await importOriginal<typeof RouterModule>();
  return {
    ...actual,
    router: {
      get route() {
        return world.route;
      },
      navigate,
    },
  };
});

function family(
  id: string,
  detected: Partial<ApiClient.UserFontFamily["detected"]> = {},
): ApiClient.UserFontFamily {
  return {
    id,
    label: id.startsWith("user:") ? id.slice(5) : id,
    category: "serif",
    files: ["Regular.otf", "Italic.otf"],
    variable: false,
    detected: {
      regular: "",
      italic: "",
      bold: "",
      boldItalic: "",
      ...detected,
    },
  };
}

function preset(
  id: string,
  name: string,
  over: Partial<ApiClient.UserSettings> = {},
): ApiClient.SettingsPreset {
  return {
    id,
    name,
    settings: { ...DEFAULT_USER_SETTINGS, ...over },
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: Error) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

let dispose: (() => void) | undefined;
let container: HTMLDivElement;
const onclose = vi.fn();

function mount(): void {
  container = document.createElement("div");
  document.body.appendChild(container);
  dispose = render(() => SettingsPanel({ onclose }), container);
  flush();
}

function unmount(): void {
  dispose?.();
  dispose = undefined;
  container.remove();
}

/** Drains the promise chains onSettled kicks off, flushing between ticks. */
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

function selectEl(selector: string): HTMLSelectElement {
  return el(selector) as HTMLSelectElement;
}

function all<T extends Element>(selector: string): T[] {
  return [...container.querySelectorAll<T>(selector)];
}

function text(node: Element | null): string {
  return node?.textContent?.trim() ?? "";
}

/** Preset names in DOM order, read off the per-row delete buttons. */
function names(): string[] {
  return all<HTMLButtonElement>('[aria-label^="Delete preset "]').map((b) =>
    (b.getAttribute("aria-label") ?? "").replace("Delete preset ", ""),
  );
}

function del(name: string): HTMLElement {
  return el(`[aria-label="Delete preset ${name}"]`);
}

function namingForm(): HTMLFormElement {
  const form = el(".stp-preset-confirm").closest("form");
  if (!form) throw new Error("preset naming form not found");
  return form;
}

function openNaming(name: string): void {
  el(".stp-preset-new").click();
  flush();
  const input = namingForm().querySelector("input");
  if (!input) throw new Error("preset name input not found");
  input.value = name;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flush();
}

function submitNaming(): void {
  namingForm().dispatchEvent(
    new Event("submit", { bubbles: true, cancelable: true }),
  );
  flush();
}

function headNotes(): string[] {
  return all(".stp-head-note").map((n) => text(n));
}

function userGroups(): HTMLOptGroupElement[] {
  return all<HTMLOptGroupElement>('optgroup[label^="Your fonts"]');
}

function userOptions(): HTMLOptionElement[] {
  return all<HTMLOptionElement>('optgroup[label^="Your fonts"] option');
}

beforeEach(() => {
  vi.clearAllMocks();
  world.settingsLoaded = true;
  world.themesLoaded = false;
  world.fontsLoaded = true;
  world.route = { path: "/read/book-1", params: { id: "book-1" } };
  world.updates = [];
  world.resets = 0;
  world.setSettings({ ...DEFAULT_USER_SETTINGS });
  world.setFamilies([]);
  api.getPresets.mockResolvedValue([]);
  api.createPreset.mockImplementation((data) =>
    Promise.resolve(preset("created", data.name)),
  );
  api.deletePreset.mockResolvedValue(undefined);
  rescanFonts.mockResolvedValue(true);
});

afterEach(() => {
  if (dispose) unmount();
  vi.useRealTimers();
});

describe("reader settings panel", () => {
  it("does not apply a theme while settings are still unloaded", async () => {
    // The panel paints the theme once the custom themes arrive. Before the
    // settings load resolves, value.theme is the compile-time default, so
    // applying it repaints the shell wrong AND poisons the localStorage
    // palette cache the pre-paint bootstrap reads on the next reload.
    world.settingsLoaded = false;
    world.setSettings({ theme: "gruvbox" });
    mount();
    await settle();
    expect(applyTheme).not.toHaveBeenCalled();
  });

  it("applies the theme once settings and custom themes are both in", async () => {
    world.setSettings({ theme: "gruvbox" });
    mount();
    await settle();
    expect(applyTheme).toHaveBeenCalledWith("gruvbox");
  });

  it("restores a failed delete at its original position", async () => {
    api.getPresets.mockResolvedValue([
      preset("a", "Alpha"),
      preset("b", "Beta"),
      preset("c", "Gamma"),
    ]);
    api.deletePreset.mockRejectedValue(new Error("404 not_found"));
    mount();
    await settle();
    expect(names()).toEqual(["Alpha", "Beta", "Gamma"]);

    del("Beta").click();
    flush();
    expect(names()).toEqual(["Alpha", "Gamma"]);

    await settle();
    expect(names()).toEqual(["Alpha", "Beta", "Gamma"]);
    expect(showToast).toHaveBeenCalledWith("Couldn't delete preset");
  });

  it("does not resurrect a delete that succeeded when a later one fails", async () => {
    // Beta's request is in flight and will fail; Alpha's starts after it and
    // succeeds. Rolling back to a whole-list snapshot taken before Beta's
    // removal brings Alpha back from the dead.
    api.getPresets.mockResolvedValue([
      preset("a", "Alpha"),
      preset("b", "Beta"),
      preset("c", "Gamma"),
    ]);
    const beta = deferred<void>();
    api.deletePreset.mockImplementation((id) =>
      id === "b" ? beta.promise : Promise.resolve(),
    );
    mount();
    await settle();

    del("Beta").click();
    flush();
    del("Alpha").click();
    flush();
    expect(names()).toEqual(["Gamma"]);

    beta.reject(new Error("404 not_found"));
    await settle();
    expect(names()).toEqual(["Gamma", "Beta"]);
  });

  it("refuses to capture a preset before settings have loaded", async () => {
    world.settingsLoaded = false;
    mount();
    await settle();
    openNaming("Night");
    submitNaming();
    await settle();
    expect(api.createPreset).not.toHaveBeenCalled();
    expect(showToast).toHaveBeenCalledWith(
      expect.stringContaining("loaded") as unknown as string,
    );
  });

  it("sends a copy of the font-role map, never the live one", async () => {
    world.setSettings({
      fontRoles: { "user:minion": { regular: "Regular.otf" } },
    });
    mount();
    await settle();
    openNaming("Night");
    submitNaming();
    await settle();
    const sent = api.createPreset.mock.calls[0]?.[0];
    expect(sent?.settings.fontRoles).toEqual(settings.value.fontRoles);
    expect(sent?.settings.fontRoles).not.toBe(settings.value.fontRoles);
  });

  it("applies a preset without aliasing its cached font-role map", async () => {
    const saved = preset("a", "Alpha", {
      fontRoles: { "user:minion": { regular: "Regular.otf" } },
    });
    api.getPresets.mockResolvedValue([saved]);
    mount();
    await settle();

    el(".stp-preset-apply").click();
    flush();
    const patch = world.updates[0];
    expect(patch?.fontRoles).toEqual(saved.settings.fontRoles);
    expect(patch?.fontRoles).not.toBe(saved.settings.fontRoles);
    expect(settings.value.fontRoles).not.toBe(saved.settings.fontRoles);
  });

  it("keeps exactly one user font group per select across rescans", async () => {
    // On 2.0.0-beta.29 a conditional element child of a <select> is never
    // removed once the <select> has another element child, so gating the group
    // on userFamilies().length stranded a stale group (and its dead
    // user:<dir> options) after every rescan. Always-mounted, emptied by <For>.
    world.setFamilies([family("user:minion")]);
    mount();
    await settle();
    expect(text(el(".stp-rescan"))).toContain("Rescan fonts folder");
    expect(userGroups()).toHaveLength(2);
    expect(userOptions()).toHaveLength(2);

    rescanFonts.mockImplementation(() => {
      world.setFamilies([]);
      return Promise.resolve(true);
    });
    el(".stp-rescan").click();
    flush();
    await settle();
    expect(userGroups()).toHaveLength(2);
    expect(userOptions()).toHaveLength(0);
    expect(userGroups().map((g) => g.label)).toEqual([
      "Your fonts (none)",
      "Your fonts (none)",
    ]);

    world.setFamilies([family("user:minion"), family("user:garamond")]);
    flush();
    expect(userGroups()).toHaveLength(2);
    expect(userOptions()).toHaveLength(4);
  });

  it("reports a bottom margin that has diverged from the top", async () => {
    world.setSettings({ marginTop: 48, marginBottom: 96 });
    mount();
    await settle();
    expect(headNotes().some((n) => n.includes("bottom 96px"))).toBe(true);

    // Auto rows carry the note too: the row shows "--", the note the real
    // bottom value.
    world.setSettings({ marginTop: null });
    flush();
    expect(headNotes().some((n) => n.includes("bottom 96px"))).toBe(true);

    world.setSettings({ marginTop: 96 });
    flush();
    expect(headNotes().some((n) => n.includes("bottom"))).toBe(false);
  });

  it("keeps the preset submit button focusable while it is inert", async () => {
    mount();
    await settle();
    el(".stp-preset-new").click();
    flush();

    const confirmBtn = el(".stp-preset-confirm") as HTMLButtonElement;
    expect(confirmBtn.getAttribute("aria-disabled")).toBe("true");
    expect(confirmBtn.disabled).toBe(false);
    submitNaming();
    await settle();
    expect(api.createPreset).not.toHaveBeenCalled();

    const input = namingForm().querySelector("input");
    if (!input) throw new Error("preset name input not found");
    input.value = "Night";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    flush();
    expect(el(".stp-preset-confirm").getAttribute("aria-disabled")).toBe(
      "false",
    );
    submitNaming();
    await settle();
    expect(api.createPreset).toHaveBeenCalledTimes(1);
  });

  it("names the detected file on Auto and never preselects it", async () => {
    world.setFamilies([family("user:minion", { regular: "Regular.otf" })]);
    world.setSettings({ fontFamily: "user:minion", preserveFonts: false });
    mount();
    await settle();

    const regular = selectEl("#font-role-regular");
    expect(regular.value).toBe("");
    expect(text(regular.options.item(0))).toBe("Auto (Regular.otf)");
    expect(text(selectEl("#font-role-italic").options.item(0))).toBe("Auto");

    regular.value = "Italic.otf";
    regular.dispatchEvent(new Event("change", { bubbles: true }));
    flush();
    expect(settings.value.fontRoles).toEqual({
      "user:minion": { regular: "Italic.otf" },
    });

    const cleared = selectEl("#font-role-regular");
    cleared.value = "";
    cleared.dispatchEvent(new Event("change", { bubbles: true }));
    flush();
    expect(settings.value.fontRoles).toEqual({});
  });

  it("closes instead of re-navigating when the specimen is already open", async () => {
    mount();
    await settle();
    el(".stp-specimen").click();
    flush();
    expect(navigate).toHaveBeenCalledWith(`/read/${SPECIMEN_BOOK_ID}`);
    expect(onclose).not.toHaveBeenCalled();

    unmount();
    world.route = {
      path: `/read/${SPECIMEN_BOOK_ID}`,
      params: { id: SPECIMEN_BOOK_ID },
    };
    mount();
    await settle();
    el(".stp-specimen").click();
    flush();
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("closes on Escape from inside the panel", async () => {
    mount();
    await settle();
    el(".stp").dispatchEvent(
      new KeyboardEvent("keydown", {
        key: "Escape",
        bubbles: true,
        cancelable: true,
      }),
    );
    flush();
    expect(onclose).toHaveBeenCalledTimes(1);
  });

  it("arms the reset button, disarms it after three seconds", async () => {
    vi.useFakeTimers();
    mount();
    await settle();

    const reset = el(".stp-reset");
    reset.click();
    flush();
    expect(reset.className).toContain("armed");
    expect(world.resets).toBe(0);

    vi.advanceTimersByTime(3000);
    flush();
    expect(reset.className).not.toContain("armed");

    reset.click();
    flush();
    reset.click();
    flush();
    expect(world.resets).toBe(1);
  });
});
