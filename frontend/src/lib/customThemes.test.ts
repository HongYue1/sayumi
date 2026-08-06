import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CustomTheme, CustomThemeInput } from "~/api/client";
import { autoAccent, getTheme, setCustomThemes } from "~/lib/themes";

const {
  getCustomThemes,
  createCustomTheme,
  updateCustomTheme,
  deleteCustomTheme,
  toastShow,
} = vi.hoisted(() => ({
  getCustomThemes: vi.fn<() => Promise<CustomTheme[]>>(),
  createCustomTheme: vi.fn<() => Promise<CustomTheme>>(),
  updateCustomTheme: vi.fn<() => Promise<CustomTheme>>(),
  deleteCustomTheme: vi.fn<() => Promise<void>>(),
  toastShow: vi.fn<(message: string) => void>(),
}));

vi.mock("~/api/client", () => ({
  getCustomThemes,
  createCustomTheme,
  updateCustomTheme,
  deleteCustomTheme,
}));

vi.mock("~/lib/toast", () => ({ toast: { show: toastShow } }));

const { CustomThemes } = await import("~/lib/customThemes");

function theme(id: string, name: string, accent = "#3366cc"): CustomTheme {
  return {
    id,
    name,
    group: "light",
    bg: "#ffffff",
    fg: "#111111",
    accent,
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

const INPUT: CustomThemeInput = {
  name: "Made",
  group: "light",
  bg: "#ffffff",
  fg: "#111111",
  accent: "",
};

describe("custom theme profile lifecycle", () => {
  beforeEach(() => {
    getCustomThemes.mockReset();
    createCustomTheme.mockReset();
    updateCustomTheme.mockReset();
    deleteCustomTheme.mockReset();
    toastShow.mockReset();
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

  it("keeps a create that landed while the first load was in flight", async () => {
    const pending = deferred<CustomTheme[]>();
    getCustomThemes.mockReturnValueOnce(pending.promise);
    createCustomTheme.mockResolvedValueOnce(theme("made", "Made"));
    const store = new CustomThemes();

    const load = store.activate("profile-a");
    await store.create(INPUT);
    expect(store.list.map((item) => item.id)).toEqual(["made"]);

    // The GET was issued before the POST, so its list cannot contain the new
    // theme; publishing it would erase a write the server already accepted.
    pending.resolve([]);
    await load;

    expect(store.list.map((item) => item.id)).toEqual(["made"]);
    expect(getTheme("made").label).toBe("Made");
  });

  it("keeps a delete that landed while the first load was in flight", async () => {
    const pending = deferred<CustomTheme[]>();
    getCustomThemes.mockReturnValueOnce(pending.promise);
    deleteCustomTheme.mockResolvedValueOnce(undefined);
    const store = new CustomThemes();

    const load = store.activate("profile-a");
    await store.remove("gone");

    pending.resolve([theme("keep", "Keep"), theme("gone", "Gone")]);
    await load;

    expect(store.list.map((item) => item.id)).toEqual([]);
  });

  it("leaves loaded false when a stale load is dropped so a retry refetches", async () => {
    const pending = deferred<CustomTheme[]>();
    getCustomThemes
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce([theme("made", "Made")]);
    createCustomTheme.mockResolvedValueOnce(theme("made", "Made"));
    const store = new CustomThemes();

    const load = store.activate("profile-a");
    await store.create(INPUT);
    pending.resolve([]);
    await load;
    expect(store.loaded).toBe(false);

    await store.load();

    expect(store.loaded).toBe(true);
    expect(getCustomThemes).toHaveBeenCalledTimes(2);
    expect(store.list.map((item) => item.id)).toEqual(["made"]);
  });

  it("resolves a blank accent through autoAccent", async () => {
    getCustomThemes.mockResolvedValueOnce([theme("auto", "Auto", "")]);
    const store = new CustomThemes();

    await store.activate("profile-a");

    const [only] = store.list;
    expect(only?.accent).toBe(autoAccent("#ffffff", "#111111"));
  });

  it("toasts once when a create fails for real", async () => {
    getCustomThemes.mockResolvedValueOnce([]);
    createCustomTheme.mockRejectedValueOnce(new Error("500"));
    const store = new CustomThemes();
    await store.activate("profile-a");

    expect(await store.create(INPUT)).toBeNull();
    expect(toastShow).toHaveBeenCalledTimes(1);
    expect(toastShow).toHaveBeenCalledWith("Couldn't save theme");
  });

  it("stays silent when a create is aborted", async () => {
    getCustomThemes.mockResolvedValueOnce([]);
    createCustomTheme.mockRejectedValueOnce(
      new DOMException("aborted", "AbortError"),
    );
    const store = new CustomThemes();
    await store.activate("profile-a");

    expect(await store.create(INPUT)).toBeNull();
    expect(toastShow).not.toHaveBeenCalled();
  });

  it("mirrors an update into the shared theme registry", async () => {
    getCustomThemes.mockResolvedValueOnce([theme("t1", "Before")]);
    updateCustomTheme.mockResolvedValueOnce(theme("t1", "After"));
    const store = new CustomThemes();

    await store.activate("profile-a");
    expect(getTheme("t1").label).toBe("Before");

    const def = await store.update("t1", INPUT);

    expect(def?.label).toBe("After");
    expect(store.list.map((item) => item.label)).toEqual(["After"]);
    expect(getTheme("t1").label).toBe("After");
  });
});
