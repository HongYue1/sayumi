// Pure CSS-text transforms for the reader frame's chapter-CSS pipeline.
//
// Extracted from frame.ts: every function maps its inputs to outputs, touches
// no frame runtime state, and keeps no state of its own. esbuild inlines this
// module into the frame IIFE, so it runs inside the same srcdoc sandbox as
// before; CSP/nonce/sanitization are unaffected (this code only rewrites CSS
// strings, never the DOM).
//
// @import: in-EPUB import targets are spliced in by the backend before a
// chapter ever reaches the frame (internal/epub/chapter.go, inlineCSSImports).
// They have to be, because a constructed CSSStyleSheet drops @import outright:
// replaceSync ignores it, so both passes below would silently lose an imported
// sheet in every preserve mode, and neither pass can color-strip or font-split
// text it never parsed. Imports still present here are remote or unresolvable
// and are meant to disappear.

const FONT_FAMILY_RE =
  /font-family\s*:\s*['"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['"]?/gi;
// Applied only to the reader's own generated @font-face text, never to book
// CSS: [^}] would stop early at a "}" inside a string or comment, which
// host-authored input never contains.
const FONT_FACE_BLOCK_RE = /@font-face\s*\{[^}]*\}/gi;
const FONT_FACE_FAMILY_RE =
  /font-family\s*:\s*['"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['"]?/i;

const COLOR_PROPS_SET = new Set([
  "color",
  "background",
  "background-color",
  "background-image",
  "background-blend-mode",
  "border-image-source",
  "border-color",
  "border-top-color",
  "border-right-color",
  "border-bottom-color",
  "border-left-color",
  "border-block-color",
  "border-block-start-color",
  "border-block-end-color",
  "border-inline-color",
  "border-inline-start-color",
  "border-inline-end-color",
  "outline-color",
  "text-decoration-color",
  "text-emphasis-color",
  "column-rule-color",
  "caret-color",
  "accent-color",
  "fill",
  "stroke",
  "stop-color",
  "flood-color",
  "lighting-color",
  "-webkit-text-fill-color",
  "-webkit-text-stroke-color",
  "text-shadow",
  "box-shadow",
]);

// These shorthands may carry a color alongside layout/decorative values. Keep
// their non-color longhands so disabling book colors does not also discard a
// border's width/style, an underline, or text-stroke geometry.
//
// Chromium never hands us a shorthand: CSSOM stores longhands, so item() only
// ever yields expanded properties (probe — a rule written with border, font,
// background, outline and text-decoration enumerates 52 longhands and zero
// shorthands). This map is for engines that do keep them; happy-dom stores
// text-decoration un-expanded today. It is also load-bearing for
// hasColorDeclaration below, which must treat a surviving shorthand as
// color-bearing instead of passing the rule through untouched.
const COLOR_SHORTHAND_LONGHANDS = new Map<string, readonly string[]>([
  [
    "border",
    [
      "border-top-width",
      "border-top-style",
      "border-right-width",
      "border-right-style",
      "border-bottom-width",
      "border-bottom-style",
      "border-left-width",
      "border-left-style",
    ],
  ],
  ["border-top", ["border-top-width", "border-top-style"]],
  ["border-right", ["border-right-width", "border-right-style"]],
  ["border-bottom", ["border-bottom-width", "border-bottom-style"]],
  ["border-left", ["border-left-width", "border-left-style"]],
  [
    "border-block",
    [
      "border-block-start-width",
      "border-block-start-style",
      "border-block-end-width",
      "border-block-end-style",
    ],
  ],
  [
    "border-inline",
    [
      "border-inline-start-width",
      "border-inline-start-style",
      "border-inline-end-width",
      "border-inline-end-style",
    ],
  ],
  [
    "border-block-start",
    ["border-block-start-width", "border-block-start-style"],
  ],
  ["border-block-end", ["border-block-end-width", "border-block-end-style"]],
  [
    "border-inline-start",
    ["border-inline-start-width", "border-inline-start-style"],
  ],
  ["border-inline-end", ["border-inline-end-width", "border-inline-end-style"]],
  ["outline", ["outline-width", "outline-style"]],
  ["column-rule", ["column-rule-width", "column-rule-style"]],
  [
    "text-decoration",
    [
      "text-decoration-line",
      "text-decoration-style",
      "text-decoration-thickness",
    ],
  ],
  [
    "border-image",
    [
      "border-image-slice",
      "border-image-width",
      "border-image-outset",
      "border-image-repeat",
    ],
  ],
  ["-webkit-text-stroke", ["-webkit-text-stroke-width"]],
]);

const FONT_SHORTHAND_LAYOUT_LONGHANDS = [
  "font-style",
  "font-variant",
  "font-weight",
  "font-stretch",
  "font-size",
  "line-height",
] as const;

type Declaration = { value: string; priority: string };
type Declarations = Map<string, Declaration>;
type SplitCSS = { fontCSS: string; layoutCSS: string };

function getRuleStyle(rule: CSSRule): CSSStyleDeclaration | null {
  const style = (rule as CSSRule & { style?: CSSStyleDeclaration }).style;
  return style && typeof style.getPropertyValue === "function" ? style : null;
}

function getRuleChildren(rule: CSSRule): CSSRuleList | null {
  const rules = (rule as CSSRule & { cssRules?: CSSRuleList }).cssRules;
  return rules && rules.length > 0 ? rules : null;
}

// Duck-typed on purpose: this is the routing test for "has a declaration block
// we can split", and @page satisfies it too, since CSSPageRule exposes both
// selectorText and style. Splitting a page rule is harmless — but note that its
// selectorText is ":first" for "@page :first", with the at-keyword omitted, so
// wrapRule must not take the selectorText shortcut for anything but a real
// CSSStyleRule.
function isStyleRule(rule: CSSRule): rule is CSSStyleRule {
  return (
    typeof (rule as CSSStyleRule).selectorText === "string" &&
    getRuleStyle(rule) !== null
  );
}

function findRuleBlockStart(cssText: string): number {
  let quote = "";
  let escaped = false;
  let inComment = false;

  for (let i = 0; i < cssText.length; i++) {
    const char = cssText[i];
    const next = cssText[i + 1];

    if (inComment) {
      if (char === "*" && next === "/") {
        inComment = false;
        i++;
      }
      continue;
    }
    if (quote) {
      if (escaped) {
        escaped = false;
      } else if (char === "\\") {
        escaped = true;
      } else if (char === quote) {
        quote = "";
      }
      continue;
    }
    if (char === "/" && next === "*") {
      inComment = true;
      i++;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === "{") return i;
  }

  return -1;
}

// Recovering the prelude from cssText costs a full re-serialization of the
// rule, and for a grouping rule of its entire subtree. A real style rule
// already has it in selectorText: a Chromium probe over an 82 KB sheet walked
// cssText in 7.2 ms and selectorText in 0.1 ms. The instanceof guard is not
// paranoia — CSSPageRule also exposes selectorText, but as ":first" for
// "@page :first", so using it there would emit an invalid rule the engine then
// drops. Everything else (including CSSLayerBlockRule and CSSContainerRule,
// which report type === 0 in current Chromium) goes through the scan.
function wrapRule(rule: CSSRule, body: string): string {
  const closeBrace = "}";
  if (typeof CSSStyleRule === "function" && rule instanceof CSSStyleRule) {
    return `${rule.selectorText} {\n${body}${closeBrace}\n`;
  }
  const blockStart = findRuleBlockStart(rule.cssText);
  if (blockStart < 0) return rule.cssText + "\n";
  const prelude = rule.cssText.slice(0, blockStart).trimEnd();
  return `${prelude} {\n${body}${closeBrace}\n`;
}

function addDeclaration(
  declarations: Declarations,
  style: CSSStyleDeclaration,
  property: string,
  fallbackPriority = "",
): void {
  const value = style.getPropertyValue(property);
  if (!value) return;
  declarations.set(property, {
    value,
    priority: style.getPropertyPriority(property) || fallbackPriority,
  });
}

function serializeDeclarations(declarations: Declarations): string {
  let output = "";
  for (const [property, declaration] of declarations) {
    output += `${property}: ${declaration.value}${declaration.priority ? " !important" : ""};\n`;
  }
  return output;
}

function splitStyleRule(rule: CSSStyleRule): SplitCSS {
  const style = rule.style;
  const children = getRuleChildren(rule);
  const family = style.getPropertyValue("font-family");

  // Nothing to lift out, so the layout arm is the rule exactly as the engine
  // serialized it: no declaration walk, and shorthands stay compact instead of
  // being exploded into longhands. The font shorthand is checked as well, for
  // an engine that stores it un-expanded.
  if (
    !family &&
    !children &&
    style.length > 0 &&
    !style.getPropertyValue("font")
  ) {
    return { fontCSS: "", layoutCSS: rule.cssText + "\n" };
  }

  const fontDeclarations: Declarations = new Map();
  const layoutDeclarations: Declarations = new Map();

  if (family) {
    fontDeclarations.set("font-family", {
      value: family,
      priority:
        style.getPropertyPriority("font-family") ||
        style.getPropertyPriority("font"),
    });
  }

  for (let i = 0; i < style.length; i++) {
    const property = style.item(i);
    if (property === "font-family") continue;
    if (property === "font") {
      // Only reachable in an engine that stores the font shorthand
      // un-expanded; Chromium and happy-dom both expand it (probe). Kept so
      // such an engine keeps font-size and line-height on the layout arm
      // instead of losing them along with the family.
      const priority = style.getPropertyPriority(property);
      for (const longhand of FONT_SHORTHAND_LAYOUT_LONGHANDS) {
        addDeclaration(layoutDeclarations, style, longhand, priority);
      }
      continue;
    }
    addDeclaration(layoutDeclarations, style, property);
  }

  let fontBody = serializeDeclarations(fontDeclarations);
  let layoutBody = serializeDeclarations(layoutDeclarations);
  if (children) {
    for (const child of children) {
      const split = splitRule(child);
      fontBody += split.fontCSS;
      layoutBody += split.layoutCSS;
    }
  }

  return {
    fontCSS: fontBody ? wrapRule(rule, fontBody) : "",
    layoutCSS: layoutBody ? wrapRule(rule, layoutBody) : "",
  };
}

function splitRule(rule: CSSRule): SplitCSS {
  if (isStyleRule(rule)) return splitStyleRule(rule);

  const children = getRuleChildren(rule);
  if (children) {
    let fontBody = "";
    let layoutBody = "";
    for (const child of children) {
      const split = splitRule(child);
      fontBody += split.fontCSS;
      layoutBody += split.layoutCSS;
    }
    return {
      fontCSS: fontBody ? wrapRule(rule, fontBody) : "",
      layoutCSS: layoutBody ? wrapRule(rule, layoutBody) : "",
    };
  }

  return { fontCSS: "", layoutCSS: rule.cssText + "\n" };
}

export function splitBookCSS(cssText: string): SplitCSS {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(cssText);
    let fontCSS = "";
    let layoutCSS = "";

    for (const rule of sheet.cssRules) {
      const split = splitRule(rule);
      fontCSS += split.fontCSS;
      layoutCSS += split.layoutCSS;
    }

    return { fontCSS, layoutCSS };
  } catch {
    return { fontCSS: "", layoutCSS: cssText };
  }
}

function declarationsWithoutColors(style: CSSStyleDeclaration): string {
  const kept: Declarations = new Map();

  for (let i = 0; i < style.length; i++) {
    const property = style.item(i);
    if (COLOR_PROPS_SET.has(property)) continue;

    const longhands = COLOR_SHORTHAND_LONGHANDS.get(property);
    if (longhands) {
      const priority = style.getPropertyPriority(property);
      for (const longhand of longhands) {
        addDeclaration(kept, style, longhand, priority);
      }
      continue;
    }

    addDeclaration(kept, style, property);
  }

  return serializeDeclarations(kept);
}

// True when the rule declares something the strip pass would remove. A
// shorthand an engine kept un-expanded counts: it may carry a color, so the
// rule cannot be passed through untouched.
function hasColorDeclaration(style: CSSStyleDeclaration): boolean {
  for (let i = 0; i < style.length; i++) {
    const property = style.item(i);
    if (COLOR_PROPS_SET.has(property)) return true;
    if (COLOR_SHORTHAND_LONGHANDS.has(property)) return true;
  }
  return false;
}

// Deliberately unmemoized: the frame strips two different inputs back to back
// (the layout arm, then the raw sheet), so a single-entry memo is always
// evicted before it can be read, while pinning two chapter-sized strings for
// the life of the frame. Repeat chapters are served by the prepared-CSS LRU in
// frame.ts, which never reaches this far.
export function stripColorsFromCSS(cssText: string): string {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(cssText);
    let result = "";
    for (const rule of sheet.cssRules) result += processRuleStripColors(rule);
    return result;
  } catch {
    return cssText;
  }
}

function processRuleStripColors(rule: CSSRule): string {
  const style = getRuleStyle(rule);
  const children = getRuleChildren(rule);
  if (style || children) {
    // Nothing to strip: hand back the engine's own serialization instead of
    // rebuilding the rule from longhands. Rebuilding every rule is what
    // inflates the injected stylesheet — Chromium probe over a realistic
    // 500-rule chapter sheet: 82 KB in, 230 KB out rebuilding everything
    // versus 139 KB with this pass-through, 21.3 ms versus 11.8 ms, and
    // identical declarations after a re-parse.
    if (style && !children && style.length > 0 && !hasColorDeclaration(style)) {
      return rule.cssText + "\n";
    }
    let body = style ? declarationsWithoutColors(style) : "";
    if (children) {
      for (const child of children) body += processRuleStripColors(child);
    }
    return body ? wrapRule(rule, body) : "";
  }
  return rule.cssText + "\n";
}

// The two sides of the font comparison come from different sources, so
// normalize them the same way: CSS family names are case-insensitive and their
// internal whitespace is not significant, which makes "Book  Sans" and
// "book sans" one font.
function normalizeFamilyName(raw: string): string {
  return raw.trim().toLowerCase().replace(/\s+/g, " ");
}

export function extractBookFontFamilies(fontFaceCSS: string): Set<string> {
  const names = new Set<string>();
  // Fresh regex per call: matchAll seeds its internal clone from this regex's
  // lastIndex, so sharing one /g instance would silently skip matches the day
  // anything calls exec() on it.
  const familyRe = new RegExp(FONT_FAMILY_RE.source, FONT_FAMILY_RE.flags);
  for (const match of fontFaceCSS.matchAll(familyRe)) {
    names.add(normalizeFamilyName(match[1]));
  }
  return names;
}

export function filterReaderFontFaces(
  readerFF: string,
  excludeNames: Set<string>,
): string {
  if (excludeNames.size === 0) return readerFF;
  return readerFF.replace(FONT_FACE_BLOCK_RE, (block) => {
    const match = block.match(FONT_FACE_FAMILY_RE);
    if (match && excludeNames.has(normalizeFamilyName(match[1]))) return "";
    return block;
  });
}
