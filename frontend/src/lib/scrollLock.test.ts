import { afterEach, describe, expect, it, vi } from "vitest";
import { lockDocumentScroll } from "~/lib/scrollLock";

afterEach(() => {
  document.documentElement.removeAttribute("style");
  document.body.removeAttribute("style");
  vi.restoreAllMocks();
});

function restoreProperty(
  object: object,
  property: PropertyKey,
  descriptor: PropertyDescriptor | undefined,
): void {
  if (descriptor === undefined) Reflect.deleteProperty(object, property);
  else Object.defineProperty(object, property, descriptor);
}

describe("lockDocumentScroll", () => {
  it("restores the exact inline styles and viewport position", () => {
    document.documentElement.style.overflow = "clip";
    Object.assign(document.body.style, {
      overflow: "auto",
      position: "relative",
      top: "2px",
      left: "3px",
      right: "4px",
      width: "75%",
      paddingRight: "7px",
    });
    const oldX = Object.getOwnPropertyDescriptor(window, "scrollX");
    const oldY = Object.getOwnPropertyDescriptor(window, "scrollY");
    Object.defineProperty(window, "scrollX", { configurable: true, value: 13 });
    Object.defineProperty(window, "scrollY", {
      configurable: true,
      value: 240,
    });
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});

    const release = lockDocumentScroll(document);
    try {
      expect(document.documentElement.style.overflow).toBe("hidden");
      expect(document.body.style.overflow).toBe("hidden");
      expect(document.body.style.position).toBe("fixed");
      expect(document.body.style.top).toBe("-240px");
      expect(document.body.style.left).toBe("-13px");
      expect(document.body.style.right).toBe("0px");
      expect(document.body.style.width).toBe("auto");
    } finally {
      release();
      restoreProperty(window, "scrollX", oldX);
      restoreProperty(window, "scrollY", oldY);
    }

    expect(document.documentElement.style.overflow).toBe("clip");
    expect(document.body.style.overflow).toBe("auto");
    expect(document.body.style.position).toBe("relative");
    expect(document.body.style.top).toBe("2px");
    expect(document.body.style.left).toBe("3px");
    expect(document.body.style.right).toBe("4px");
    expect(document.body.style.width).toBe("75%");
    expect(document.body.style.paddingRight).toBe("7px");
    expect(scrollTo).toHaveBeenCalledWith(13, 240);
  });

  it("compensates a classic scrollbar without losing existing padding", () => {
    document.body.style.paddingRight = "6px";
    const oldClientWidth = Object.getOwnPropertyDescriptor(
      document.documentElement,
      "clientWidth",
    );
    const oldInnerWidth = Object.getOwnPropertyDescriptor(window, "innerWidth");
    Object.defineProperty(document.documentElement, "clientWidth", {
      configurable: true,
      value: 980,
    });
    Object.defineProperty(window, "innerWidth", {
      configurable: true,
      value: 1000,
    });

    const release = lockDocumentScroll(document);
    try {
      expect(document.body.style.paddingRight).toBe("26px");
    } finally {
      release();
      restoreProperty(document.documentElement, "clientWidth", oldClientWidth);
      restoreProperty(window, "innerWidth", oldInnerWidth);
    }
    expect(document.body.style.paddingRight).toBe("6px");
  });

  it("waits for the final owner and makes every release idempotent", () => {
    document.body.style.position = "relative";
    const releaseOuter = lockDocumentScroll(document);
    const releaseInner = lockDocumentScroll(document);

    expect(document.body.style.position).toBe("fixed");
    releaseOuter();
    releaseOuter();
    expect(document.body.style.position).toBe("fixed");

    releaseInner();
    expect(document.body.style.position).toBe("relative");
    releaseInner();
    expect(document.body.style.position).toBe("relative");
  });
});
