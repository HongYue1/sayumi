import { describe, expect, it } from "vitest";
import type {
  FrameToParentMessage,
  ParentToFrameMessage,
} from "~/lib/frameMessages";

function acceptParentMessage(_message: ParentToFrameMessage): void {}
function acceptFrameMessage(_message: FrameToParentMessage): void {}

// These compile-time assertions keep command payloads mandatory. The branch is
// unreachable at runtime; svelte-check verifies that each omission is rejected.
if (false) {
  // @ts-expect-error font CSS is required
  acceptParentMessage({ type: "set-font-faces" });
  // @ts-expect-error page is required
  acceptParentMessage({ type: "go-to-page" });
  // @ts-expect-error fragment id is required
  acceptParentMessage({ type: "scroll-to-fragment" });
  // @ts-expect-error CFI is required
  acceptParentMessage({ type: "scroll-to-cfi" });
  // @ts-expect-error complete search coordinates, query, and seq are required
  acceptParentMessage({ type: "highlight-search" });
  // @ts-expect-error chapter-owned link events require a seq
  acceptFrameMessage({ type: "link-clicked", href: "chapter.xhtml" });
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
});
