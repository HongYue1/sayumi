import { describe, expect, it } from "vitest";
import type { SpineEntry, TocEntry } from "~/api/client";
import { buildTocChapterEntries, resolveHref } from "~/lib/href";

function spine(...hrefs: string[]): SpineEntry[] {
  return hrefs.map((href, index) => ({
    href,
    id: String(index),
    mediaType: "application/xhtml+xml",
    linear: true,
  }));
}

function toc(href: string, title = href): TocEntry {
  return { href, title, depth: 0 };
}

describe("resolveHref", () => {
  it("matches query-bearing chapter paths and preserves fragments", () => {
    const result = resolveHref(
      "OPS/text/chapter-2.xhtml?view=reader#section-3",
      spine("OPS/text/chapter-1.xhtml", "OPS/text/chapter-2.xhtml"),
    );

    expect(result).toEqual({ chapterIndex: 1, fragment: "section-3" });
  });

  it("resolves iframe links from the source chapter directory", () => {
    const result = resolveHref(
      "chapter-2.xhtml#target",
      spine("OPS/text/chapter-1.xhtml", "OPS/text/chapter-2.xhtml"),
      0,
    );

    expect(result).toEqual({ chapterIndex: 1, fragment: "target" });
  });

  it("normalizes parent-directory segments", () => {
    const result = resolveHref(
      "../notes/endnotes.xhtml#note-1",
      spine("OPS/text/chapter.xhtml", "OPS/notes/endnotes.xhtml"),
      0,
    );

    expect(result).toEqual({ chapterIndex: 1, fragment: "note-1" });
  });

  it("uses the source directory to disambiguate duplicate basenames", () => {
    const entries = spine(
      "OPS/part-a/chapter.xhtml",
      "OPS/part-a/next.xhtml",
      "OPS/part-b/next.xhtml",
    );

    expect(resolveHref("next.xhtml", entries, 0)).toEqual({
      chapterIndex: 1,
      fragment: "",
    });
    expect(resolveHref("next.xhtml", entries)).toBeNull();
  });
});

describe("buildTocChapterEntries", () => {
  it("matches query-bearing TOC entries", () => {
    const entry = toc("OPS/text/chapter-2.xhtml?view=toc#start", "Second");
    expect(
      buildTocChapterEntries(
        [entry],
        spine("OPS/text/chapter-1.xhtml", "OPS/text/chapter-2.xhtml"),
      ),
    ).toEqual([null, entry]);
  });

  it("rejects ambiguous basename fallback", () => {
    expect(
      buildTocChapterEntries(
        [toc("chapter.xhtml")],
        spine("OPS/part-a/chapter.xhtml", "OPS/part-b/chapter.xhtml"),
      ),
    ).toEqual([null, null]);
  });
});
