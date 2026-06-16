import type { SpineEntry, TocEntry } from "~/api/client";

function pathBasename(p: string): string {
  const i = p.lastIndexOf("/");
  return i >= 0 ? p.slice(i + 1) : p;
}

function hrefMatches(a: string, b: string): boolean {
  if (a === b) return true;
  return (
    pathBasename(a) === pathBasename(b) ||
    a.endsWith("/" + b) ||
    b.endsWith("/" + a)
  );
}

/** Resolves an in-book href (possibly with #fragment) to a spine chapter. */
export function resolveHref(
  href: string,
  spine: SpineEntry[],
): { chapterIndex: number; fragment: string } | null {
  const hashIdx = href.indexOf("#");
  const hrefBase = hashIdx >= 0 ? href.slice(0, hashIdx) : href;
  const fragment = hashIdx >= 0 ? href.slice(hashIdx + 1) : "";

  // Pass 1: exact or path-suffix match (the reliable signals). Run to
  // completion before falling back so a stronger match later in the spine
  // always wins over a weaker basename-only match earlier in it.
  for (let i = 0; i < spine.length; i++) {
    const spineBase = spine[i].href.split("#")[0];
    if (
      spineBase === hrefBase ||
      spineBase.endsWith("/" + hrefBase) ||
      hrefBase.endsWith("/" + spineBase)
    ) {
      return { chapterIndex: i, fragment };
    }
  }
  // Pass 2: basename equality, only when nothing stronger matched. Handles
  // links that differ only by directory prefix, but can collide when two
  // spine files share a basename, so it stays the last resort.
  for (let i = 0; i < spine.length; i++) {
    const spineBase = spine[i].href.split("#")[0];
    if (pathBasename(spineBase) === pathBasename(hrefBase)) {
      return { chapterIndex: i, fragment };
    }
  }
  return null;
}

/**
 * Precomputes, for each spine chapter index, the TOC entry that should be
 * highlighted while that chapter is open. Runs in O(toc + spine): it builds
 * exact + basename lookups from the spine once, resolves each TOC entry through
 * them (every pass-1 suffix match in resolveHref also shares a basename, so the
 * basename map is a faithful fallback), keeps the first TOC entry per chapter
 * (document order = the chapter-level heading), then fills forward so a chapter
 * with no TOC line of its own inherits the nearest preceding heading. Build
 * this once per book and index it by chapter instead of calling resolveHref for
 * every TOC entry on every open.
 */
export function buildTocChapterEntries(
  toc: TocEntry[],
  spine: SpineEntry[],
): Array<TocEntry | null> {
  const byBase = new Map<string, number>();
  const byBasename = new Map<string, number>();
  for (let i = 0; i < spine.length; i++) {
    const base = spine[i].href.split("#")[0];
    if (!byBase.has(base)) byBase.set(base, i);
    const bn = pathBasename(base);
    if (!byBasename.has(bn)) byBasename.set(bn, i);
  }

  const result: Array<TocEntry | null> = new Array(spine.length).fill(null);
  const walk = (entries: TocEntry[]): void => {
    for (const entry of entries) {
      const hrefBase = entry.href.split("#")[0];
      const idx =
        byBase.get(hrefBase) ?? byBasename.get(pathBasename(hrefBase));
      if (idx !== undefined && result[idx] == null) result[idx] = entry;
      if (entry.children?.length) walk(entry.children);
    }
  };
  walk(toc);

  for (let i = 1; i < result.length; i++) {
    if (result[i] == null) result[i] = result[i - 1];
  }
  return result;
}

/** Finds the TOC title for the chapter at the given spine index, if any. */
export function findTocLabel(
  toc: TocEntry[],
  spine: SpineEntry[],
  chapterIndex: number,
): string {
  if (chapterIndex < 0 || chapterIndex >= spine.length) return "";
  const spineHref = spine[chapterIndex].href.split("#")[0];

  function search(entries: TocEntry[]): string | null {
    let best: string | null = null;
    for (const entry of entries) {
      const entryBase = entry.href.split("#")[0];
      if (hrefMatches(entryBase, spineHref)) best = entry.title;
      if (entry.children) {
        const child = search(entry.children);
        if (child) best = child;
      }
    }
    return best;
  }

  return search(toc) || "";
}
