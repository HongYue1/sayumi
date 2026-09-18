// One BCP-47-ish normalizer for the book language tag, shared by the frame
// shell (frameHtmlTemplate's initial lang) and the frame bundle (the load
// handler's per-chapter lang) so the two can never disagree about what is
// legal. Shared rather than duplicated: two hand-synced copies already
// drifted once in spirit (strip-only), and only the shared form fails closed
// together.
//
// Underscores fold to hyphens first: book metadata commonly tags "zh_Hant"
// -style variants, which BCP 47 spells with a hyphen, while stripping alone
// would fuse them into a subtag ("zhHant") that matches nothing. Edge hyphens
// come off so a pathological "_en" degrades to "en" rather than "-en".
export function normalizeLangTag(raw: string | null | undefined): string {
  if (!raw) return "";
  return raw
    .replace(/_/g, "-")
    .replace(/[^a-zA-Z0-9-]/g, "")
    .replace(/^-+|-+$/g, "")
    .slice(0, 35);
}
