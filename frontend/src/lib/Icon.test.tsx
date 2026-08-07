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

import { afterEach, describe, expect, it } from "vitest";
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

afterEach(() => {
  while (disposers.length > 0) {
    const dispose = disposers.pop();
    if (dispose !== undefined) dispose();
  }
  document.body.innerHTML = "";
});

describe("Icon", () => {
  it("swaps geometry when the icon prop changes", () => {
    const [copied, setCopied] = createSignal(false);
    const host = mount(() => <Icon icon={copied() ? Check : Copy} size={15} />);
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
    const svg = svgIn(mount(() => <Icon icon={Search} />));
    expect(svg.getAttribute("width")).toBe("20");
    expect(svg.getAttribute("height")).toBe("20");
    expect(svg.getAttribute("stroke-width")).toBe("1.75");
  });

  it("takes size and stroke overrides", () => {
    const svg = svgIn(
      mount(() => <Icon icon={Search} size={15} stroke={2.5} />),
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
    const svg = svgIn(mount(() => <Icon icon={Search} />));
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.getAttribute("role")).toBeNull();
    expect(svg.getAttribute("aria-label")).toBeNull();
  });

  it("forwards class to the svg", () => {
    const svg = svgIn(mount(() => <Icon icon={Search} class="icon-btn" />));
    expect(svg.getAttribute("class")).toBe("icon-btn");
  });

  it("renders a stroke-only shell", () => {
    const svg = svgIn(mount(() => <Icon icon={Search} />));
    expect(svg.getAttribute("fill")).toBe("none");
    expect(svg.getAttribute("stroke")).toBe("currentColor");
    expect(svg.getAttribute("viewBox")).toBe("0 0 24 24");
  });

  it("lets Tag's dot override the shell fill", () => {
    const svg = svgIn(mount(() => <Icon icon={Tag} />));
    expect(svg.getAttribute("fill")).toBe("none");
    const dot = svg.querySelector("circle");
    expect(dot).not.toBeNull();
    expect(dot?.getAttribute("fill")).toBe("currentColor");
  });

  it("serialises every attribute from the table verbatim", () => {
    // Derived from the geometry rather than hardcoded, so this exercises the
    // serialiser instead of restating a copy of its input. Any change to the
    // name="value" shape markup() emits fails here.
    const html = svgIn(mount(() => <Icon icon={Tag} />)).innerHTML;
    for (const [, attrs] of Tag) {
      for (const [name, value] of Object.entries(attrs)) {
        expect(html).toContain(name + '="' + value + '"');
      }
    }
  });

  it("renders the same glyph identically twice", () => {
    const a = svgIn(mount(() => <Icon icon={Search} />)).innerHTML;
    const b = svgIn(mount(() => <Icon icon={Search} />)).innerHTML;
    expect(a).toBe(b);
    expect(a.length).toBeGreaterThan(0);
  });
});
