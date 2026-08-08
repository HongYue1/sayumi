// End-to-end client → gate → session coverage. Unlike session.test.ts, this
// file keeps the real API client so the server's 401 payload has to cross the
// same request parser and generation-stamped dispatch used in production.
import { afterEach, expect, it, vi } from "vitest";

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

it("turns a real protected-request 401 into a signed-out session", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const href =
        input instanceof Request
          ? input.url
          : input instanceof URL
            ? input.href
            : input;
      const path = new URL(href, window.location.origin).pathname;
      if (path === "/api/auth/login") {
        return Promise.resolve(jsonResponse({ profile: "Alice" }));
      }
      if (path === "/api/settings") {
        return Promise.resolve(
          jsonResponse(
            { error: "not logged in", code: "unauthenticated" },
            401,
          ),
        );
      }
      throw new Error(`Unexpected request: ${path}`);
    }),
  );
  const { session } = await import("~/lib/session");
  const { getSettings } = await import("~/api/client");
  const { flush } = await import("solid-js");

  await session.login("Alice", "", false);
  flush();
  expect(session.status).toBe("authenticated");
  expect(session.profile).toBe("Alice");

  await expect(getSettings()).rejects.toMatchObject({
    status: 401,
    code: "unauthenticated",
  });
  flush();

  expect(session.status).toBe("signed-out");
  expect(session.profile).toBeNull();
});
