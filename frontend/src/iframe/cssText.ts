// Pure CSS-text transforms for the reader frame's chapter-CSS pipeline.
//
// Extracted from frame.ts: every function maps its inputs to outputs and
// touches no frame runtime state. The only retained state is the single-entry
// strip-colors memo, which is local to this module. esbuild inlines this module
// into the frame IIFE, so it runs inside the same srcdoc sandbox as before;
// CSP/nonce/sanitization are unaffected (this code only rewrites CSS strings,
// never the DOM).

const FONT_FAMILY_RE =
  /font-family\s*:\s*['"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['"]?/gi;
const FONT_FACE_BLOCK_RE = /@font-face\s*\{[^}]*\}/gi;
const FONT_FACE_FAMILY_RE =
  /font-family\s*:\s*['"]?([^'"\s;,}{]+(?:\s+[^'"\s;,}{]+)*)['"]?/i;

const COLOR_PROPS_SET = new Set([
  "color",
  "background",
  "background-color",
  "background-image",
  "background-blend-mode",
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

let _stripCSSCache: { input: string; output: string } | null = null;

function getRuleStyle(rule: CSSRule): CSSStyleDeclaration | null {
  const style = (rule as CSSRule & { style?: CSSStyleDeclaration }).style;
  return style && typeof style.getPropertyValue === "function" ? style : null;
}

function getRuleChildren(rule: CSSRule): CSSRuleList | null {
  const rules = (rule as CSSRule & { cssRules?: CSSRuleList }).cssRules;
  return rules && rules.length > 0 ? rules : null;
}

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

function wrapRule(rule: CSSRule, body: string): string {
  const blockStart = findRuleBlockStart(rule.cssText);
  if (blockStart < 0) return rule.cssText + "\n";
  const prelude = rule.cssText.slice(0, blockStart).trimEnd();
  const closeBrace = "}";
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
  const fontDeclarations: Declarations = new Map();
  const layoutDeclarations: Declarations = new Map();
  const family = style.getPropertyValue("font-family");

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
  const children = getRuleChildren(rule);
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

export function stripColorsFromCSS(cssText: string): string {
  if (_stripCSSCache?.input === cssText) return _stripCSSCache.output;
  let output: string;
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(cssText);
    let result = "";
    for (const rule of sheet.cssRules) result += processRuleStripColors(rule);
    output = result;
  } catch {
    output = cssText;
  }
  _stripCSSCache = { input: cssText, output };
  return output;
}

function processRuleStripColors(rule: CSSRule): string {
  const style = getRuleStyle(rule);
  const children = getRuleChildren(rule);
  if (style || children) {
    let body = style ? declarationsWithoutColors(style) : "";
    if (children) {
      for (const child of children) body += processRuleStripColors(child);
    }
    return body ? wrapRule(rule, body) : "";
  }
  return rule.cssText + "\n";
}

export function extractBookFontFamilies(fontFaceCSS: string): Set<string> {
  const names = new Set<string>();
  for (const match of fontFaceCSS.matchAll(FONT_FAMILY_RE)) {
    names.add(match[1].trim().toLowerCase());
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
    if (match && excludeNames.has(match[1].trim().toLowerCase())) return "";
    return block;
  });
}
