import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CustomTheme } from "~/api/client";
import { getTheme, setCustomThemes } from "~/lib/themes";

const { getCustomThemes } = vi.hoisted(() => ({
  getCustomThemes: vi.fn<() => Promise<CustomTheme[]>>(),
}));

vi.mock("~/api/client", () => ({
  getCustomThemes,
  createCustomTheme: vi.fn(),
  updateCustomTheme: vi.fn(),
  deleteCustomTheme: vi.fn(),
}));

const { CustomThemes } = await import("~/lib/customThemes.svelte");

function theme(id: string, name: string): CustomTheme {
  return {
    id,
    name,
    group: "light",
    bg: "#ffffff",
    fg: "#111111",
    accent: "#3366cc",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
} {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("custom theme profile lifecycle", () => {
  beforeEach(() => {
    getCustomThemes.mockReset();
    setCustomThemes([]);
  });

  afterEach(() => {
    setCustomThemes([]);
  });

  it("clears themes on sign-out", async () => {
    getCustomThemes.mockResolvedValueOnce([theme("theme-a", "Profile A")]);
    const store = new CustomThemes();

    await store.activate("profile-a");
    expect(store.list.map((item) => item.id)).toEqual(["theme-a"]);
    expect(getTheme("theme-a").label).toBe("Profile A");

    await store.activate(null);
    expect(store.list).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(getTheme("theme-a").id).toBe("light");
  });

  it("drops a stale response after the active profile changes", async () => {
    const first = deferred<CustomTheme[]>();
    const second = deferred<CustomTheme[]>();
    getCustomThemes
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const store = new CustomThemes();

    const profileALoad = store.activate("profile-a");
    const profileBLoad = store.activate("profile-b");
    second.resolve([theme("theme-b", "Profile B")]);
    await profileBLoad;
    first.resolve([theme("theme-a", "Profile A")]);
    await profileALoad;

    expect(store.list.map((item) => item.id)).toEqual(["theme-b"]);
    expect(store.loaded).toBe(true);
    expect(getTheme("theme-a").id).toBe("light");
    expect(getTheme("theme-b").label).toBe("Profile B");
  });

  it("retries after a non-fatal load failure", async () => {
    getCustomThemes
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce([theme("theme-a", "Recovered")]);
    const store = new CustomThemes();

    await store.activate("profile-a");
    expect(store.loaded).toBe(false);

    await store.load();
    expect(store.list.map((item) => item.id)).toEqual(["theme-a"]);
    expect(store.loaded).toBe(true);
  });
});
