import { expect, test } from "vitest";
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
// A shared suffix matches the first paragraph, not the end of the chapter.
const lateNeedle = foldQuery("paragraph number 119 of the fixture chapter");
const missNeedle = foldQuery("zzzz not present anywhere");

test("index build", async ({ bench }) => {
  expect(index.foldedChars.length).toBeGreaterThan(10_000);
  await bench("buildSearchTextIndex whole chapter", () => {
    buildSearchTextIndex(chapter);
  }).run();

  await bench("buildSearchTextIndex stops at match", () => {
    buildSearchTextIndex(chapter, 400);
  }).run();
});

test("folded scan", async ({ bench }) => {
  // Validate fixture labels outside the timed callbacks so empty/misleading
  // work cannot masquerade as a faster scan.
  expect(findFoldedMatch(index.foldedChars, missNeedle)).toBe(-1);
  expect(findFoldedMatch(index.foldedChars, lateNeedle)).toBeGreaterThan(
    index.foldedChars.length * 0.9,
  );
  await bench("findFoldedMatch miss", () => {
    findFoldedMatch(index.foldedChars, missNeedle);
  }).run();

  await bench("findFoldedMatch late hit", () => {
    findFoldedMatch(index.foldedChars, lateNeedle);
  }).run();

  await bench("foldQuery", () => {
    foldQuery("Istanbul Unicode fixture chapter");
  }).run();
});
