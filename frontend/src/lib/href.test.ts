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

  it("resolves a shorter link through the forward suffix tier", () => {
    expect(
      resolveHref(
        "part-b/next.xhtml",
        spine("OPS/part-a/next.xhtml", "OPS/part-b/next.xhtml"),
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("resolves a longer link through the reverse suffix tier", () => {
    expect(
      resolveHref(
        "OPS/part-b/next.xhtml",
        spine("part-a/next.xhtml", "part-b/next.xhtml"),
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("rejects an ambiguous suffix match instead of guessing", () => {
    expect(
      resolveHref(
        "text/ch.xhtml",
        spine("OPS/a/text/ch.xhtml", "OPS/b/text/ch.xhtml"),
      ),
    ).toBeNull();
  });

  it("normalizes backslash-separated hrefs", () => {
    expect(
      resolveHref(
        "OPS\\text\\ch2.xhtml",
        spine("OPS/text/ch1.xhtml", "OPS/text/ch2.xhtml"),
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("resolves a root-relative href from the archive root", () => {
    expect(
      resolveHref(
        "/OPS/part-b/next.xhtml",
        spine("OPS/part-a/ch.xhtml", "OPS/part-b/next.xhtml"),
        0,
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("ignores a source chapter past the end of the spine", () => {
    expect(
      resolveHref(
        "OPS/text/ch2.xhtml",
        spine("OPS/text/ch1.xhtml", "OPS/text/ch2.xhtml"),
        99,
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("falls back to a unique basename when no path tier matches", () => {
    expect(
      resolveHref(
        "wrong/dir/ch2.xhtml",
        spine("OPS/text/ch1.xhtml", "OPS/text/ch2.xhtml"),
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("drops current-directory segments before matching", () => {
    expect(
      resolveHref(
        "./next.xhtml",
        spine(
          "OPS/part-a/ch.xhtml",
          "OPS/part-a/next.xhtml",
          "OPS/part-b/next.xhtml",
        ),
        0,
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
  });

  it("pops parent segments when the file name alone is ambiguous", () => {
    expect(
      resolveHref(
        "../notes/end.xhtml",
        spine(
          "OPS/text/ch.xhtml",
          "OPS/notes/end.xhtml",
          "OPS/text/notes/end.xhtml",
        ),
        0,
      ),
    ).toEqual({ chapterIndex: 1, fragment: "" });
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

  it("fills forward from the first matched chapter without backfilling", () => {
    const entry = toc("OPS/ch2.xhtml", "Second");
    expect(
      buildTocChapterEntries(
        [entry],
        spine("OPS/ch1.xhtml", "OPS/ch2.xhtml", "OPS/ch3.xhtml"),
      ),
    ).toEqual([null, entry, entry]);
  });

  it("walks nested TOC children", () => {
    const child = toc("OPS/ch3.xhtml", "Sub");
    const parent: TocEntry = {
      ...toc("OPS/ch1.xhtml", "Top"),
      children: [child],
    };
    expect(
      buildTocChapterEntries(
        [parent],
        spine("OPS/ch1.xhtml", "OPS/ch2.xhtml", "OPS/ch3.xhtml"),
      ),
    ).toEqual([parent, parent, child]);
  });

  it("keeps the first of two entries sharing an href", () => {
    const first = toc("OPS/ch1.xhtml", "Book title");
    const second = toc("OPS/ch1.xhtml", "Chapter one");
    const result = buildTocChapterEntries(
      [first, second],
      spine("OPS/ch1.xhtml", "OPS/ch2.xhtml"),
    );
    expect(result[0]).toBe(first);
  });
});
