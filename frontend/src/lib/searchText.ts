const ASCII_LOWER = "abcdefghijklmnopqrstuvwxyz";

/** One-code-point lowercase mapping, matching Go's unicode.ToLower contract. */
export function foldSearchCodePoint(char: string): string {
  const code = char.charCodeAt(0);
  if (code < 0x80) {
    return code >= 0x41 && code <= 0x5a
      ? ASCII_LOWER.charAt(code - 0x41)
      : char;
  }
  return Array.from(char.toLowerCase())[0] ?? char;
}

export function toCodePoints(value: string): string[] {
  return Array.from(value);
}

export function codePointLength(value: string): number {
  return toCodePoints(value).length;
}

export function foldSearchText(value: string): string[] {
  return Array.from(value, foldSearchCodePoint);
}

export interface CodePointMatch {
  before: string;
  match: string;
  after: string;
}

/**
 * The scan itself, over an already-folded haystack. Kept separate so a caller
 * that also needs the source's own code points can segment the string once and
 * fold that array, rather than walking the string again.
 */
function findRangeInFolded(
  haystack: readonly string[],
  needle: readonly string[],
): { start: number; end: number } | null {
  if (needle.length === 0 || needle.length > haystack.length) return null;

  const limit = haystack.length - needle.length;
  for (let start = 0; start <= limit; start += 1) {
    let matched = true;
    for (let i = 0; i < needle.length; i += 1) {
      if (haystack[start + i] === needle[i]) continue;
      matched = false;
      break;
    }
    if (matched) return { start, end: start + needle.length };
  }
  return null;
}

export function findFoldedCodePointRange(
  source: string,
  needle: readonly string[],
): { start: number; end: number } | null {
  return findRangeInFolded(foldSearchText(source), needle);
}

/** Splits on code-point boundaries; no returned part can hold half a pair. */
export function splitFoldedCodePointMatch(
  source: string,
  foldedQuery: readonly string[],
): CodePointMatch | null {
  // One segmentation pass: the folded haystack is derived from the very array
  // the slices below index into. Going through findFoldedCodePointRange would
  // walk the string once to fold it and once more to split it, and a
  // highlighted TOC row pays that on every keystroke.
  const chars = toCodePoints(source);
  const range = findRangeInFolded(
    chars.map((char) => foldSearchCodePoint(char)),
    foldedQuery,
  );
  if (!range) return null;
  return {
    before: chars.slice(0, range.start).join(""),
    match: chars.slice(range.start, range.end).join(""),
    after: chars.slice(range.end).join(""),
  };
}
