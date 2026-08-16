// Behaviour suite for the shared icon wrapper.
//
// This is the repo's first .tsx test, and deliberately so. Icon's contract is
// what its JSX compiles to: innerHTML={markup(props.icon)} updates on a signal
// change only because Solid wraps that binding in an effect. Calling the
// component with a hand-built props object hands it a plain, non-reactive
// object, so the dynamic test below would report a bug that is not there. The
// vitest include pattern already covered .tsx; no config change was needed.
//
// Two live call sites depend on the dynamic path: ShareDialog's copy button
// (Copy to Check) and the reader's bookmark toggle (Bookmark to BookmarkCheck).

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type JSX, render } from "@solidjs/web";
import { createSignal, flush } from "solid-js";
import Icon from "~/lib/Icon";
import { Check, Copy, Search, Tag } from "~/lib/icons";

const disposers: Array<() => void> = [];

function mount(ui: () => JSX.Element): HTMLElement {
  const host = document.createElement("div");
  document.body.appendChild(host);
  disposers.push(render(ui, host));
  return host;
}

function svgIn(host: HTMLElement): Element {
  const svg = host.querySelector("svg");
  if (svg === null) throw new Error("Icon rendered no svg element");
  return svg;
}

// Compile-time half of the API: every caller must state exactly one intent.
function compileAccessibilityContract(): void {
  // @ts-expect-error Accessibility intent is required.
  void (<Icon icon={Search} />);
  // @ts-expect-error A meaningful icon cannot also be decorative.
  void (<Icon icon={Search} label="Search" decorative />);
}
void compileAccessibilityContract;

function sourcePath(path: string): string {
  return path.replaceAll("\\", "/");
}

function isProductionTsx(path: string): boolean {
  const normalized = sourcePath(path);
  return (
    normalized.endsWith(".tsx") &&
    !normalized.includes(".test.") &&
    normalized !== "src/lib/Icon.tsx"
  );
}

function productionTsxFiles(dir = "src"): string[] {
  const files: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) files.push(...productionTsxFiles(path));
    else if (isProductionTsx(path)) files.push(path);
  }
  return files;
}

afterEach(() => {
  while (disposers.length > 0) {
    const dispose = disposers.pop();
    if (dispose !== undefined) dispose();
  }
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("Icon", () => {
  it("normalises and classifies source census paths across platforms", () => {
    expect(sourcePath("src\\components\\library\\BookCard.tsx")).toBe(
      "src/components/library/BookCard.tsx",
    );
    expect(isProductionTsx("src\\components\\library\\BookCard.tsx")).toBe(
      true,
    );
    expect(isProductionTsx("src\\lib\\Icon.tsx")).toBe(false);
    expect(isProductionTsx("src\\lib\\Icon.test.tsx")).toBe(false);
    expect(isProductionTsx("src/lib/Icon.tsx")).toBe(false);
  });

  it("swaps geometry when the icon prop changes", () => {
    const [copied, setCopied] = createSignal(false);
    const host = mount(() => (
      <Icon icon={copied() ? Check : Copy} size={15} decorative />
    ));
    const before = svgIn(host).innerHTML;
    expect(before).toContain("rect");

    setCopied(true);
    flush();

    const after = svgIn(host).innerHTML;
    expect(after).not.toBe(before);
    expect(after).toContain("M20 6 9 17l-5-5");
    expect(after).not.toContain("rect");
  });

  it("defaults to 20px at stroke 1.75", () => {
    const svg = svgIn(mount(() => <Icon icon={Search} decorative />));
    expect(svg.getAttribute("width")).toBe("20");
    expect(svg.getAttribute("height")).toBe("20");
    expect(svg.getAttribute("stroke-width")).toBe("1.75");
  });

  it("takes size and stroke overrides", () => {
    const svg = svgIn(
      mount(() => <Icon icon={Search} size={15} stroke={2.5} decorative />),
    );
    expect(svg.getAttribute("width")).toBe("15");
    expect(svg.getAttribute("height")).toBe("15");
    expect(svg.getAttribute("stroke-width")).toBe("2.5");
  });

  it("exposes a labelled icon as an image", () => {
    const svg = svgIn(mount(() => <Icon icon={Search} label="Search" />));
    expect(svg.getAttribute("role")).toBe("img");
    expect(svg.getAttribute("aria-label")).toBe("Search");
    expect(svg.getAttribute("aria-hidden")).toBeNull();
  });

  it("hides an unlabelled icon from assistive tech", () => {
    // Icon-only buttons carry their own aria-label; a second accessible name
    // inside them would be announced twice.
    const svg = svgIn(mount(() => <Icon icon={Search} decorative />));
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.getAttribute("role")).toBeNull();
    expect(svg.getAttribute("aria-label")).toBeNull();
  });

  it("forwards class to the svg", () => {
    const svg = svgIn(
      mount(() => <Icon icon={Search} class="icon-btn" decorative />),
    );
    expect(svg.getAttribute("class")).toBe("icon-btn");
  });

  it("renders a stroke-only shell", () => {
    const svg = svgIn(mount(() => <Icon icon={Search} decorative />));
    expect(svg.getAttribute("fill")).toBe("none");
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(svg.getAttribute("viewBox")).toBe("0 0 24 24");
  });

  it("lets Tag's dot override the shell fill", () => {
    const svg = svgIn(mount(() => <Icon icon={Tag} decorative />));
    expect(svg.getAttribute("fill")).toBe("none");
    const dot = svg.querySelector("circle");
    expect(dot).not.toBeNull();
    expect(dot?.getAttribute("fill")).toBe("currentColor");
  });

  it("serialises every attribute from the table verbatim", () => {
    // Derived from the geometry rather than hardcoded, so this exercises the
    // serialiser instead of restating a copy of its input. Any change to the
    // name="value" shape markup() emits fails here.
    const html = svgIn(mount(() => <Icon icon={Tag} decorative />)).innerHTML;
    for (const [, attrs] of Tag) {
      for (const [name, value] of Object.entries(attrs)) {
        expect(html).toContain(name + '="' + value + '"');
      }
    }
  });

  it("renders the same glyph identically twice", () => {
    const a = svgIn(mount(() => <Icon icon={Search} decorative />)).innerHTML;
    const b = svgIn(mount(() => <Icon icon={Search} decorative />)).innerHTML;
    expect(a).toBe(b);
    expect(a.length).toBeGreaterThan(0);
  });

  it("requires an explicit accessibility intent at every production call site", () => {
    const violations: string[] = [];
    const intents = { decorative: 0, labelFromParent: 0, label: 0 };
    const files = new Set<string>();
    let calls = 0;

    for (const file of productionTsxFiles()) {
      const source = readFileSync(file, "utf8");
      for (const match of source.matchAll(/<Icon\b[\s\S]*?\/>/g)) {
        const tag = match[0];
        const declared = {
          decorative: /\bdecorative\b/.test(tag),
          labelFromParent: /\blabelFromParent\b/.test(tag),
          label: /\blabel\s*=/.test(tag),
        };
        const count = Object.values(declared).filter(Boolean).length;
        if (count !== 1) {
          violations.push(
            `${sourcePath(relative("src", file))}: ${tag.replace(/\s+/g, " ")}`,
          );
        }
        if (declared.decorative) intents.decorative += 1;
        if (declared.labelFromParent) intents.labelFromParent += 1;
        if (declared.label) intents.label += 1;
        files.add(file);
        calls += 1;
      }
    }

    expect(violations).toEqual([]);
    expect({ calls, files: files.size, ...intents }).toEqual({
      calls: 71,
      files: 19,
      decorative: 38,
      labelFromParent: 32,
      label: 1,
    });
  });

  it("warns when a decorative icon is the whole unnamed control", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button">
        <Icon icon={Search} decorative />
      </button>
    ));

    await Promise.resolve();

    expect(warn).toHaveBeenCalledOnce();
    expect(warn.mock.calls[0][0]).toContain("use labelFromParent");
  });

  it("warns when an icon-only labelled control declares decoration", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button" aria-label="Search">
        <Icon icon={Search} decorative />
      </button>
    ));

    await Promise.resolve();

    expect(warn).toHaveBeenCalledOnce();
    expect(warn.mock.calls[0][0]).toContain("use labelFromParent");
  });

  it("accepts an icon-only control named by its parent", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button" aria-label="Search">
        <Icon icon={Search} labelFromParent />
      </button>
    ));

    await Promise.resolve();

    expect(warn).not.toHaveBeenCalled();
  });

  it("ignores visual text explicitly hidden by the labelled parent", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button" aria-label="Choose theme">
        <span aria-hidden="true">Aa</span>
        <Icon icon={Search} labelFromParent />
      </button>
    ));

    await Promise.resolve();

    expect(warn).not.toHaveBeenCalled();
  });

  it("accepts a decorative icon beside exposed control text", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button">
        <Icon icon={Search} decorative />
        Search
      </button>
    ));

    await Promise.resolve();

    expect(warn).not.toHaveBeenCalled();
  });

  it("rejects labelFromParent when the parent has no explicit name", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button">
        <Icon icon={Search} labelFromParent />
      </button>
    ));

    await Promise.resolve();

    expect(warn).toHaveBeenCalledOnce();
    expect(warn.mock.calls[0][0]).toContain("non-empty aria-label");
  });

  it("rejects labelFromParent when exposed text owns the name", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mount(() => (
      <button type="button" aria-label="Search">
        <Icon icon={Search} labelFromParent />
        Search
      </button>
    ));

    await Promise.resolve();

    expect(warn).toHaveBeenCalledOnce();
  });
});
