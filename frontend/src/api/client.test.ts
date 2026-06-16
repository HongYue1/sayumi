import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { requestWithRetry, ApiError } from "~/api/client";

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// timeoutMs: 0 disables the per-attempt AbortSignal.timeout so the only timers
// in play are the retry backoff sleeps, which fake timers drive deterministically.
const noTimeout = (attempts: number) => ({ attempts, timeoutMs: 0 });

describe("requestWithRetry", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("retries an idempotent GET on a 500 and then succeeds", async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ error: "boom" }, 500))
      .mockResolvedValueOnce(jsonResponse({ ok: true }, 200));
    const p = requestWithRetry<{ ok: boolean }>(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    );
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("retries an idempotent PUT on a network error and then succeeds", async () => {
    fetchMock
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(jsonResponse({ ok: true }, 200));
    const p = requestWithRetry<{ ok: boolean }>(
      "PUT",
      "/x",
      { a: 1 },
      noTimeout(3),
    );
    await vi.runAllTimersAsync();
    await expect(p).resolves.toEqual({ ok: true });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does NOT retry a POST on a 500 (non-idempotent write)", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "boom" }, 500));
    const err = await requestWithRetry(
      "POST",
      "/x",
      { a: 1 },
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does NOT retry a PATCH on a 500 (non-idempotent write)", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "boom" }, 500));
    const err = await requestWithRetry(
      "PATCH",
      "/x",
      { a: 1 },
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does NOT retry a 4xx even for an idempotent GET", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "nope" }, 404));
    const err = await requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 404 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("gives up after the configured attempts on a persistent 500", async () => {
    fetchMock.mockResolvedValue(jsonResponse({ error: "down" }, 500));
    const settled = requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(2),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    const err = await settled;
    expect(err).toBeInstanceOf(ApiError);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("rethrows an AbortError without retrying", async () => {
    fetchMock.mockRejectedValue(new DOMException("aborted", "AbortError"));
    const err = await requestWithRetry(
      "GET",
      "/x",
      undefined,
      noTimeout(3),
    ).catch((e) => e);
    await vi.runAllTimersAsync();
    expect(err).toBeInstanceOf(DOMException);
    expect(err).toMatchObject({ name: "AbortError" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
