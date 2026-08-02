import { bench, describe } from "vitest";
import {
  extractBookFontFamilies,
  filterReaderFontFaces,
  splitBookCSS,
  stripColorsFromCSS,
} from "./cssText";

// Chapter-sized book stylesheet: roughly a third of the rules carry a color,
// the rest are pure layout, plus a block of media queries. That mix is what
// decides how much of the sheet each pass has to rebuild, so it is the fixture
// that matters — an all-color sheet flatters the rebuild path.
function buildSheet(rules: number): string {
  const parts: string[] = [];
  for (let i = 0; i < rules; i += 1) {
    if (i % 10 < 3) {
      parts.push(
        `.c${i} { color: #123456; background-color: #ffffff; margin: 4px 5px; font-family: Georgia, serif; padding: 2px 3px; line-height: 1.4; }`,
      );
    } else {
      parts.push(
        `.c${i} { margin: 4px 5px; padding: 2px 3px; text-indent: 1em; font-size: 1.05em; line-height: 1.4; text-align: justify; }`,
      );
    }
  }
  for (let m = 0; m < 40; m += 1) {
    const inner: string[] = [];
    for (let k = 0; k < 10; k += 1) {
      inner.push(`.m${m}-${k} { margin: 1px; padding: 1px; }`);
    }
    parts.push(
      `@media screen and (min-width: ${300 + m}px) { ${inner.join(" ")} }`,
    );
  }
  return parts.join("\n");
}

// The frame strips two different inputs back to back (the layout arm and the
// raw sheet), so alternating inputs is what the hot path actually looks like.
const sheetA = buildSheet(500);
const sheetB = buildSheet(480);
let tick = 0;

// How much of the fixture the host DOM actually parses rides in the bench name:
// a sudden rule-count drop means the fixture stopped exercising the pipeline,
// not that the code got faster.
function parsedRuleCount(cssText: string): number {
  try {
    const parsed = new CSSStyleSheet();
    parsed.replaceSync(cssText);
    return parsed.cssRules.length;
  } catch {
    return -1;
  }
}
const ruleCount = parsedRuleCount(sheetA);

const bookFontFaces = Array.from(
  { length: 12 },
  (_, i) =>
    `@font-face { font-family: "Book Face ${i}"; src: url(/api/f${i}.woff2) format("woff2"); }`,
).join("\n");

const readerFontFaces = Array.from(
  { length: 10 },
  (_, i) =>
    `@font-face { font-family: "Reader Face ${i}"; src: url(/fonts/r${i}.woff2) format("woff2"); }`,
)
  .concat(
    '@font-face { font-family: "Book Face 3"; src: url(/fonts/x.woff2); }',
  )
  .join("\n");

const bookFamilies = extractBookFontFamilies(bookFontFaces);

describe(`chapter css (${ruleCount} rules)`, () => {
  bench("stripColorsFromCSS", () => {
    tick += 1;
    stripColorsFromCSS(tick % 2 === 0 ? sheetA : sheetB);
  });

  bench("splitBookCSS", () => {
    splitBookCSS(sheetA);
  });
});

describe("font-face text", () => {
  bench("extractBookFontFamilies", () => {
    extractBookFontFamilies(bookFontFaces);
  });

  bench("filterReaderFontFaces", () => {
    filterReaderFontFaces(readerFontFaces, bookFamilies);
  });
});
