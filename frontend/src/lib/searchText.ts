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

export function findFoldedCodePointRange(
  source: string,
  needle: readonly string[],
): { start: number; end: number } | null {
  const haystack = foldSearchText(source);
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

/** Splits on code-point boundaries; no returned part can hold half a pair. */
export function splitFoldedCodePointMatch(
  source: string,
  foldedQuery: readonly string[],
): CodePointMatch | null {
  const range = findFoldedCodePointRange(source, foldedQuery);
  if (!range) return null;
  const chars = toCodePoints(source);
  return {
    before: chars.slice(0, range.start).join(""),
    match: chars.slice(range.start, range.end).join(""),
    after: chars.slice(range.end).join(""),
  };
}
