import type { SpineEntry, TocEntry } from "~/api/client";

interface ParsedHref {
  path: string;
  fragment: string;
}

export function decodeHrefComponent(hrefPart: string): string {
  try {
    return decodeURIComponent(hrefPart);
  } catch {
    // A literal or malformed percent escape is still a valid authored file or
    // fragment spelling. Identity fallback keeps navigation fail-soft.
    return hrefPart;
  }
}

function parseHref(href: string): ParsedHref {
  const hashIdx = href.indexOf("#");
  const beforeFragment = hashIdx >= 0 ? href.slice(0, hashIdx) : href;
  const fragment = hashIdx >= 0 ? href.slice(hashIdx + 1) : "";
  const queryIdx = beforeFragment.indexOf("?");
  return {
    path: queryIdx >= 0 ? beforeFragment.slice(0, queryIdx) : beforeFragment,
    fragment: decodeHrefComponent(fragment),
  };
}

/**
 * Canonicalizes an archive path one segment at a time. Splitting before decode
 * keeps an encoded slash inside its authored segment; re-encoding gives raw
 * and encoded spellings one key without treating '+' as form-space or decoding
 * a doubly encoded value twice. A malformed escape falls back to its literal
 * spelling and is then encoded normally.
 */
function normalizeArchivePath(path: string): string {
  const parts: string[] = [];
  for (const rawPart of path.replaceAll("\\", "/").split("/")) {
    const part = decodeHrefComponent(rawPart);
    if (!part || part === ".") continue;
    if (part === "..") {
      parts.pop();
      continue;
    }
    try {
      parts.push(encodeURIComponent(part));
    } catch {
      // Lone UTF-16 surrogates make encodeURIComponent throw. Keep that one
      // malformed segment literal rather than crashing the whole reader.
      parts.push(rawPart);
    }
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

/**
 * Normalized spine paths plus O(1) lookup maps for the exact and basename
 * match tiers. A map value of null marks an AMBIGUOUS key (two spine entries
 * share it) — the same "unique or nothing" outcome uniqueMatch produced by
 * scanning. Building this once turns TOC resolution from O(toc × spine) into
 * O(toc + spine) for the common exact-match case; the (rare) suffix tier stays
 * a linear scan.
 */
interface SpineIndex {
  paths: string[];
  byPath: Map<string, number | null>;
  byBasename: Map<string, number | null>;
}

function buildSpineIndex(spine: SpineEntry[]): SpineIndex {
  const paths = spine.map((entry) =>
    normalizeArchivePath(parseHref(entry.href).path),
  );
  const byPath = new Map<string, number | null>();
  const byBasename = new Map<string, number | null>();
  for (let i = 0; i < paths.length; i++) {
    const path = paths[i];
    byPath.set(path, byPath.has(path) ? null : i);
    const base = pathBasename(path);
    byBasename.set(base, byBasename.has(base) ? null : i);
  }
  return { paths, byPath, byBasename };
}

function matchSpinePath(path: string, index: SpineIndex): number | null {
  // Exact tier. An ambiguous exact key (null) falls through to the suffix
  // tier rather than failing.
  const exact = index.byPath.get(path);
  if (exact !== undefined && exact !== null) return exact;

  const suffix = uniqueMatch(
    index.paths,
    (spinePath) =>
      spinePath.endsWith("/" + path) || path.endsWith("/" + spinePath),
  );
  if (suffix !== null) return suffix;

  const basename = index.byBasename.get(pathBasename(path));
  return basename === undefined ? null : basename;
}

/**
 * Resolves an in-book href to a spine chapter. Raw iframe links may pass the
 * source chapter index so relative paths are resolved from that chapter's
 * archive directory; TOC hrefs remain canonical archive paths.
 *
 * Unlike buildTocChapterEntries, this rebuilds the spine index on every call.
 * That is right at click pace, but do not wire it into a per-render or
 * per-item path without hoisting the index out of the call first.
 */
export function resolveHref(
  href: string,
  spine: SpineEntry[],
  sourceChapter?: number,
): { chapterIndex: number; fragment: string } | null {
  const parsed = parseHref(href);
  const index = buildSpineIndex(spine);
  let path = normalizeArchivePath(parsed.path);

  if (
    sourceChapter !== undefined &&
    Number.isSafeInteger(sourceChapter) &&
    sourceChapter >= 0 &&
    sourceChapter < index.paths.length
  ) {
    path = resolveRelativePath(parsed.path, index.paths[sourceChapter]);
  }

  const chapterIndex = matchSpinePath(path, index);
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
  const index = buildSpineIndex(spine);
  const result: Array<TocEntry | null> = new Array(spine.length).fill(null);

  const walk = (entries: TocEntry[]): void => {
    for (const entry of entries) {
      const path = normalizeArchivePath(parseHref(entry.href).path);
      const idx = matchSpinePath(path, index);
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
