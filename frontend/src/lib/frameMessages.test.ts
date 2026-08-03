import { describe, expect, it } from "vitest";
import type {
  FrameToParentMessage,
  ParentToFrameMessage,
  ReadingDirection,
  WritingMode,
} from "~/lib/frameMessages";

function acceptParentMessage(_message: ParentToFrameMessage): void {}
function acceptFrameMessage(_message: FrameToParentMessage): void {}

// These compile-time assertions keep command payloads mandatory. The branch is
// unreachable at runtime; `tsc --noEmit` (bun run check) verifies that each
// omission is rejected.
if (false) {
  // @ts-expect-error font CSS is required
  acceptParentMessage({ type: "set-font-faces" });
  // @ts-expect-error page and seq are required
  acceptParentMessage({ type: "go-to-page" });
  // @ts-expect-error positional commands carry the chapter seq
  acceptParentMessage({ type: "go-to-page", page: 3 });
  // @ts-expect-error fragment id is required
  acceptParentMessage({ type: "scroll-to-fragment" });
  // @ts-expect-error positional commands carry the chapter seq
  acceptParentMessage({ type: "scroll-to-fragment", id: "ch1" });
  // @ts-expect-error CFI is required
  acceptParentMessage({ type: "scroll-to-cfi" });
  // @ts-expect-error positional commands carry the chapter seq
  acceptParentMessage({ type: "next-page" });
  // @ts-expect-error positional commands carry the chapter seq
  acceptParentMessage({ type: "scroll-to-end" });
  // @ts-expect-error complete search coordinates, query, and seq are required
  acceptParentMessage({ type: "highlight-search" });
  // @ts-expect-error chapter-owned link events require a seq
  acceptFrameMessage({ type: "link-clicked", href: "chapter.xhtml" });
  // @ts-expect-error direction is a closed enum mirroring the Go side
  const badDirection: ReadingDirection = "auto";
  // @ts-expect-error writing mode is a closed enum mirroring the Go side
  const badWritingMode: WritingMode = "sideways-rl";
  void badDirection;
  void badWritingMode;
}

describe("frame message protocol", () => {
  it("carries the chapter sequence on link events", () => {
    const message: FrameToParentMessage = {
      type: "link-clicked",
      seq: 7,
      href: "chapter.xhtml",
    };

    expect(message.seq).toBe(7);
  });

  it("stamps positional commands with the chapter sequence", () => {
    const message: ParentToFrameMessage = {
      type: "go-to-page",
      seq: 4,
      page: 1,
    };

    expect(message).toEqual({ type: "go-to-page", seq: 4, page: 1 });
  });
});
