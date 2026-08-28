// Fonts are served by the Go server at /fonts/.
// @font-face CSS is built at runtime with absolute URLs so the srcdoc iframe
// can load them across the null origin boundary.

import {
  embeddedMetrics,
  userFontUrl,
  type FontMetrics,
  type FontRoleMap,
  type UserFontFamily,
} from "~/api/client";
import { userFamilyCSSName, userFamilyDir } from "~/lib/fontRegistry";

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

// Every family is normalized against Literata, the default reading font. Its
// own numbers cancel out — size-adjust resolves to 100% and each override
// equals what the face already reports — so the default rendering is unchanged
// and only the other families move.
const REFERENCE_FACE = FONT_FILES.literata;

function percent(ratio: number): string {
  return `${Number((ratio * 100).toFixed(3))}%`;
}

/**
 * The @font-face descriptors that make a family look the same size as the
 * reference, or "" when either set of metrics is unknown — in which case the
 * family is left at its natural size rather than sized from a guess.
 *
 * `size-adjust` scales the glyphs until the x-heights match, and x-height (not
 * font-size) is what the eye reads as size; that is why two families need
 * different font-size values to look equal without this.
 *
 * The three overrides then replace the face's own vertical metrics, so
 * `line-height: normal` produces the same line box for every family. That is
 * what makes the Auto leading setting portable: without them, size-adjust
 * fixes the glyphs but each family still picks its own natural leading.
 *
 * Each override is divided by the adjustment because the browser resolves it
 * against the already-adjusted size; without the divide, scaling glyphs down
 * would drag the line box down with them.
 */
function normalizeToReference(
  face: FontMetrics | undefined,
  reference: FontMetrics | undefined,
): string {
  if (!face?.xHeight || !reference?.xHeight) return "";
  const adjust = reference.xHeight / face.xHeight;
  return [
    `  size-adjust: ${percent(adjust)};`,
    `  ascent-override: ${percent(reference.ascent / adjust)};`,
    `  descent-override: ${percent(reference.descent / adjust)};`,
    `  line-gap-override: ${percent(reference.lineGap / adjust)};`,
  ].join("\n");
}

let cachedReaderFontFaces: string | null = null;
// The metrics object this cache was built from. /fonts returns a fresh object
// per response, so an identity check is enough to rebuild once measurements
// arrive — otherwise an un-normalized first build would stick for the session.
let cachedMetricsSource: Record<string, FontMetrics> | null = null;

export function buildReaderFontFaces(): string {
  const metrics = embeddedMetrics();
  if (cachedReaderFontFaces && cachedMetricsSource === metrics) {
    return cachedReaderFontFaces;
  }

  const face = (
    family: string,
    file: string,
    weight: string,
    style = "normal",
    normalize = "",
  ) => `@font-face {
  font-family: '${family}';
  src: url('${fontUrl(file)}') format('woff2');
  font-weight: ${weight};
  font-style: ${style};
  font-display: block;${normalize ? `\n${normalize}` : ""}
}`;

  const { literata, literataItalic, atkinson, atkinsonItalic } = FONT_FILES;
  const reference = metrics[REFERENCE_FACE];
  // Both faces of a family take the UPRIGHT face's adjustment, so the
  // designer's intended roman-to-italic relationship survives normalization.
  const literataFit = normalizeToReference(metrics[literata], reference);
  const atkinsonFit = normalizeToReference(metrics[atkinson], reference);

  cachedReaderFontFaces = [
    face("Literata", literata, "100 900", "normal", literataFit),
    face("Literata", literataItalic, "100 900", "italic", literataFit),
    face(
      "Atkinson Hyperlegible Next",
      atkinson,
      "100 900",
      "normal",
      atkinsonFit,
    ),
    face(
      "Atkinson Hyperlegible Next",
      atkinsonItalic,
      "100 900",
      "italic",
      atkinsonFit,
    ),
  ].join("\n");
  cachedMetricsSource = metrics;

  return cachedReaderFontFaces;
}

// Builds @font-face rules for the user families that have at least one role
// assigned. The CSS family name matches userFamilyCSSValue() (an escaped CSS
// string for the directory segment). `dir` is used only in the served URL.
//
// Variable families emit ONE 100–900 face per axis: the upright file covers
// regular + bold (and the weight slider) and the italic file covers italic +
// bold-italic, so the browser never synthesizes a faux bold. Static families
// emit one fixed-weight face per assigned role (regular→400, bold→700,
// italic→400/italic, boldItalic→700/italic); unassigned roles are left to
// browser synthesis (bold-italic synthesizes from the italic face).
function buildUserFontFaces(
  families: UserFontFamily[],
  roles: Record<string, FontRoleMap> | undefined,
): string {
  if (!families.length) return "";

  const face = (
    family: string,
    url: string,
    weight: string,
    style: string,
    normalize: string,
  ) =>
    `@font-face {
  font-family: ${family};
  src: url('${url}') format('${formatHint(url)}');
  font-weight: ${weight};
  font-style: ${style};
  font-display: block;${normalize ? `\n${normalize}` : ""}
}`;

  const reference = embeddedMetrics()[REFERENCE_FACE];
  const out: string[] = [];
  for (const fam of families) {
    const dir = userFamilyDir(fam.id);
    const family = userFamilyCSSName(fam.id);
    // fam.metrics describes the family's regular face, and every role takes
    // that same adjustment so roman, italic and bold keep their relationship
    // to one another. Absent metrics leave the family at its natural size.
    const fit = normalizeToReference(fam.metrics, reference);
    const map = roles?.[fam.id] ?? {};
    // Fall back to the backend's detected roles when the user hasn't chosen.
    //
    // Nullish coalescing rather than a truthiness check is deliberate, and it
    // relies on an unset role being ABSENT rather than "": SettingsPanel
    // deletes a cleared key, and Go's fontRoleEntry tags every role omitempty
    // so a partial entry never serializes its empty siblings. Were either
    // side to emit "", these lines would suppress the face instead of
    // falling back to the detected file. Both halves are pinned by tests.
    const regular = map.regular ?? fam.detected.regular;
    const italic = map.italic ?? fam.detected.italic;
    const bold = map.bold ?? fam.detected.bold;
    const boldItalic = map.boldItalic ?? fam.detected.boldItalic;

    if (fam.variable) {
      // The upright file carries the whole weight axis, so a single 100–900
      // face yields real regular AND bold (and the weight slider works);
      // likewise the italic file for italic + bold-italic. No separate 700
      // face means the browser never synthesizes a faux bold. A split-out
      // static bold/bold-italic file (detected or chosen) is deliberately
      // ignored on this path — SettingsPanel hides those role rows for
      // variable families, so no new pick can be made.
      if (regular)
        out.push(
          face(family, userFontUrl(dir, regular), "100 900", "normal", fit),
        );
      if (italic)
        out.push(
          face(family, userFontUrl(dir, italic), "100 900", "italic", fit),
        );
      continue;
    }

    if (regular)
      out.push(face(family, userFontUrl(dir, regular), "400", "normal", fit));
    if (bold)
      out.push(face(family, userFontUrl(dir, bold), "700", "normal", fit));
    if (italic)
      out.push(face(family, userFontUrl(dir, italic), "400", "italic", fit));
    if (boldItalic)
      out.push(
        face(family, userFontUrl(dir, boldItalic), "700", "italic", fit),
      );
  }
  return out.join("\n");
}

function formatHint(url: string): string {
  const queryStart = url.indexOf("?");
  const path = queryStart === -1 ? url : url.slice(0, queryStart);
  const lower = path.toLowerCase();
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
