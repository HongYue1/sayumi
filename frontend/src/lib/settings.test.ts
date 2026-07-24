import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  getSettings: vi.fn(),
  saveSettings: vi.fn(),
}));
const showToast = vi.hoisted(() => vi.fn());

vi.mock("~/api/client", () => {
  class ApiError extends Error {
    readonly status?: number;
    readonly code?: string;

    constructor(message: string, status?: number, code?: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.code = code;
    }
  }

  return { ApiError, ...api };
});

vi.mock("~/lib/fonts", () => ({
  getFontById: (id: string) => (id === "literata" ? { id } : undefined),
  getFontFamily: (id: string) => id,
}));

vi.mock("~/lib/fontRegistry.svelte", () => ({
  fontRegistry: { cssValue: vi.fn(() => undefined) },
  isUserFamilyId: (id: string) => id.startsWith("user:"),
}));

vi.mock("~/lib/toast.svelte", () => ({
  toast: { show: showToast },
}));

vi.mock("~/lib/customThemes.svelte", () => ({
  customThemes: { list: [] },
}));

vi.mock("~/lib/themes", () => ({
  readerThemeVars: vi.fn(() => null),
}));

beforeEach(() => {
  vi.resetModules();
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("settings profile lifecycle", () => {
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
      .mockResolvedValueOnce({ fontSize: 36, fontFamily: "literata" });
    const { settings } = await import("~/lib/settings.svelte");

    const oldLoad = settings.load();
    const oldSignal = api.getSettings.mock.calls[0][0] as AbortSignal;
    settings.reset();
    expect(oldSignal.aborted).toBe(true);

    await settings.load();
    expect(settings.value.fontSize).toBe(36);

    resolveFirst({ fontSize: 18, fontFamily: "literata" });
    await oldLoad;
    expect(settings.value.fontSize).toBe(36);
  });
});

describe("settings save ordering", () => {
  it("does not roll back a newer edit when an older save returns 4xx", async () => {
    vi.useFakeTimers();
    let rejectFirst!: (reason: unknown) => void;
    const first = new Promise<void>((_resolve, reject) => {
      rejectFirst = reject;
    });
    api.saveSettings
      .mockReturnValueOnce(first)
      .mockResolvedValueOnce(undefined);
    const { ApiError } = await import("~/api/client");
    const { settings } = await import("~/lib/settings.svelte");

    settings.update({ fontSize: 31 });
    await vi.advanceTimersByTimeAsync(500);
    expect(api.saveSettings).toHaveBeenCalledOnce();

    settings.update({ fontSize: 32 });
    rejectFirst(new ApiError("invalid settings", 400, "invalid"));
    await Promise.resolve();
    await Promise.resolve();

    expect(settings.value.fontSize).toBe(32);
    expect(showToast).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(500);
    await Promise.resolve();
    expect(api.saveSettings).toHaveBeenCalledTimes(2);
    expect(api.saveSettings.mock.calls[1][0]).toMatchObject({ fontSize: 32 });
  });
});
