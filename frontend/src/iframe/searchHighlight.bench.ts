import { bench, describe } from "vitest";
import {
  buildSearchTextIndex,
  findFoldedMatch,
  foldQuery,
} from "./searchHighlight";

// Search navigation rebuilds the character index on every "next match", so the
// index walk and the folded scan are the hot paths of this module. Fixture is
// chapter-sized: prose with inline markup, roughly what a novel chapter hands
// the frame.
function buildChapter(paragraphs: number): HTMLElement {
  const root = document.createElement("div");
  for (let i = 0; i < paragraphs; i += 1) {
    const p = document.createElement("p");
    p.innerHTML =
      "The quick brown fox jumps over the lazy dog while " +
      "<em>Unicode</em> and <strong>Istanbul</strong> keep the folding path " +
      "honest, paragraph number " +
      i +
      " of the fixture chapter.";
    root.appendChild(p);
  }
  return root;
}

const chapter = buildChapter(120);
const index = buildSearchTextIndex(chapter);
const lateNeedle = foldQuery("of the fixture chapter");
const missNeedle = foldQuery("zzzz not present anywhere");

describe("index build", () => {
  bench("buildSearchTextIndex whole chapter", () => {
    buildSearchTextIndex(chapter);
  });

  bench("buildSearchTextIndex stops at match", () => {
    buildSearchTextIndex(chapter, 400);
  });
});

describe("folded scan", () => {
  bench("findFoldedMatch miss", () => {
    findFoldedMatch(index.foldedChars, missNeedle);
  });

  bench("findFoldedMatch late hit", () => {
    findFoldedMatch(index.foldedChars, lateNeedle);
  });

  bench("foldQuery", () => {
    foldQuery("Istanbul Unicode fixture chapter");
  });
});
