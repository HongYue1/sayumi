// frame.css contract: the paged multicol scroller must not be user-scrollable.
// Page turns drive scrollLeft programmatically; a natively scrollable #content
// lets a touchpad horizontal swipe move the columns underneath the controller
// with no page turn registering (currentPage, indicator, and position report
// all desync). Reads the shipped stylesheet from disk — the ?raw bundler
// import resolves empty under vitest, so this asserts against the real file.
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";

const frameCSS = readFileSync("src/iframe/frame.css", "utf8");

function mountShell(): HTMLElement {
  document.head.innerHTML = `<style>${frameCSS}</style>`;
  document.body.innerHTML =
    '<div id="paged-clip"><div id="content"><div id="content-inner"></div></div></div>';
  const content = document.getElementById("content");
  if (!content) throw new Error("shell fixture missing");
  return content;
}

afterEach(() => {
  document.documentElement.className = "";
  document.head.innerHTML = "";
  document.body.innerHTML = "";
});

describe("paged #content overflow", () => {
  it.each(["paged", "paged-two"] as const)(
    "is user-locked in %s mode",
    (mode) => {
      const content = mountShell();
      document.documentElement.classList.add(mode);
      expect(getComputedStyle(content).overflowX).toBe("hidden");
    },
  );

  it("stays natively scrollable in scroll mode", () => {
    const content = mountShell();
    // No paged class: the scroll-mode #content has no overflow-x rule, so a
    // wheel keeps driving the chapter the way scroll mode expects.
    expect(getComputedStyle(content).overflowX).not.toBe("hidden");
  });
});
