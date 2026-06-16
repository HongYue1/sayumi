import { describe, it, expect } from "vitest";
import { generateCFI, resolveCFI } from "~/lib/cfi";

function setBody(html: string): void {
  document.body.innerHTML = html;
}

describe("generateCFI / resolveCFI", () => {
  it("round-trips a nested element to a 1-based element path", () => {
    setBody(`<div><p>a</p><p><span id="t">x</span></p></div>`);
    const target = document.getElementById("t")!;
    const cfi = generateCFI(target, document);
    // body > div(1) > p(2) > span(1)
    expect(cfi).toBe("cfi:1/2/1");
    expect(resolveCFI(cfi!, document)).toBe(target);
  });

  it("returns null when generating for body itself", () => {
    setBody(`<p>a</p>`);
    expect(generateCFI(document.body, document)).toBeNull();
  });

  it("returns null when generating for a detached element", () => {
    expect(generateCFI(document.createElement("div"), document)).toBeNull();
  });

  it("returns null when the CFI prefix is missing", () => {
    setBody(`<div></div>`);
    expect(resolveCFI("1/2/1", document)).toBeNull();
  });

  it("returns null when an index points past the children", () => {
    setBody(`<div></div>`);
    expect(resolveCFI("cfi:5", document)).toBeNull();
  });

  // Strict integer parse: a malformed/foreign segment must fail to null so
  // callers fall back to percent, rather than parseInt coercing it to a
  // wrong-but-valid index. Mirrors the (now deleted) inlined resolveCFILocal.
  for (const bad of [
    "cfi:3x",
    "cfi:1/1.5",
    "cfi:",
    "cfi:abc",
    "cfi:0",
    "cfi:1/0",
  ]) {
    it(`rejects the malformed CFI "${bad}"`, () => {
      setBody(`<div><p><span>x</span></p></div>`);
      expect(resolveCFI(bad, document)).toBeNull();
    });
  }
});
