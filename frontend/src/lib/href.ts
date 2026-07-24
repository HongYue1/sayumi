import type { SpineEntry, TocEntry } from "~/api/client";

interface ParsedHref {
  path: string;
  fragment: string;
}

function parseHref(href: string): ParsedHref {
  const hashIdx = href.indexOf("#");
  const beforeFragment = hashIdx >= 0 ? href.slice(0, hashIdx) : href;
  const fragment = hashIdx >= 0 ? href.slice(hashIdx + 1) : "";
  const queryIdx = beforeFragment.indexOf("?");
  return {
    path: queryIdx >= 0 ? beforeFragment.slice(0, queryIdx) : beforeFragment,
    fragment,
  };
}

function normalizeArchivePath(path: string): string {
  const parts: string[] = [];
  for (const part of path.replaceAll("\\", "/").split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") {
      parts.pop();
      continue;
    }
    parts.push(part);
  }
  return parts.join("/");
}

function pathBasename(path: string): string {
  const i = path.lastIndexOf("/");
  return i >= 0 ? path.slice(i + 1) : path;
}

function resolveRelativePath(path: string, sourcePath: string): string {
  if (path.startsWith("/")) return normalizeArchivePath(path);
  const slash = sourcePath.lastIndexOf("/");
  const directory = slash >= 0 ? sourcePath.slice(0, slash + 1) : "";
  return normalizeArchivePath(directory + path);
}

function uniqueMatch(
  spinePaths: string[],
  predicate: (path: string) => boolean,
): number | null {
  let match = -1;
  for (let i = 0; i < spinePaths.length; i++) {
    if (!predicate(spinePaths[i])) continue;
    if (match >= 0) return null;
    match = i;
  }
  return match >= 0 ? match : null;
}

function matchSpinePath(path: string, spinePaths: string[]): number | null {
  const exact = uniqueMatch(spinePaths, (spinePath) => spinePath === path);
  if (exact !== null) return exact;

  const suffix = uniqueMatch(
    spinePaths,
    (spinePath) =>
      spinePath.endsWith("/" + path) || path.endsWith("/" + spinePath),
  );
  if (suffix !== null) return suffix;

  const basename = pathBasename(path);
  return uniqueMatch(
    spinePaths,
    (spinePath) => pathBasename(spinePath) === basename,
  );
}

/**
 * Resolves an in-book href to a spine chapter. Raw iframe links may pass the
 * source chapter index so relative paths are resolved from that chapter's
 * archive directory; TOC hrefs remain canonical archive paths.
 */
export function resolveHref(
  href: string,
  spine: SpineEntry[],
  sourceChapter?: number,
): { chapterIndex: number; fragment: string } | null {
  const parsed = parseHref(href);
  const spinePaths = spine.map((entry) =>
    normalizeArchivePath(parseHref(entry.href).path),
  );
  let path = normalizeArchivePath(parsed.path);

  if (
    sourceChapter !== undefined &&
    Number.isSafeInteger(sourceChapter) &&
    sourceChapter >= 0 &&
    sourceChapter < spinePaths.length
  ) {
    path = resolveRelativePath(parsed.path, spinePaths[sourceChapter]);
  }

  const chapterIndex = matchSpinePath(path, spinePaths);
  return chapterIndex === null
    ? null
    : { chapterIndex, fragment: parsed.fragment };
}

/**
 * Precomputes, for each spine chapter index, the TOC entry that should be
 * highlighted while that chapter is open, then fills forward so chapters
 * without their own TOC line inherit the nearest preceding heading.
 */
export function buildTocChapterEntries(
  toc: TocEntry[],
  spine: SpineEntry[],
): Array<TocEntry | null> {
  const spinePaths = spine.map((entry) =>
    normalizeArchivePath(parseHref(entry.href).path),
  );
  const result: Array<TocEntry | null> = new Array(spine.length).fill(null);

  const walk = (entries: TocEntry[]): void => {
    for (const entry of entries) {
      const path = normalizeArchivePath(parseHref(entry.href).path);
      const idx = matchSpinePath(path, spinePaths);
      if (idx !== null && result[idx] == null) result[idx] = entry;
      if (entry.children?.length) walk(entry.children);
    }
  };
  walk(toc);

  for (let i = 1; i < result.length; i++) {
    if (result[i] == null) result[i] = result[i - 1];
  }
  return result;
}
