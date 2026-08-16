// Suite for the app shell: the boot sequence, the profile-owned activation
// chain, the global keyboard shortcuts and the top-level Switch. The shell had
// no coverage at all before this, which is how the theme-cache defect below
// survived a fix to its own copy in Library.tsx.
//
// Harness notes:
//   - Every store the shell reaches for is an app-lifetime singleton, so each
//     test calls vi.resetModules() and re-imports the whole graph through
//     loadShell(). Without that, `ready` never returns to false and the boot
//     assertions would only be true for whichever test ran first (the
//     session.test.ts pattern, applied to a component).
//   - Child routes and overlays are marker <div>s: this suite is about the
//     wiring, and rendering the real Read route would drag in the iframe.
//   - applyTheme is stubbed but getTheme/getCachedThemeId stay real (spread
//     over importOriginal), so the cached-id path reads real localStorage.
//     What applyTheme WRITES is the point of the theme tests: it persists
//     whatever it paints into localStorage["sayumi:theme"], so a call with
//     the wrong id is not a repaint, it is data loss.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createComponent, flush } from "solid-js";
import { render } from "@solidjs/web";
import type * as ApiClient from "~/api/client";

const api = vi.hoisted(() => ({
  getAuthStatus: vi.fn(),
  getCustomThemes: vi.fn(),
  getSettings: vi.fn(),
  saveSettings: vi.fn(),
  logout: vi.fn(),
  getBooks: vi.fn(),
  getFlairs: vi.fn(),
}));

const applyTheme = vi.hoisted(() => vi.fn());
const stubs = vi.hoisted(() => ({
  marker: (id: string) => () => {
    const el = document.createElement("div");
    el.dataset.stub = id;
    return el;
  },
  read: vi.fn((props: { bookId: string }) => {
    const el = document.createElement("div");
    el.dataset.stub = "read";
    el.dataset.bookId = props.bookId;
    return el;
  }),
}));

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});
vi.mock("~/lib/theme", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    applyTheme,
  };
});
vi.mock("~/routes/Login", () => ({ default: stubs.marker("login") }));
vi.mock("~/routes/Library", () => ({ default: stubs.marker("library") }));
vi.mock("~/routes/Read", () => ({ default: stubs.read }));
vi.mock("~/components/Toaster", () => ({ default: stubs.marker("toaster") }));
vi.mock("~/components/OfflineBanner", () => ({
  default: stubs.marker("offline"),
}));
vi.mock("~/components/CommandPalette", () => ({
  default: stubs.marker("palette"),
}));
vi.mock("~/components/ShortcutsHelp", () => ({
  default: stubs.marker("shortcuts"),
}));
vi.mock("~/lib/reachability", () => ({
  isReachable: () => true,
  reportReachable: () => {},
  reportUnreachable: () => {},
  subscribeReachability: () => () => {},
}));

async function loadShell() {
  const [
    app,
    uiMod,
    sessionMod,
    settingsMod,
    customThemesMod,
    themesMod,
    libraryMod,
    routerMod,
  ] = await Promise.all([
    import("~/App"),
    import("~/lib/ui"),
    import("~/lib/session"),
    import("~/lib/settings"),
    import("~/lib/customThemes"),
    import("~/lib/themes"),
    import("~/lib/library"),
    import("~/lib/router"),
  ]);
  return {
    App: app.default,
    ui: uiMod.ui,
    session: sessionMod.session,
    settings: settingsMod.settings,
    customThemes: customThemesMod.customThemes,
    setCustomThemes: themesMod.setCustomThemes,
    library: libraryMod.library,
    router: routerMod.router,
  };
}

type Shell = Awaited<ReturnType<typeof loadShell>>;

let host: HTMLDivElement;
let dispose: (() => void) | undefined;

function mount(shell: Shell): void {
  dispose = render(() => createComponent(shell.App, {}), host);
}

async function boot(): Promise<Shell> {
  const shell = await loadShell();
  mount(shell);
  await settle();
  return shell;
}

async function settle(): Promise<void> {
  for (let i = 0; i < 8; i += 1) {
    await Promise.resolve();
    flush();
  }
}

async function signedIn(profile = "ada"): Promise<Shell> {
  api.getAuthStatus.mockResolvedValue({ authenticated: true, profile });
  const shell = await boot();
  await vi.waitFor(() => {
    flush();
    expect(shell.session.profile).toBe(profile);
  });
  await settle();
  return shell;
}

function press(init: KeyboardEventInit): KeyboardEvent {
  const e = new KeyboardEvent("keydown", { cancelable: true, ...init });
  window.dispatchEvent(e);
  flush();
  return e;
}

function stub(id: string): Element | null {
  return host.querySelector(`[data-stub="${id}"]`);
}

describe("App shell", () => {
  beforeEach(() => {
    vi.resetModules();
    host = document.createElement("div");
    document.body.appendChild(host);
    localStorage.clear();
    window.location.hash = "#/";
    applyTheme.mockClear();
    stubs.read.mockClear();
    api.getAuthStatus.mockReset();
    api.getCustomThemes.mockReset();
    api.getSettings.mockReset();
    api.saveSettings.mockReset();
    api.logout.mockReset();
    api.getBooks.mockReset();
    api.getFlairs.mockReset();
    api.getAuthStatus.mockResolvedValue({ authenticated: false, profile: "" });
    api.getCustomThemes.mockResolvedValue([]);
    api.getSettings.mockResolvedValue({ theme: "nord" });
    api.saveSettings.mockResolvedValue(undefined);
    api.logout.mockResolvedValue(undefined);
    api.getBooks.mockResolvedValue([]);
    api.getFlairs.mockResolvedValue([]);
  });

  afterEach(() => {
    dispose?.();
    dispose = undefined;
    document.body.innerHTML = "";
    window.location.hash = "#/";
  });

  it("holds the boot placeholder until the session resolves", async () => {
    let release = (): void => {};
    api.getAuthStatus.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => {
            resolve({ authenticated: false, profile: "" });
          };
        }),
    );

    const shell = await boot();
    const placeholder = host.querySelector(".boot");
    expect(placeholder).not.toBeNull();
    expect(placeholder?.getAttribute("role")).toBe("status");
    expect(placeholder?.getAttribute("aria-busy")).toBe("true");
    expect(stub("login")).toBeNull();

    release();
    await settle();

    expect(shell.session.status).toBe("signed-out");
    expect(host.querySelector(".boot")).toBeNull();
    expect(stub("login")).not.toBeNull();
  });

  it("shows a blocking retry surface while sign-in status is unknown", async () => {
    const { ApiError } = await import("~/api/client");
    api.getAuthStatus.mockRejectedValueOnce(
      new ApiError("Could not reach the server.", undefined, "network_error"),
    );

    const shell = await boot();

    expect(shell.session.status).toBe("unavailable");
    expect(shell.session.authenticated).toBe(false);
    expect(stub("login")).toBeNull();
    expect(stub("library")).toBeNull();
    expect(stub("offline")).not.toBeNull();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain(
      "Your sign-in status is unknown",
    );

    const retry = Array.from(host.querySelectorAll("button")).find(
      (button) => button.textContent?.trim() === "Try again",
    );
    expect(retry).toBeDefined();
    retry?.click();
    await settle();

    expect(api.getAuthStatus).toHaveBeenCalledTimes(2);
    expect(shell.session.status).toBe("signed-out");
    expect(stub("login")).not.toBeNull();
  });

  it("re-applies the cached theme on mount", async () => {
    localStorage.setItem("sayumi:theme", "nord");

    await boot();

    expect(applyTheme.mock.calls[0]).toEqual(["nord"]);
  });

  it("falls back to light for a visitor with no cached theme", async () => {
    await boot();

    expect(applyTheme.mock.calls[0]).toEqual(["light"]);
  });

  it("starts the session probe exactly once on mount", async () => {
    await boot();

    expect(api.getAuthStatus).toHaveBeenCalledTimes(1);
  });

  it("activates settings for the authenticated profile without route help", async () => {
    const shell = await signedIn("ada");

    expect(api.getSettings).toHaveBeenCalledTimes(1);
    expect(shell.settings.loaded).toBe(true);
    expect(shell.settings.value.theme).toBe("nord");
  });

  it("applies the saved theme through the centralized owner", async () => {
    localStorage.setItem("sayumi:theme", "light");
    api.getAuthStatus.mockResolvedValue({
      authenticated: true,
      profile: "ada",
    });
    api.getSettings.mockResolvedValue({ theme: "nord" });

    const shell = await loadShell();
    mount(shell);

    await vi.waitFor(() => {
      flush();
      expect(shell.customThemes.loaded).toBe(true);
    });
    await settle();

    expect(applyTheme.mock.calls.map((call) => call[0])).toEqual([
      "light",
      "nord",
    ]);
  });

  it("leaves the theme alone when the settings load failed", async () => {
    // A failed activation leaves the compile-time default in place. Painting
    // it would persist a guess over the cached server theme, so the centralized
    // effect must stay gated on successful settings ownership.
    localStorage.setItem("sayumi:theme", "nord");
    api.getAuthStatus.mockResolvedValue({
      authenticated: true,
      profile: "ada",
    });
    api.getSettings.mockRejectedValue(new Error("offline"));

    const shell = await loadShell();
    mount(shell);

    await vi.waitFor(() => {
      flush();
      expect(shell.customThemes.loaded).toBe(true);
    });
    await settle();

    expect(applyTheme.mock.calls.map((call) => call[0])).toEqual(["nord"]);
    expect(localStorage.getItem("sayumi:theme")).toBe("nord");
  });

  it("leaves the theme alone when the custom-theme registry failed", async () => {
    localStorage.setItem("sayumi:theme", "nord");
    api.getAuthStatus.mockResolvedValue({
      authenticated: true,
      profile: "ada",
    });
    api.getCustomThemes.mockRejectedValue(new Error("offline"));

    const shell = await loadShell();
    mount(shell);

    await vi.waitFor(() => {
      flush();
      expect(shell.session.profile).toBe("ada");
    });
    await settle();

    expect(shell.customThemes.loaded).toBe(false);
    expect(applyTheme.mock.calls.map((call) => call[0])).toEqual([
      "nord",
      "nord",
    ]);
  });

  it("repaints a saved custom theme when its registry definition arrives", async () => {
    let releaseThemes = (_themes: ApiClient.CustomTheme[]): void => {};
    api.getSettings.mockResolvedValue({ theme: "custom:ink" });
    api.getCustomThemes.mockImplementation(
      () =>
        new Promise<ApiClient.CustomTheme[]>((resolve) => {
          releaseThemes = resolve;
        }),
    );

    const shell = await signedIn("ada");
    await vi.waitFor(() => {
      flush();
      expect(shell.settings.loaded).toBe(true);
      expect(
        applyTheme.mock.calls.some((call) => call[0] === "custom:ink"),
      ).toBe(true);
    });

    releaseThemes([
      {
        id: "custom:ink",
        name: "Ink",
        group: "dark",
        bg: "#101820",
        fg: "#f4f1ea",
        accent: "#ef8354",
        createdAt: "2026-08-09T00:00:00Z",
        updatedAt: "2026-08-09T00:00:00Z",
      },
    ]);
    await vi.waitFor(() => {
      flush();
      expect(shell.customThemes.loaded).toBe(true);
      const calls = applyTheme.mock.calls.filter(
        (call) => call[0] === "custom:ink",
      );
      expect(calls.at(-1)?.[1]).toMatchObject({
        id: "custom:ink",
        bg: "#101820",
        fg: "#f4f1ea",
      });
    });

    const beforeEdit = applyTheme.mock.calls.length;
    shell.setCustomThemes([
      {
        id: "custom:ink",
        label: "Ink edited",
        group: "dark",
        bg: "#202830",
        fg: "#f8f5ef",
        accent: "#7dd3fc",
      },
    ]);
    await settle();
    expect(applyTheme.mock.calls).toHaveLength(beforeEdit + 1);
    expect(applyTheme.mock.calls.at(-1)?.[1]).toMatchObject({
      id: "custom:ink",
      bg: "#202830",
      fg: "#f8f5ef",
    });
  });

  it("does not let a superseded profile's load paint the theme", async () => {
    localStorage.setItem("sayumi:theme", "light");
    let releaseAda = (): void => {};
    api.getCustomThemes.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseAda = () => {
            resolve([]);
          };
        }),
    );
    api.getCustomThemes.mockResolvedValue([]);
    api.getAuthStatus.mockResolvedValue({
      authenticated: true,
      profile: "ada",
    });

    const shell = await loadShell();
    mount(shell);
    await vi.waitFor(() => {
      flush();
      expect(shell.session.profile).toBe("ada");
    });

    // The session moves on while ada's registry request is still in flight.
    api.getAuthStatus.mockResolvedValue({ authenticated: true, profile: "bo" });
    await shell.session.init();
    await vi.waitFor(() => {
      flush();
      expect(shell.customThemes.loaded).toBe(true);
    });
    releaseAda();
    await settle();

    expect(shell.session.profile).toBe("bo");
    expect(applyTheme.mock.calls.map((call) => call[0])).toEqual([
      "light",
      "nord",
      "nord",
    ]);
  });

  it("toggles the command palette on the ctrl chord and swallows it", async () => {
    const shell = await signedIn();

    const opened = press({ key: "k", ctrlKey: true });
    expect(shell.ui.palette).toBe(true);
    expect(shell.ui.shortcuts).toBe(false);
    expect(opened.defaultPrevented).toBe(true);

    const closed = press({ key: "k", ctrlKey: true });
    expect(shell.ui.palette).toBe(false);
    expect(closed.defaultPrevented).toBe(true);
  });

  it("accepts the meta chord and its shifted key name", async () => {
    const shell = await signedIn();

    const e = press({ key: "K", metaKey: true });

    expect(shell.ui.palette).toBe(true);
    expect(e.defaultPrevented).toBe(true);
  });

  it("leaves AltGr+K to the keyboard layout", async () => {
    // AltGr reaches the DOM as ctrl+alt on Windows and most Linux layouts,
    // where AltGr+K is a character. frame.ts's key handler already excludes
    // it inside the book iframe; this is the parent-document half.
    const shell = await signedIn();

    const e = press({ key: "k", ctrlKey: true, altKey: true });

    expect(shell.ui.palette).toBe(false);
    expect(e.defaultPrevented).toBe(false);
  });

  it("ignores every shortcut before sign-in", async () => {
    const shell = await boot();

    const chord = press({ key: "k", ctrlKey: true });
    const question = press({ key: "?" });

    expect(shell.ui.palette).toBe(false);
    expect(shell.ui.shortcuts).toBe(false);
    expect(chord.defaultPrevented).toBe(false);
    expect(question.defaultPrevented).toBe(false);
  });

  it("opens the shortcuts sheet on ?", async () => {
    const shell = await signedIn();

    const e = press({ key: "?" });

    expect(shell.ui.shortcuts).toBe(true);
    expect(shell.ui.palette).toBe(false);
    expect(e.defaultPrevented).toBe(true);
  });

  it("ignores ? while the user is typing in a form control", async () => {
    const shell = await signedIn();

    for (const tag of ["input", "textarea", "select"] as const) {
      const field = document.createElement(tag);
      document.body.appendChild(field);
      field.focus();
      expect(document.activeElement).toBe(field);

      const e = press({ key: "?" });

      expect(shell.ui.shortcuts).toBe(false);
      expect(e.defaultPrevented).toBe(false);
      field.remove();
    }
  });

  it("leaves shell shortcuts with a contenteditable region", async () => {
    const shell = await signedIn();
    const region = document.createElement("div");
    region.setAttribute("contenteditable", "true");
    region.tabIndex = 0;
    document.body.appendChild(region);
    region.focus();
    expect(document.activeElement).toBe(region);
    expect(region.isContentEditable).toBe(true);

    const question = press({ key: "?" });
    const palette = press({ key: "k", ctrlKey: true });

    expect(shell.ui.shortcuts).toBe(false);
    expect(shell.ui.palette).toBe(false);
    expect(question.defaultPrevented).toBe(false);
    expect(palette.defaultPrevented).toBe(false);
    region.remove();
  });

  it("leaves composing shell shortcuts untouched", async () => {
    const shell = await signedIn();

    const palette = press({ key: "k", ctrlKey: true, isComposing: true });
    const question = press({ key: "?", isComposing: true });

    expect(shell.ui.palette).toBe(false);
    expect(shell.ui.shortcuts).toBe(false);
    expect(palette.defaultPrevented).toBe(false);
    expect(question.defaultPrevented).toBe(false);
  });

  it("leaves modified ? to whoever owns that chord", async () => {
    const shell = await signedIn();

    const ctrl = press({ key: "?", ctrlKey: true });
    const meta = press({ key: "?", metaKey: true });

    expect(shell.ui.shortcuts).toBe(false);
    expect(ctrl.defaultPrevented).toBe(false);
    expect(meta.defaultPrevented).toBe(false);
  });

  it("stops listening once the shell unmounts", async () => {
    const shell = await signedIn();
    dispose?.();
    dispose = undefined;

    const e = press({ key: "k", ctrlKey: true });

    expect(shell.ui.palette).toBe(false);
    expect(e.defaultPrevented).toBe(false);
  });

  it("hands the reader its decoded book id", async () => {
    window.location.hash = "#/read/the%20hobbit";

    const shell = await signedIn();

    expect(shell.router.route.path).toBe("/read/:id");
    expect(stub("read")?.getAttribute("data-book-id")).toBe("the hobbit");
    expect(stubs.read).toHaveBeenCalledTimes(1);
  });

  it("remounts the reader on a new book id rather than reusing it", async () => {
    window.location.hash = "#/read/dune";
    await signedIn();
    expect(stubs.read).toHaveBeenCalledTimes(1);

    window.location.hash = "#/read/hyperion";
    window.dispatchEvent(new Event("hashchange"));
    await settle();

    expect(stubs.read).toHaveBeenCalledTimes(2);
    expect(stub("read")?.getAttribute("data-book-id")).toBe("hyperion");
  });

  it("re-activates the library store when the profile changes", async () => {
    const shell = await signedIn("ada");
    await shell.library.loadForProfile("ada");
    expect(api.getBooks).toHaveBeenCalledTimes(1);

    api.getAuthStatus.mockResolvedValue({ authenticated: true, profile: "bo" });
    await shell.session.init();
    await settle();

    // activate() cleared the cached load, so the next read refetches instead
    // of serving ada's books to bo.
    await shell.library.load();
    expect(api.getBooks).toHaveBeenCalledTimes(2);
  });

  it("closes global overlays on sign-out", async () => {
    const shell = await signedIn("ada");
    press({ key: "k", ctrlKey: true });
    expect(shell.ui.palette).toBe(true);

    await shell.session.logout();
    await settle();

    expect(shell.session.profile).toBeNull();
    expect(shell.ui.palette).toBe(false);
    expect(stub("login")).not.toBeNull();
  });

  it("keeps overlays open across a profile switch", async () => {
    // Only a sign-out is a teardown. The palette's contents are profile-owned
    // and were just cleared by activate(), so what stays open is an empty
    // palette, not another profile's commands. A direct switch like this one
    // is synthesized: the UI routes profile changes through sign-out.
    const shell = await signedIn("ada");
    press({ key: "k", ctrlKey: true });
    expect(shell.ui.palette).toBe(true);

    api.getAuthStatus.mockResolvedValue({ authenticated: true, profile: "bo" });
    await shell.session.init();
    await settle();

    expect(shell.session.profile).toBe("bo");
    expect(shell.ui.palette).toBe(true);
  });
});
