import { afterAll, afterEach } from "vitest";

// No suite may touch the network. happy-dom resolves relative URLs against the
// environment's default document origin, which is the same port the dev server
// listens on, so a fetch that slips past a module mock does not merely fail --
// it can reach a live backend holding real data. The call sites that leak are
// fire-and-forget (`void store.load()`), so the rejection is swallowed and the
// only trace is stderr noise that fails nothing. Record every escape and fail
// the test or hook responsible for it.
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

function assertNoEscapedRequests(): void {
  if (escapedRequests.length === 0) return;
  const seen = [...new Set(escapedRequests)];
  escapedRequests.length = 0;
  throw new Error(
    `Unmocked network call escaped this test or hook: ${seen.join(", ")}. Stub the module that issues it.`,
  );
}

afterEach(assertNoEscapedRequests);
// Setup hooks run last during stack-ordered teardown, after suite-owned hooks.
// A swallowed fetch in afterAll otherwise bypasses the per-test assertion.
afterAll(assertNoEscapedRequests);
