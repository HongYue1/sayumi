import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createComponent, flush } from "solid-js";
import { render } from "@solidjs/web";
import type * as ApiClient from "~/api/client";
import type { FlairDef } from "~/api/client";
import LibraryRoute from "~/routes/Library";
import { library, SORT_OPTIONS } from "~/lib/library";

// The route's own wiring is what is under test here, so every child component
// is a null stub and every singleton the route reaches for is a controllable
// double. The one exception is `library` itself: the boot path, the flair chip
// row and the no-results branch all read real derived state, and stubbing the
// store would make those assertions vacuous. It is driven through the mocked
// transport instead.
const api = vi.hoisted(() => ({
  getBooks: vi.fn(),
  getFlairs: vi.fn(),
  createFlair: vi.fn(),
  deleteFlair: vi.fn(),
  setBookFlair: vi.fn(),
  uploadBook: vi.fn(),
  updateBookMeta: vi.fn(),
  uploadCover: vi.fn(),
  deleteBook: vi.fn(),
  rescanLibrary: vi.fn(),
}));

const applyTheme = vi.hoisted(() => vi.fn());
const navigate = vi.hoisted(() => vi.fn());
const showToast = vi.hoisted(() => vi.fn());

// settings.load() resolves on both the success and the failure path and flips
// `loaded` only on success -- that asymmetry is the whole of H1, so the stub
// reproduces it exactly rather than rejecting.
const settingsStub = vi.hoisted(() => ({
  loaded: false,
  value: { theme: "catppuccin" },
  load: vi.fn(),
}));

const sessionStub = vi.hoisted(() => ({ profile: "p" as string | null }));

// `library` is an app-lifetime singleton, so every test activates its own
// profile. activate() resets books, flairs, filters and the query, which is
// the only way to stop one test's state leaking into the next.
let profileSeq = 0;

vi.mock("~/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof ApiClient>();
  return { ...actual, ...api };
});
vi.mock("~/lib/settings", () => ({ settings: settingsStub }));
vi.mock("~/lib/session", () => ({ session: sessionStub }));
vi.mock("~/lib/theme", () => ({ applyTheme }));
vi.mock("~/lib/router", () => ({ router: { navigate } }));
vi.mock("~/lib/toast", () => ({ toast: { show: showToast } }));
vi.mock("~/lib/reachability", () => ({
  isReachable: () => true,
  reportReachable: () => {},
  reportUnreachable: () => {},
  subscribeReachability: () => () => {},
}));

vi.mock("~/components/library/BookCard", () => ({ default: () => null }));
vi.mock("~/components/library/ThemeDropdown", () => ({ default: () => null }));
vi.mock("~/components/library/ProfileMenu", () => ({ default: () => null }));
vi.mock("~/components/library/ProfileDialog", () => ({ default: () => null }));
vi.mock("~/components/library/EditBookDialog", () => ({
  default: () => null,
}));
vi.mock("~/components/library/ShareDialog", () => ({ default: () => null }));

function book(
  p: Partial<ApiClient.BookMeta> & { id: string; title: string },
): ApiClient.BookMeta {
  return {
    author: "",
    language: "",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "",
    chapterCount: 0,
    progress: 0,
    ...p,
  };
}

let dispose: (() => void) | null = null;

// load() awaits getBooks and getFlairs before it writes the stores, so a
// couple of microtask turns is not enough to see a fully seeded library.
async function settle(): Promise<void> {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
    vi.advanceTimersByTime(0);
    flush();
  }
}

async function mount(): Promise<HTMLElement> {
  const host = document.createElement("div");
  document.body.appendChild(host);
  dispose = render(() => createComponent(LibraryRoute, {}), host);
  await settle();
  return host;
}

function page(host: HTMLElement): HTMLElement {
  const el = host.querySelector<HTMLElement>(".lib-page");
  if (!el) throw new Error("lib-page did not render");
  return el;
}

/**
 * A drag event carrying a file manifest. happy-dom has no DragEvent
 * constructor with a usable DataTransfer, and `hasFiles` only ever reads
 * `dataTransfer.types`, so a plain Event with the field defined is a faithful
 * stand-in for what the handlers actually consume.
 */
function drag(type: string, withFiles = true): Event {
  const e = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperty(e, "dataTransfer", {
    value: { types: withFiles ? ["Files"] : ["text/plain"], files: [] },
  });
  return e;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.clearAllMocks();
  localStorage.clear();
  settingsStub.loaded = false;
  settingsStub.value = { theme: "catppuccin" };
  settingsStub.load.mockImplementation(async () => {});
  profileSeq += 1;
  sessionStub.profile = `p${profileSeq}`;
  api.getBooks.mockResolvedValue([]);
  api.getFlairs.mockResolvedValue([]);
});

afterEach(() => {
  if (dispose) {
    dispose();
    dispose = null;
  }
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("Library route: theme on boot (H1)", () => {
  it("leaves the theme alone when the settings request failed", async () => {
    // The failure path: load() resolves, `loaded` stays false, and
    // settings.value is still the compile-time default. Applying it here would
    // overwrite the user's real theme -- and persist the guess to localStorage
    // -- because the network blipped.
    await mount();
    expect(settingsStub.load).toHaveBeenCalledOnce();
    expect(applyTheme).not.toHaveBeenCalled();
  });

  it("applies the loaded theme once settings genuinely loaded", async () => {
    settingsStub.load.mockImplementation(async () => {
      settingsStub.loaded = true;
      settingsStub.value = { theme: "nord" };
    });
    await mount();
    expect(applyTheme).toHaveBeenCalledWith("nord");
  });

  it("does not apply a theme that resolves after unmount", async () => {
    let release = (): void => {};
    settingsStub.load.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          release = () => {
            settingsStub.loaded = true;
            settingsStub.value = { theme: "nord" };
            resolve();
          };
        }),
    );
    await mount();
    dispose?.();
    dispose = null;
    release();
    await settle();
    expect(applyTheme).not.toHaveBeenCalled();
  });
});

describe("Library route: drag overlay depth (H2)", () => {
  it("counts both dragenters that land inside one flush window", async () => {
    const host = await mount();
    const root = page(host);
    // Read-modify-write through the accessor lost one of these: a signal read
    // immediately after its own write still returns the pre-write value, so
    // the counter finished at 1 and one dragleave was enough to hide a zone
    // that two dragenters had raised.
    root.dispatchEvent(drag("dragenter"));
    root.dispatchEvent(drag("dragenter"));
    flush();
    expect(host.querySelector(".lib-dropzone")).not.toBeNull();

    root.dispatchEvent(drag("dragleave"));
    flush();
    expect(host.querySelector(".lib-dropzone")).not.toBeNull();

    root.dispatchEvent(drag("dragleave"));
    flush();
    expect(host.querySelector(".lib-dropzone")).toBeNull();
  });

  it("clears a stranded overlay on window dragend", async () => {
    const host = await mount();
    page(host).dispatchEvent(drag("dragenter"));
    flush();
    expect(host.querySelector(".lib-dropzone")).not.toBeNull();

    // A drag that ends outside the window never delivers a matching
    // dragleave, so without this reset the fixed inset:0 overlay stayed over
    // the whole viewport until reload.
    window.dispatchEvent(new Event("dragend"));
    flush();
    expect(host.querySelector(".lib-dropzone")).toBeNull();
  });

  it("never lets the counter go negative on an unmatched dragleave", async () => {
    const host = await mount();
    const root = page(host);
    root.dispatchEvent(drag("dragleave"));
    root.dispatchEvent(drag("dragleave"));
    flush();
    expect(host.querySelector(".lib-dropzone")).toBeNull();

    root.dispatchEvent(drag("dragenter"));
    flush();
    expect(host.querySelector(".lib-dropzone")).not.toBeNull();
  });

  it("ignores drags that carry no files", async () => {
    const host = await mount();
    page(host).dispatchEvent(drag("dragenter", false));
    flush();
    expect(host.querySelector(".lib-dropzone")).toBeNull();
  });

  it("tears the window listeners down with the route", async () => {
    const host = await mount();
    dispose?.();
    dispose = null;
    // Nothing is mounted to observe, so the only thing being asserted is that
    // the teardown ran without throwing and left no live handler behind.
    expect(() => window.dispatchEvent(new Event("dragend"))).not.toThrow();
    expect(host.querySelector(".lib-dropzone")).toBeNull();
  });
});

describe("Library route: sort menu (H4, L1)", () => {
  function openMenu(host: HTMLElement): HTMLElement {
    const trigger = host.querySelector<HTMLButtonElement>(".lib-sort-trigger");
    if (!trigger) throw new Error("sort trigger did not render");
    trigger.click();
    flush();
    const menu = host.querySelector<HTMLElement>(".lib-sort-menu");
    if (!menu) throw new Error("sort menu did not open");
    return menu;
  }

  it("closes on Tab rather than wrapping focus inside itself", async () => {
    const host = await mount();
    const menu = openMenu(host);
    // WCAG 2.1.2: the popover has no visible close control, so containment
    // left Escape as the only exit.
    menu.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Tab", bubbles: true }),
    );
    flush();
    expect(host.querySelector(".lib-sort-menu")).toBeNull();
  });

  it("still closes on Escape", async () => {
    const host = await mount();
    const menu = openMenu(host);
    menu.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    );
    flush();
    expect(host.querySelector(".lib-sort-menu")).toBeNull();
  });

  it("renders exactly one item per sort option", async () => {
    // Guards the <For> -> .map() conversion: SORT_OPTIONS is frozen, so the
    // reconciler node was pure overhead, but the rewrite must still emit the
    // full set once.
    const host = await mount();
    const menu = openMenu(host);
    expect(menu.querySelectorAll('[role="menuitemradio"]')).toHaveLength(
      SORT_OPTIONS.length,
    );
  });
});

describe("Library route: live regions (M4)", () => {
  it("mounts both regions empty, before they ever carry text", async () => {
    // A region inserted in the same tick as its text gives AT no "before" to
    // diff against, and NVDA and JAWS drop the announcement outright.
    const host = await mount();
    const alert = host.querySelector("p.lib-error.lib-live[role='alert']");
    const status = host.querySelector("p.lib-state.lib-live[role='status']");
    expect(alert).not.toBeNull();
    expect(status).not.toBeNull();
    expect(alert?.textContent).toBe("");
    expect(status?.textContent).toBe("");
  });

  it("announces the empty result set through the status region", async () => {
    api.getBooks.mockResolvedValue([book({ id: "1", title: "Dune" })]);
    const host = await mount();
    expect(library.books).toHaveLength(1);

    library.setQuery("zzzz");
    vi.advanceTimersByTime(140);
    flush();

    const status = host.querySelector("p.lib-state.lib-live[role='status']");
    expect(status?.textContent).toContain("No books match");

    // …and the no-results block must not be a second, competing status region.
    const noresults = host.querySelector(".lib-noresults");
    expect(noresults).not.toBeNull();
    expect(noresults?.getAttribute("role")).toBeNull();
  });
});

describe("Library route: custom flair delete (M1)", () => {
  const custom: FlairDef = {
    id: "cf1",
    label: "Favourites",
    color: "#3b82f6",
  };

  it("confirms, naming the flair, before removing it", async () => {
    api.getBooks.mockResolvedValue([book({ id: "1", title: "Dune" })]);
    api.getFlairs.mockResolvedValue([custom]);
    const confirmMock = vi.fn((_message?: string) => false);
    vi.stubGlobal("confirm", confirmMock);

    const host = await mount();
    const del = host.querySelector<HTMLButtonElement>(".lib-chip-del");
    expect(del).not.toBeNull();

    del?.click();
    flush();
    expect(confirmMock).toHaveBeenCalledOnce();
    expect(confirmMock.mock.calls[0]?.[0]).toContain("Favourites");
    expect(api.deleteFlair).not.toHaveBeenCalled();
  });

  it("removes the flair once the confirmation is accepted", async () => {
    api.getBooks.mockResolvedValue([book({ id: "1", title: "Dune" })]);
    api.getFlairs.mockResolvedValue([custom]);
    api.deleteFlair.mockResolvedValue(undefined);
    vi.stubGlobal(
      "confirm",
      vi.fn(() => true),
    );

    const host = await mount();
    host.querySelector<HTMLButtonElement>(".lib-chip-del")?.click();
    await settle();
    expect(api.deleteFlair).toHaveBeenCalled();
  });
});
