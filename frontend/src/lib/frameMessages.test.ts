import { describe, expect, it } from "vitest";
import type { ChapterFrameAPI } from "~/components/reader/frame-types";
import type {
  FrameToParentMessage,
  IframeSettings,
  LoadMessage,
  ParentToFrameMessage,
  ReadingDirection,
  WritingMode,
} from "~/lib/frameMessages";

function acceptParentMessage(_message: ParentToFrameMessage): void {}
function acceptFrameMessage(_message: FrameToParentMessage): void {}

const settings = {} as IframeSettings;
const atomicLoad = {
  type: "load",
  seq: 4,
  origin: "https://reader.example",
  chapterIndex: 2,
  settings,
  html: "<p>chapter</p>",
  css: "",
  fontFaceCSS: "",
  direction: "ltr",
  writingMode: "horizontal-tb",
  resourceBase: null,
  scrollTo: "top",
  fragment: null,
  hasPrev: true,
  hasNext: true,
  restorePercent: null,
  restoreCfi: null,
} satisfies LoadMessage;

type DeadWireVariants = Extract<
  ParentToFrameMessage["type"],
  "go-to-page" | "go-to-last-page"
>;
type DeadApiMethods = Extract<
  keyof ChapterFrameAPI,
  "goToPage" | "goToLastPage"
>;
const deadWireRemoved: DeadWireVariants extends never ? true : never = true;
const deadApiRemoved: DeadApiMethods extends never ? true : never = true;

// These compile-time assertions keep command payloads mandatory. The branch is
// unreachable at runtime; `tsc --noEmit` verifies that each omission is rejected.
if (false) {
  const { settings: _settings, ...loadWithoutSettings } = atomicLoad;
  // @ts-expect-error settings are required inside the atomic chapter load
  acceptParentMessage(loadWithoutSettings);
  // @ts-expect-error font CSS is required
  acceptParentMessage({ type: "set-font-faces" });
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
  void _settings;
  void badDirection;
  void badWritingMode;
}

describe("frame message protocol", () => {
  it("carries settings inside the atomic chapter load", () => {
    expect(atomicLoad.settings).toBe(settings);
  });

  it("carries the chapter sequence on link events", () => {
    const message: FrameToParentMessage = {
      type: "link-clicked",
      seq: 7,
      href: "chapter.xhtml",
    };
    expect(message.seq).toBe(7);
  });

  it("has no dead page-jump wire variants or controller methods", () => {
    expect(deadWireRemoved).toBe(true);
    expect(deadApiRemoved).toBe(true);
  });
});
