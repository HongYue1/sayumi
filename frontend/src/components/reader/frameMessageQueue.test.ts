import { describe, it, expect } from "vitest";
import { createFrameMessageQueue } from "~/components/reader/frameMessageQueue";
import type { ParentToFrameMessage } from "~/lib/frameMessages";

// The queue only ever inspects `type`, so `load` and `applySettings` cast
// minimal shapes through rather than build a fourteen-field LoadMessage or a
// whole IframeSettings. The two fixtures that CAN be built completely are
// checked with `satisfies`, so a required field added to either wire member
// fails this file instead of leaving it testing a shape no producer can send.
const load = (seq: number) =>
  ({ type: "load", seq }) as unknown as ParentToFrameMessage;
const applySettings = () =>
  ({ type: "apply-settings" }) as unknown as ParentToFrameMessage;
const setFontFaces = (css: string) =>
  ({
    type: "set-font-faces",
    fontFaces: css,
  }) satisfies ParentToFrameMessage;
const scrollTo = (percent: number, seq = 0) =>
  ({ type: "scroll-to", seq, percent }) satisfies ParentToFrameMessage;

describe("createFrameMessageQueue", () => {
  it("coalesces a newer message into the earlier one's slot (latest wins, order kept)", () => {
    const q = createFrameMessageQueue();
    q.enqueue(setFontFaces("a"));
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(load(2)); // supersedes load(1) at load(1)'s position

    const drained = q.drain();
    expect(drained.map((m) => m.type)).toEqual([
      "set-font-faces",
      "load",
      "apply-settings",
    ]);
    // The surviving load is the newest one.
    const surviving = drained.find((m) => m.type === "load");
    expect((surviving as { seq: number }).seq).toBe(2);
  });

  it("coalesces font-face state in place, keeping the newest CSS", () => {
    const q = createFrameMessageQueue();
    q.enqueue(setFontFaces("old"));
    q.enqueue(scrollTo(0.1));
    q.enqueue(setFontFaces("new"));

    const drained = q.drain();
    expect(drained.map((m) => m.type)).toEqual(["set-font-faces", "scroll-to"]);
    expect(
      (drained[0] as { type: "set-font-faces"; fontFaces: string }).fontFaces,
    ).toBe("new");
  });

  it("keeps a superseding load ahead of the settings queued behind it", () => {
    const q = createFrameMessageQueue();
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(load(2));

    // Order is the contract here, not just membership: a settings message that
    // reaches the frame before its load is applied against CSS the frame has
    // not prepared yet, and that empty result is memoised.
    const drained = q.drain();
    expect(drained.map((m) => m.type)).toEqual(["load", "apply-settings"]);
    expect((drained[0] as { seq: number }).seq).toBe(2);
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

  it("preserves all coalesced startup state when capping stray interactions", () => {
    const q = createFrameMessageQueue(4);
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(setFontFaces("fonts"));
    q.enqueue(scrollTo(0.1));
    q.enqueue(scrollTo(0.2)); // drop scrollTo(0.1), not startup state

    expect(q.size).toBe(4);
    expect(q.drain().map((m) => m.type)).toEqual([
      "load",
      "apply-settings",
      "set-font-faces",
      "scroll-to",
    ]);
  });

  it("drops the oldest coalesced message when the cap is below their count", () => {
    // The shift() fallback is only reachable when maxQueued is below the number
    // of coalesce types; no production caller passes one, so this is its only
    // exercise.
    const q = createFrameMessageQueue(2);
    q.enqueue(load(1));
    q.enqueue(applySettings());
    q.enqueue(setFontFaces("x"));

    expect(q.size).toBe(2);
    expect(q.drain().map((m) => m.type)).toEqual([
      "apply-settings",
      "set-font-faces",
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
