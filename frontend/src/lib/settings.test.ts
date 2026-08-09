import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type * as ApiClient from "~/api/client";

const api = vi.hoisted(() => ({
  getSettings: vi.fn(),
  saveSettings: vi.fn(),
}));
const showToast = vi.hoisted(() => vi.fn());

// Spread the real module so ApiError stays the exact class the store's
// instanceof checks use. A hand-rolled twin drifts (the real constructor takes
// a fourth `cause` argument) and proves nothing about discrimination.
vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});

vi.mock("~/lib/fonts", () => ({
  getFontById: (id: string) => (id === "literata" ? { id } : undefined),
  getFontFamily: (id: string) => id,
}));

vi.mock("~/lib/fontRegistry", () => ({
  fontRegistry: {
    cssValue: vi.fn((id: string) =>
      id === "user:minion" ? "'UserFont', serif" : null,
    ),
  },
  isUserFamilyId: (id: string) => id.startsWith("user:"),
}));

vi.mock("~/lib/toast", () => ({
  toast: { show: showToast },
}));

vi.mock("~/lib/customThemes", () => ({
  customThemes: { list: [] },
}));

vi.mock("~/lib/themes", () => ({
  readerThemeVars: vi.fn(() => null),
}));

/** A complete valid settings object (post-merge shape), with overrides. */
function full(
  overrides: Partial<ApiClient.UserSettings> = {},
): ApiClient.UserSettings {
  return {
    fontSize: 30,
    fontFamily: "literata",
    lineHeight: null,
    paragraphSpacing: null,
    textIndent: 0,
    letterSpacing: null,
    contentWidth: null,
    displayMode: "scroll",
    marginTop: 48,
    marginBottom: 48,
    marginSide: 48,
    preserveStyles: true,
    preserveFonts: false,
    justify: true,
    hyphenation: true,
    theme: "catppuccin",
    chapterTitleAlign: "center",
    chapterTitleSize: 48,
    chapterTitleSpacing: 1,
    chapterTitleFontFamily: null,
    headingLetterSpacing: null,
    headerSizesEnabled: false,
    h1Size: null,
    h2Size: null,
    h3Size: null,
    h4Size: null,
    h5Size: null,
    h6Size: null,
    headerWeight: null,
    textWeight: null,
    fontRoles: {},
    ...overrides,
  };
}

/** Flush the promise microtask queue (save callbacks, deferred saves). */
async function flushMicrotasks(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

beforeEach(() => {
  vi.resetModules();
  // resetAllMocks, not clearAllMocks: clear keeps implementations, so a
  // rejection seeded by one test bleeds into the next and the suite goes
  // order-dependent. Default seeds below; tests layer Once variants on top.
  vi.resetAllMocks();
  vi.useRealTimers();
  api.getSettings.mockResolvedValue({});
  api.saveSettings.mockResolvedValue({});
});

afterEach(() => {
  // A timer that outlives its test would fire into a later test's instance.
  // (getTimerCount throws when the timers APIs are not mocked, so only assert
  // when the test faked them.)
  if (vi.isFakeTimers()) expect(vi.getTimerCount()).toBe(0);
  // Fake timers are worker-global: without a restore they leak into whatever
  // suite runs next in this worker and starve Solid 2.0's microtask flush
  // (fake timers also fake queueMicrotask). Login.test.ts hit this.
  vi.useRealTimers();
});

describe("settings profile lifecycle", () => {
  it("does not fetch settings while no profile is active", async () => {
    const { settings } = await import("~/lib/settings");

    await settings.activate(null);

    expect(api.getSettings).not.toHaveBeenCalled();
    expect(settings.loaded).toBe(false);
  });

  it("activates, clears, and reloads state by profile identity", async () => {
    api.getSettings
      .mockResolvedValueOnce(full({ fontSize: 36 }))
      .mockResolvedValueOnce(full({ fontSize: 42 }));
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");

    await settings.activate("ada");
    flush();
    expect(settings.loaded).toBe(true);
    expect(settings.isReadyFor("ada")).toBe(true);
    expect(settings.isReadyFor("bo")).toBe(false);
    expect(settings.value.fontSize).toBe(36);

    await settings.activate(null);
    flush();
    expect(settings.loaded).toBe(false);
    expect(settings.isReadyFor("ada")).toBe(false);
    expect(settings.value.fontSize).toBe(30);

    await settings.activate("bo");
    flush();
    expect(settings.loaded).toBe(true);
    expect(settings.isReadyFor("bo")).toBe(true);
    expect(settings.value.fontSize).toBe(42);
    expect(api.getSettings).toHaveBeenCalledTimes(2);
  });

  it("ignores a previous profile's load after reset", async () => {
    let resolveFirst!: (value: {
      fontSize: number;
      fontFamily: string;
    }) => void;
    const first = new Promise<{ fontSize: number; fontFamily: string }>(
      (resolve) => {
        resolveFirst = resolve;
      },
    );
    api.getSettings
      .mockReturnValueOnce(first)
      .mockResolvedValueOnce(full({ fontSize: 36 }));
    const { settings } = await import("~/lib/settings");
    // vi.resetModules() gives each test a fresh module graph, so flush has to
    // be imported here too: a statically imported one would drive a different
    // scheduler instance than the store under test.
    const { flush } = await import("solid-js");

    const oldLoad = settings.load();
    const oldSignal = api.getSettings.mock.calls[0][0] as AbortSignal;
    settings.reset();
    expect(oldSignal.aborted).toBe(true);

    await settings.load();
    flush();
    expect(settings.value.fontSize).toBe(36);
    expect(settings.loaded).toBe(true);

    resolveFirst({ fontSize: 18, fontFamily: "literata" });
    await oldLoad;
    flush();
    expect(settings.value.fontSize).toBe(36);
  });

  it("keeps defaults and stays retryable after a failed load", async () => {
    const { ApiError } = await import("~/api/client");
    api.getSettings.mockRejectedValueOnce(
      new ApiError("unreachable", undefined, "network_error"),
    );
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");

    await settings.load();
    flush();
    // Defaults kept, but the failure must NOT mark them as server truth:
    // loaded stays false so the next load() refetches.
    expect(settings.value.fontSize).toBe(30);
    expect(settings.loaded).toBe(false);

    await settings.load();
    flush();
    expect(api.getSettings).toHaveBeenCalledTimes(2);
    expect(settings.loaded).toBe(true);
    expect(settings.value.fontSize).toBe(30);
  });

  it("publishes defaults for a sparse server response", async () => {
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();
    expect(settings.value).toMatchObject({
      fontSize: 30,
      fontFamily: "literata",
      theme: "catppuccin",
      marginTop: 48,
    });
    expect(settings.loaded).toBe(true);
  });

  it("coerces an unknown built-in font id but keeps user fonts", async () => {
    api.getSettings.mockResolvedValueOnce({ fontFamily: "bogus" });
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();
    expect(settings.value.fontFamily).toBe("literata");
  });

  it("exempts user: ids from the coercion", async () => {
    api.getSettings.mockResolvedValueOnce({ fontFamily: "user:minion" });
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();
    expect(settings.value.fontFamily).toBe("user:minion");
  });

  it("loads once and shares an in-flight load", async () => {
    const { settings } = await import("~/lib/settings");
    const first = settings.load();
    const second = settings.load();
    expect(second).toBe(first);
    await Promise.all([first, second]);
    expect(api.getSettings).toHaveBeenCalledTimes(1);

    await settings.load();
    expect(api.getSettings).toHaveBeenCalledTimes(1);
  });

  it("leaves loaded unset when a load is superseded by reset", async () => {
    let resolveFirst!: (value: {
      fontSize: number;
      fontFamily: string;
    }) => void;
    const first = new Promise<{ fontSize: number; fontFamily: string }>(
      (resolve) => {
        resolveFirst = resolve;
      },
    );
    api.getSettings
      .mockReturnValueOnce(first)
      .mockResolvedValueOnce(full({ fontSize: 40 }));
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");

    const oldLoad = settings.load();
    settings.reset();
    resolveFirst({ fontSize: 18, fontFamily: "literata" });
    await oldLoad;
    flush();
    // The superseded load neither publishes nor flips loaded — and must not
    // trigger the deferred-save retry loop (dirty map was cleared by reset).
    expect(settings.loaded).toBe(false);
    expect(api.getSettings).toHaveBeenCalledTimes(1);

    await settings.load();
    flush();
    expect(settings.value.fontSize).toBe(40);
    expect(settings.loaded).toBe(true);
  });
});

describe("settings save pipeline", () => {
  it("defers saves and re-applies edits made before a successful load", async () => {
    vi.useFakeTimers();
    let resolveLoad!: (value: ReturnType<typeof full>) => void;
    const loadPromise = new Promise<ReturnType<typeof full>>((resolve) => {
      resolveLoad = resolve;
    });
    api.getSettings.mockReturnValueOnce(loadPromise);
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");

    void settings.load();
    // An edit while the GET is in flight must not PUT defaults+edit over the
    // server row — the save defers until the load lands.
    settings.update({ fontSize: 31 });
    flush();
    await vi.advanceTimersByTimeAsync(1000);
    expect(api.saveSettings).not.toHaveBeenCalled();
    expect(api.getSettings).toHaveBeenCalledTimes(1);

    resolveLoad(full({ theme: "dark" }));
    await flushMicrotasks();
    flush();
    // The merge preserves the server value for untouched fields and re-applies
    // the edit over it — the response cannot clobber the in-flight edit.
    expect(settings.value.fontSize).toBe(31);
    expect(settings.value.theme).toBe("dark");

    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(api.saveSettings).toHaveBeenCalledTimes(1);
    expect(api.saveSettings.mock.calls[0][0]).toMatchObject({
      fontSize: 31,
      theme: "dark",
    });
  });

  it("preserves fields a sparse response omitted when later edits save", async () => {
    vi.useFakeTimers();
    api.getSettings.mockResolvedValueOnce({ fontSize: 36 });
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();
    expect(settings.value.theme).toBe("catppuccin");

    settings.update({ fontSize: 37 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(api.saveSettings).toHaveBeenCalledTimes(1);
    expect(api.saveSettings.mock.calls[0][0]).toMatchObject({
      fontSize: 37,
      theme: "catppuccin",
      marginTop: 48,
    });
  });

  it("coalesces rapid edits into one debounced PUT", async () => {
    vi.useFakeTimers();
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    settings.update({ fontSize: 40 });
    settings.update({ fontSize: 41 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(api.saveSettings).toHaveBeenCalledTimes(1);
    expect(api.saveSettings.mock.calls[0][0]).toMatchObject({ fontSize: 41 });
  });

  it("rolls back to the last accepted payload on a 4xx", async () => {
    vi.useFakeTimers();
    api.getSettings.mockResolvedValueOnce(full({ fontSize: 36 }));
    const { ApiError } = await import("~/api/client");
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();
    expect(settings.value.fontSize).toBe(36);

    api.saveSettings.mockRejectedValueOnce(
      new ApiError("invalid settings", 400, "invalid"),
    );
    settings.update({ fontSize: 31 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    flush();
    // The invalid edit is rolled back to the server-accepted state and told.
    expect(settings.value.fontSize).toBe(36);
    expect(showToast).toHaveBeenCalledTimes(1);
  });

  it("does not roll back a newer edit when an older save returns 4xx", async () => {
    vi.useFakeTimers();
    let rejectFirst!: (reason: unknown) => void;
    const first = new Promise<void>((_resolve, reject) => {
      rejectFirst = reject;
    });
    const { ApiError } = await import("~/api/client");
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    api.saveSettings.mockReturnValueOnce(first);
    settings.update({ fontSize: 31 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(api.saveSettings).toHaveBeenCalledOnce();

    settings.update({ fontSize: 32 });
    flush();
    rejectFirst(new ApiError("invalid settings", 400, "invalid"));
    await flushMicrotasks();
    flush();

    expect(settings.value.fontSize).toBe(32);
    expect(showToast).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(api.saveSettings).toHaveBeenCalledTimes(2);
    expect(api.saveSettings.mock.calls[1][0]).toMatchObject({ fontSize: 32 });
  });

  it("keeps the edit and toasts on a non-4xx rejection", async () => {
    vi.useFakeTimers();
    const { ApiError } = await import("~/api/client");
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    api.saveSettings.mockRejectedValueOnce(
      new ApiError("server error", 500, "server_error"),
    );
    settings.update({ fontSize: 33 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    flush();
    expect(settings.value.fontSize).toBe(33);
    expect(showToast).toHaveBeenCalledTimes(1);
  });

  it("aborts an in-flight save when the next save fires", async () => {
    vi.useFakeTimers();
    let resolveFirst!: () => void;
    const first = new Promise<void>((resolve) => {
      resolveFirst = resolve;
    });
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    api.saveSettings.mockReturnValueOnce(first);
    settings.update({ fontSize: 41 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    const firstSignal = api.saveSettings.mock.calls[0][1] as AbortSignal;

    settings.update({ fontSize: 42 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    expect(api.saveSettings).toHaveBeenCalledTimes(2);
    expect(firstSignal.aborted).toBe(true);

    resolveFirst();
    await flushMicrotasks();
    flush();
    // The superseded save's late resolution must not repoint #lastSaved; the
    // store still reflects the newest edit.
    expect(settings.value.fontSize).toBe(42);
  });

  it("swallows an AbortError silently — no toast, no rollback", async () => {
    vi.useFakeTimers();
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    api.saveSettings.mockRejectedValueOnce(
      new DOMException("aborted", "AbortError"),
    );
    settings.update({ fontSize: 34 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    flush();
    expect(settings.value.fontSize).toBe(34);
    expect(showToast).not.toHaveBeenCalled();
  });

  it("reset() clears a pending save and restores defaults", async () => {
    vi.useFakeTimers();
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    settings.update({ fontSize: 44 });
    flush();
    settings.reset();
    flush();
    expect(settings.value.fontSize).toBe(30);
    expect(settings.loaded).toBe(false);

    await vi.advanceTimersByTimeAsync(1000);
    expect(api.saveSettings).not.toHaveBeenCalled();
  });

  it("resetToDefaults() keeps loaded and schedules a defaults save", async () => {
    vi.useFakeTimers();
    const { settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    settings.update({ fontSize: 45 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();

    settings.resetToDefaults();
    flush();
    expect(settings.value.fontSize).toBe(30);
    expect(settings.loaded).toBe(true);

    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(api.saveSettings).toHaveBeenCalledTimes(2);
    expect(api.saveSettings.mock.calls[1][0]).toMatchObject({
      fontSize: 30,
      fontFamily: "literata",
    });
  });
});

describe("toIframeSettings", () => {
  it("resolves user fonts through the registry and maps the frame shape", async () => {
    const { toIframeSettings } = await import("~/lib/settings");
    const iframe = toIframeSettings(
      full({
        fontFamily: "user:minion",
        chapterTitleFontFamily: "user:minion",
        displayMode: "paged",
      }),
    );
    // cssValue ?? getFontFamily: the registry answer wins for user fonts.
    expect(iframe.fontFamily).toBe("'UserFont', serif");
    expect(iframe.chapterTitleFontFamily).toBe("'UserFont', serif");
    expect(iframe.mode).toBe("paged");
    expect(iframe.margins).toEqual({ top: 48, bottom: 48, side: 48 });
    expect(iframe.themeVars).toBeNull();
  });

  it("maps a null chapterTitleFontFamily to null (inherit body font)", async () => {
    const { toIframeSettings } = await import("~/lib/settings");
    expect(toIframeSettings(full()).chapterTitleFontFamily).toBeNull();
  });

  it("save payload shares no nested fontRoles object with the store", async () => {
    vi.useFakeTimers();
    api.getSettings.mockResolvedValueOnce(
      full({ fontRoles: { "user:minion": { regular: "r.ttf" } } }),
    );
    const { DEFAULT_USER_SETTINGS, settings } = await import("~/lib/settings");
    const { flush } = await import("solid-js");
    await settings.load();
    flush();

    // The merged copy is never the module-level default map...
    expect(settings.value.fontRoles).not.toBe(DEFAULT_USER_SETTINGS.fontRoles);
    // ...nor the response's own nested object.
    settings.update({ fontSize: 33 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    const payload = api.saveSettings.mock.calls[0][0];
    expect(payload.fontRoles).not.toBe(settings.value.fontRoles);
    expect(payload.fontRoles).toEqual({ "user:minion": { regular: "r.ttf" } });

    // Mutating the store afterwards leaves the captured payload untouched.
    settings.update({ fontSize: 44 });
    flush();
    await vi.advanceTimersByTimeAsync(500);
    await flushMicrotasks();
    expect(payload.fontSize).toBe(33);
  });
});
