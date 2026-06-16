// Fonts are served by the Go server at /fonts/.
// @font-face CSS is built at runtime with absolute URLs so the srcdoc iframe
// can load them across the null origin boundary.

import {
  userFontUrl,
  type FontRoleMap,
  type UserFontFamily,
} from "~/api/client";
import { userFamilyDir } from "~/lib/fontRegistry.svelte";

// Only the two embedded reading fonts. The rest of the catalogue ships as
// drop-in ./Fonts/ families and is rendered via buildUserFontFaces below.
const FONT_FILES = {
  literata: "Literata-VariableFont.woff2",
  literataItalic: "Literata-Italic-VariableFont.woff2",
  atkinson: "AtkinsonHyperlegibleNext-VariableFont.woff2",
  atkinsonItalic: "AtkinsonHyperlegibleNext-Italic-VariableFont.woff2",
} as const;

function fontUrl(filename: string): string {
  return `${window.location.origin}/fonts/${filename}`;
}

let cachedReaderFontFaces: string | null = null;

export function buildReaderFontFaces(): string {
  if (cachedReaderFontFaces) return cachedReaderFontFaces;

  const face = (
    family: string,
    file: string,
    weight: string,
    style = "normal",
  ) => `@font-face {
  font-family: '${family}';
  src: url('${fontUrl(file)}') format('woff2');
  font-weight: ${weight};
  font-style: ${style};
  font-display: block;
}`;

  const { literata, literataItalic, atkinson, atkinsonItalic } = FONT_FILES;

  cachedReaderFontFaces = [
    face("Literata", literata, "100 900"),
    face("Literata", literataItalic, "100 900", "italic"),
    face("Atkinson Hyperlegible Next", atkinson, "100 900"),
    face("Atkinson Hyperlegible Next", atkinsonItalic, "100 900", "italic"),
  ].join("\n");

  return cachedReaderFontFaces;
}

// Builds @font-face rules for the user families that have at least one role
// assigned. The CSS family name matches userFamilyCSSValue() (the directory
// segment of the id). Roles map to weight/style: regular→400/normal,
// bold→700/normal, italic→400/italic. Only assigned roles emit a face; the
// browser synthesizes missing styles. `dir` is the on-disk folder used in the
// served URL.
function buildUserFontFaces(
  families: UserFontFamily[],
  roles: Record<string, FontRoleMap> | undefined,
): string {
  if (!families.length) return "";

  const face = (family: string, url: string, weight: string, style: string) =>
    `@font-face {
  font-family: '${family}';
  src: url('${url}') format('${formatHint(url)}');
  font-weight: ${weight};
  font-style: ${style};
  font-display: block;
}`;

  const out: string[] = [];
  for (const fam of families) {
    const dir = userFamilyDir(fam.id);
    const map = roles?.[fam.id] ?? {};
    // Fall back to the backend's detected roles when the user hasn't chosen.
    const regular = map.regular ?? fam.detected.regular;
    const italic = map.italic ?? fam.detected.italic;
    const bold = map.bold ?? fam.detected.bold;

    if (regular)
      out.push(face(dir, userFontUrl(dir, regular), "400", "normal"));
    if (bold) out.push(face(dir, userFontUrl(dir, bold), "700", "normal"));
    if (italic) out.push(face(dir, userFontUrl(dir, italic), "400", "italic"));
  }
  return out.join("\n");
}

function formatHint(url: string): string {
  const lower = url.toLowerCase();
  if (lower.endsWith(".woff2")) return "woff2";
  if (lower.endsWith(".woff")) return "woff";
  if (lower.endsWith(".otf")) return "opentype";
  if (lower.endsWith(".ttf")) return "truetype";
  return "woff2";
}

/**
 * Full @font-face CSS for the reader: the static embedded set plus any user
 * families. Recomputed whenever the user font registry or role mapping change,
 * then re-sent to the iframe.
 */
export function buildAllFontFaces(
  userFamilies: UserFontFamily[],
  roles: Record<string, FontRoleMap> | undefined,
): string {
  const userFaces = buildUserFontFaces(userFamilies, roles);
  return userFaces
    ? `${buildReaderFontFaces()}\n${userFaces}`
    : buildReaderFontFaces();
}
