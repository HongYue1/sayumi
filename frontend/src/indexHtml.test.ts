// The pre-paint theme bootstrap in index.html never enters the module graph —
// nothing imports index.html — so no suite executes it unless one reads the
// file and runs the inline script verbatim. This suite does exactly that,
// pinning the cache contract the bootstrap shares with applyCachedTheme
// (lib/theme.ts): a cache that fails the shape check is refused whole, never
// painted.
import { readFileSync } from "node:fs";
import { beforeEach, describe, expect, it, vi } from "vitest";

// cwd is the frontend root under vitest, so a plain relative read works both
// in-sandbox and on the dev box.
function readBootstrap(): string {
  const html = readFileSync("index.html", "utf8");
  // The only bare <script> in the document; the module entry script carries
  // attributes and is not matched.
  const match = /<script>\n([\s\S]*?)<\/script>/.exec(html);
  if (!match) throw new Error("no inline bootstrap script in index.html");
  return match[1];
}

const script = readBootstrap();

// The verbatim script runs as a real module import: eval and the Function
// constructor are both lint errors here (no-eval, typescript/no-implied-eval),
// and a data: import executes in this realm with document/localStorage on
// globalThis. The fragment cache-buster defeats module caching so every arm
// re-executes the script fresh.
let runSeq = 0;
async function runBootstrap(): Promise<void> {
  runSeq += 1;
  await import(
    "data:text/javascript," + encodeURIComponent(script) + `#${runSeq}`
  );
}

function root(): HTMLElement {
  return document.documentElement;
}

function propValue(name: string): string {
  return root().style.getPropertyValue(name);
}

function stubCache(raw: string | null): void {
  vi.stubGlobal("localStorage", { getItem: () => raw });
}

const FULL_CACHE = JSON.stringify({
  id: "nord",
  bg: "#2e3440",
  fg: "#d8dee9",
  accent: "#88c0d0",
  accentFg: "#000000",
  elevated: "#3b4252",
  accentInk: "#88c0d0",
  scheme: "dark",
});

beforeEach(() => {
  root().removeAttribute("style");
  delete root().dataset.theme;
  vi.unstubAllGlobals();
});

describe("index.html pre-paint theme bootstrap", () => {
  it("applies a full valid cache before paint", async () => {
    stubCache(FULL_CACHE);
    await runBootstrap();
    expect(propValue("--bg")).toBe("#2e3440");
    expect(propValue("--fg")).toBe("#d8dee9");
    expect(propValue("--accent")).toBe("#88c0d0");
    expect(propValue("--accent-fg")).toBe("#000000");
    expect(propValue("--elevated")).toBe("#3b4252");
    expect(propValue("--accent-ink")).toBe("#88c0d0");
    expect(root().style.colorScheme).toBe("dark");
    expect(root().dataset.theme).toBe("nord");
  });

  it("no-ops on empty storage", async () => {
    stubCache(null);
    await runBootstrap();
    expect(propValue("--bg")).toBe("");
    expect(root().dataset.theme).toBeUndefined();
  });

  it("no-ops on malformed JSON", async () => {
    stubCache("{not json");
    await runBootstrap();
    expect(propValue("--bg")).toBe("");
    expect(root().dataset.theme).toBeUndefined();
  });

  it("refuses a cache whose core tokens are not strings", async () => {
    stubCache(
      JSON.stringify({
        id: "nord",
        bg: 42,
        fg: "#d8dee9",
        accent: "#88c0d0",
        accentFg: "#000000",
        scheme: "dark",
      }),
    );
    await runBootstrap();
    expect(propValue("--bg")).toBe("");
    expect(propValue("--fg")).toBe("");
    expect(root().dataset.theme).toBeUndefined();
  });

  it("tolerates a legacy cache without the newer tokens", async () => {
    stubCache(
      JSON.stringify({
        id: "nord",
        bg: "#2e3440",
        fg: "#d8dee9",
        accent: "#88c0d0",
        accentFg: "#000000",
        scheme: "dark",
      }),
    );
    await runBootstrap();
    expect(propValue("--bg")).toBe("#2e3440");
    expect(propValue("--accent-fg")).toBe("#000000");
    expect(propValue("--elevated")).toBe("");
    expect(propValue("--accent-ink")).toBe("");
    expect(root().dataset.theme).toBe("nord");
  });

  it("applies the palette but ignores a non-string theme id", async () => {
    stubCache(
      JSON.stringify({
        id: 7,
        bg: "#2e3440",
        fg: "#d8dee9",
        accent: "#88c0d0",
        accentFg: "#000000",
        scheme: "dark",
      }),
    );
    await runBootstrap();
    expect(propValue("--bg")).toBe("#2e3440");
    expect(root().dataset.theme).toBeUndefined();
  });
});

describe("pre-paint parity with the cache writer", () => {
  // The arms above prove the bootstrap BEHAVES; they all hardcode today's
  // token list, so a seventh token added to paintTheme and the cache would
  // pass every one of them and simply not pre-paint. The bootstrap cannot
  // import lib/theme.ts -- it runs one realm and one network round-trip
  // earlier, which is the entire point of duplicating the list -- so this is
  // the only place the copy can be held to the original.
  const themeSource = readFileSync("src/lib/theme.ts", "utf8");

  function region(source: string, from: string): string {
    const start = source.indexOf(from);
    expect(start, from).toBeGreaterThan(-1);
    const end = source.indexOf("\n}\n", start);
    expect(end, `end of ${from}`).toBeGreaterThan(start);
    return source.slice(start, end);
  }

  function tokens(source: string): string[] {
    return [
      ...new Set(
        Array.from(
          source.matchAll(/setProperty\(\s*"(--[a-z-]+)"/gu),
          (match) => match[1] ?? "",
        ),
      ),
    ].sort();
  }

  function required(source: string): string[] {
    return [
      ...new Set(
        Array.from(
          source.matchAll(/typeof v\.(\w+) !== "string"/gu),
          (match) => match[1] ?? "",
        ),
      ),
    ].sort();
  }

  it("paints exactly the tokens paintTheme paints", () => {
    expect(tokens(script)).toEqual(
      tokens(region(themeSource, "function paintTheme")),
    );
  });

  it("paints exactly the tokens applyCachedTheme paints", () => {
    expect(tokens(script)).toEqual(
      tokens(region(themeSource, "function applyCachedTheme")),
    );
  });

  it("refuses a cache on the same core tokens applyCachedTheme requires", () => {
    // applyCachedTheme also compares v.id to the id it was asked for; the
    // bootstrap has no id to compare against, which is why it treats a
    // non-string id as "paint the palette, skip the attribute" above.
    expect(required(script)).toEqual(
      required(region(themeSource, "function applyCachedTheme")),
    );
  });

  it("caches every token it pre-paints", () => {
    const writer = region(themeSource, "JSON.stringify({");
    for (const token of tokens(script)) {
      // --accent-fg is cached as accentFg.
      const key = token
        .slice(2)
        .replace(/-(\w)/gu, (_, char: string) => char.toUpperCase());
      expect(writer, token).toMatch(new RegExp(`^\\s*${key}[,:]`, "mu"));
    }
  });
});
