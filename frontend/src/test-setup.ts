import { afterEach } from "vitest";
import { Window } from "happy-dom";

// Node 22+ ships an experimental Web Storage implementation that defines a
// `localStorage` accessor on the global object. It stays inert unless the
// process was started with --localstorage-file, and because vitest's happy-dom
// environment uses a single object for both `window` and `globalThis`, that
// dead accessor shadows the Storage instance happy-dom installs. sessionStorage
// is unaffected, which is why only localStorage-reading suites ever noticed.
// Reinstall a real Storage so DOM suites behave identically on every Node build.
if (typeof globalThis.localStorage === "undefined") {
  const donor = new Window({ url: "http://localhost/" });
  Object.defineProperty(globalThis, "localStorage", {
    value: donor.localStorage as unknown as Storage,
    configurable: true,
    writable: true,
  });
}

// No suite may touch the network. happy-dom resolves relative URLs against the
// environment's default document origin, which is the same port the dev server
// listens on, so a fetch that slips past a module mock does not merely fail --
// it can reach a live backend holding real data. The call sites that leak are
// fire-and-forget (`void store.load()`), so the rejection is swallowed and the
// only trace is stderr noise that fails nothing. Record every escape and fail
// the test responsible for it.
//
// Suites that legitimately drive fetch stub it with vi.stubGlobal, which
// snapshots this guard and restores it on vi.unstubAllGlobals.
const escapedRequests: string[] = [];

globalThis.fetch = ((input: RequestInfo | URL): Promise<Response> => {
  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.href
        : input.url;
  escapedRequests.push(url);
  return Promise.reject(new TypeError(`Unmocked fetch: ${url}`));
}) as typeof fetch;

afterEach(() => {
  if (escapedRequests.length === 0) return;
  const seen = [...new Set(escapedRequests)];
  escapedRequests.length = 0;
  throw new Error(
    `Unmocked network call escaped this test: ${seen.join(", ")}. Stub the module that issues it.`,
  );
});
