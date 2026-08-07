// Suite for the SPA entry point. Importing the module IS mounting the app, so
// every test resets the module registry and imports it fresh; render() is
// stubbed because what matters here is the order and the arguments, not a
// second real mount (App.test.ts owns that).
import { beforeEach, describe, expect, it, vi } from "vitest";

const stubs = vi.hoisted(() => ({
  render: vi.fn((_thunk: () => unknown, _target: Element) => (): void => {}),
  watchReachability: vi.fn(),
  app: vi.fn(() => null),
}));

vi.mock("@solidjs/web", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return { ...actual, render: stubs.render };
});
vi.mock("~/App", () => ({ default: stubs.app }));
vi.mock("~/lib/fontRegistry", () => ({
  fontRegistry: { watchReachability: stubs.watchReachability },
}));

function root(): HTMLDivElement {
  const target = document.createElement("div");
  target.id = "app";
  document.body.appendChild(target);
  return target;
}

describe("main", () => {
  beforeEach(() => {
    vi.resetModules();
    stubs.render.mockClear();
    stubs.watchReachability.mockClear();
    stubs.app.mockClear();
    document.body.innerHTML = "";
  });

  it("mounts into #app and exports the dispose handle", async () => {
    const target = root();

    const mod = await import("~/main");

    expect(stubs.render).toHaveBeenCalledTimes(1);
    expect(stubs.render.mock.calls[0][1]).toBe(target);
    expect(mod.default).toBeTypeOf("function");
  });

  it("renders the App shell and nothing else", async () => {
    root();

    await import("~/main");

    // render() takes a thunk, so App must not have been called yet.
    expect(stubs.app).not.toHaveBeenCalled();
    stubs.render.mock.calls[0][0]();
    expect(stubs.app).toHaveBeenCalledTimes(1);
  });

  it("arms the font-token watcher before the first paint", async () => {
    // A server restart re-mints the user-font token; the registry refetches on
    // the reachability recovery edge, so the subscription has to exist before
    // anything can render a stale /fonts/user/ URL.
    root();

    await import("~/main");

    expect(stubs.watchReachability).toHaveBeenCalledTimes(1);
    expect(stubs.watchReachability.mock.invocationCallOrder[0]).toBeLessThan(
      stubs.render.mock.invocationCallOrder[0],
    );
  });

  it("throws on a missing root instead of mounting nowhere", async () => {
    await expect(import("~/main")).rejects.toThrow(
      "#app root element not found",
    );

    expect(stubs.watchReachability).not.toHaveBeenCalled();
    expect(stubs.render).not.toHaveBeenCalled();
  });
});
