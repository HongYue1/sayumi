// Pure CSS-text transforms for the reader frame's chapter-CSS pipeline.
//
// Extracted verbatim from frame.ts with no behavioural change: every function
// maps its inputs to outputs and touches no frame runtime state. The only
// retained state is the single-entry strip-colors memo, which is local to this
// module (it was a module-level `let` in frame.ts and is read/written only by
// stripColorsFromCSS). esbuild inlines this module into the frame IIFE, so it
// runs inside the same srcdoc sandbox as before; CSP/nonce/sanitization are
// unaffected (this code only rewrites CSS strings, never the DOM).

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
  "outline-color",
  "text-decoration-color",
  "column-rule-color",
  "fill",
  "stroke",
]);

let _stripCSSCache: { input: string; output: string } | null = null;

export function splitBookCSS(cssText: string): {
  fontCSS: string;
  layoutCSS: string;
} {
  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(cssText);
    let fontCSS = "";
    let layoutCSS = "";

    for (const rule of sheet.cssRules) {
      if (rule instanceof CSSStyleRule) {
        const ff = rule.style.getPropertyValue("font-family");
        if (ff) {
          fontCSS += `${rule.selectorText} { font-family: ${ff}; }\n`;
          const clone = rule.style.cssText
            .replace(/font-family\s*:[^;]+;?/gi, "")
            .trim();
          if (clone) layoutCSS += `${rule.selectorText} { ${clone} }\n`;
        } else {
          layoutCSS += rule.cssText + "\n";
        }
      } else {
        layoutCSS += rule.cssText + "\n";
      }
    }

    return { fontCSS, layoutCSS };
  } catch {
    return { fontCSS: "", layoutCSS: cssText };
  }
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
  if (rule instanceof CSSStyleRule) {
    const style = rule.style;
    const kept: string[] = [];
    for (let i = 0; i < style.length; i++) {
      const prop = style.item(i);
      if (!COLOR_PROPS_SET.has(prop)) {
        const val = style.getPropertyValue(prop);
        const pri = style.getPropertyPriority(prop);
        kept.push(`${prop}: ${val}${pri ? " !important" : ""}`);
      }
    }
    return kept.length > 0
      ? `${rule.selectorText} { ${kept.join("; ")}; }\n`
      : "";
  }
  if (rule instanceof CSSMediaRule) {
    let inner = "";
    for (const child of rule.cssRules) inner += processRuleStripColors(child);
    return inner ? `@media ${rule.conditionText} {\n${inner}}\n` : "";
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
