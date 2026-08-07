import { describe, expect, it } from "vitest";
import {
  SPECIMEN_BOOK_ID,
  specimenBookDetail,
  specimenChapter,
} from "~/lib/specimen";

describe("typography specimen", () => {
  it("uses a sentinel id that cannot collide with a real book id", () => {
    expect(SPECIMEN_BOOK_ID).toBe("__specimen__");
  });

  it("describes exactly one chapter, consistently across spine and toc", () => {
    const detail = specimenBookDetail();
    expect(detail.id).toBe(SPECIMEN_BOOK_ID);
    expect(detail.chapterCount).toBe(1);
    expect(detail.spine).toHaveLength(1);
    expect(detail.toc).toHaveLength(1);
    expect(detail.toc[0].href).toBe(detail.spine[0].href);
  });

  it("claims no cover, so the reader never requests one", () => {
    expect(specimenBookDetail().hasCover).toBe(false);
  });

  it("returns its single chapter at index 0", () => {
    const chapter = specimenChapter();
    expect(chapter.chapterIndex).toBe(0);
    expect(chapter.html.length).toBeGreaterThan(0);
  });

  it("ships no author CSS, so reader typography is what gets judged", () => {
    // The specimen exists to preview the user's own settings. Any book CSS
    // here would style the preview and defeat its purpose.
    expect(specimenChapter().css).toBe("");
  });

  it("carries no font-face CSS of its own", () => {
    // Faces travel over the separate set-font-faces channel, never inside
    // chapter data: ChapterFrame sends the embedded set on ready and Read.tsx
    // pushes the full set from loadChapter.
    expect(specimenChapter().fontFaceCSS).toBe("");
  });

  it("exercises every block element the reader styles", () => {
    const html = specimenChapter().html;
    for (const tag of [
      "<h1>",
      "<h6>",
      "<blockquote>",
      "<ul>",
      "<ol>",
      "<pre>",
      "<code>",
      "<table>",
      "<figure>",
      "<hr />",
    ]) {
      expect(html).toContain(tag);
    }
  });

  it("embeds its only image as a data URI so it renders offline", () => {
    const html = specimenChapter().html;
    expect(html).toContain('src="data:image/svg+xml,');
    expect(html).not.toContain('src="http');
  });

  it("renders horizontally, left to right", () => {
    const chapter = specimenChapter();
    expect(chapter.direction).toBe("ltr");
    expect(chapter.writingMode).toBe("horizontal-tb");
  });
});
