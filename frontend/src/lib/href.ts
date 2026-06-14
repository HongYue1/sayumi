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
