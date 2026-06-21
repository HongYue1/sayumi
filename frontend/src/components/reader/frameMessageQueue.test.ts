import { describe, it, expect } from "vitest";
import { createFrameMessageQueue } from "~/components/reader/frameMessageQueue";
import type { ParentToFrameMessage } from "~/lib/frameMessages";

// The queue only ever inspects `type`; other fields are irrelevant to it, so the
// factories cast minimal shapes through.
const load = (seq: number) =>
  ({ type: "load", seq }) as unknown as ParentToFrameMessage;
const applySettings = () =>
  ({ type: "apply-settings" }) as unknown as ParentToFrameMessage;
const setFontFaces = (css: string) =>
  ({
    type: "set-font-faces",
    fontFaces: css,
  }) as unknown as ParentToFrameMessage;
const scrollTo = (percent: number) =>
  ({ type: "scroll-to", percent }) as unknown as ParentToFrameMessage;

describe("createFrameMessageQueue", () => {
  it("coalesces a newer message over an earlier one of the same type (latest wins)", () => {
    const q = createFrameMessageQueue();
    q.enqueue(setFontFaces("a"));
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(load(2)); // supersedes load(1)

    const drained = q.drain();
    expect(drained.map((m) => m.type)).toEqual([
      "set-font-faces",
      "apply-settings",
      "load",
    ]);
    // The surviving load is the newest one.
    const surviving = drained.find((m) => m.type === "load");
    expect((surviving as { seq: number }).seq).toBe(2);
  });

  it("keeps every non-coalesced message (no dedupe)", () => {
    const q = createFrameMessageQueue();
    q.enqueue(scrollTo(0.1));
    q.enqueue(scrollTo(0.2));
    expect(q.size).toBe(2);
    expect(q.drain().map((m) => (m as { percent: number }).percent)).toEqual([
      0.1, 0.2,
    ]);
  });

  it("caps the queue, dropping the oldest non-coalesced message past the limit", () => {
    const q = createFrameMessageQueue(3);
    q.enqueue(scrollTo(0));
    q.enqueue(scrollTo(1));
    q.enqueue(scrollTo(2));
    q.enqueue(scrollTo(3)); // length 4 > 3 -> drop oldest non-coalesced (0)
    expect(q.size).toBe(3);
    expect(q.drain().map((m) => (m as { percent: number }).percent)).toEqual([
      1, 2, 3,
    ]);
  });

  it("preserves coalesced startup state when capping stray interactions", () => {
    const q = createFrameMessageQueue(3);
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(scrollTo(0.1));
    q.enqueue(scrollTo(0.2)); // drop scrollTo(0.1), not load/apply-settings

    expect(q.size).toBe(3);
    expect(q.drain().map((m) => m.type)).toEqual([
      "load",
      "apply-settings",
      "scroll-to",
    ]);
  });

  it("defaults the cap to 64", () => {
    const q = createFrameMessageQueue();
    for (let i = 0; i < 70; i++) q.enqueue(scrollTo(i));
    expect(q.size).toBe(64);
  });

  it("drain empties the queue", () => {
    const q = createFrameMessageQueue();
    q.enqueue(scrollTo(0.5));
    expect(q.drain()).toHaveLength(1);
    expect(q.size).toBe(0);
    expect(q.drain()).toEqual([]);
  });

  it("clear empties the queue", () => {
    const q = createFrameMessageQueue();
    q.enqueue(load(1));
    q.enqueue(scrollTo(0.5));
    q.clear();
    expect(q.size).toBe(0);
  });
});
