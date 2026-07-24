import { afterEach, describe, expect, it } from "vitest";
import { focusTrap } from "~/lib/focusTrap";

function markVisible(element: HTMLElement): void {
  Object.defineProperty(element, "getClientRects", {
    configurable: true,
    value: () => [{}],
  });
}

function pressTab(target: HTMLElement, shiftKey = false): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    key: "Tab",
    shiftKey,
    bubbles: true,
    cancelable: true,
  });
  target.dispatchEvent(event);
  return event;
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("focusTrap", () => {
  it("focuses an empty non-tabbable container and restores its markup", async () => {
    const trigger = document.createElement("button");
    const dialog = document.createElement("div");
    document.body.append(trigger, dialog);
    trigger.focus();

    const dispose = focusTrap(dialog);
    await Promise.resolve();

    expect(document.activeElement).toBe(dialog);
    expect(dialog.getAttribute("tabindex")).toBe("-1");

    dispose();

    expect(dialog.hasAttribute("tabindex")).toBe(false);
    expect(document.activeElement).toBe(trigger);
  });

  it("recovers Tab and Shift+Tab when focus has escaped", async () => {
    const trigger = document.createElement("button");
    const dialog = document.createElement("div");
    const first = document.createElement("button");
    const last = document.createElement("button");
    markVisible(first);
    markVisible(last);
    dialog.append(first, last);
    document.body.append(trigger, dialog);
    trigger.focus();

    const dispose = focusTrap(dialog);
    await Promise.resolve();
    expect(document.activeElement).toBe(first);

    trigger.focus();
    const forward = pressTab(trigger);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(first);

    trigger.focus();
    const backward = pressTab(trigger, true);
    expect(backward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(last);

    dispose();
  });

  it("lets only the topmost nested trap contain focus", async () => {
    const trigger = document.createElement("button");
    const outer = document.createElement("div");
    const outerButton = document.createElement("button");
    const inner = document.createElement("div");
    const innerButton = document.createElement("button");
    markVisible(outerButton);
    markVisible(innerButton);
    inner.append(innerButton);
    outer.append(outerButton, inner);
    document.body.append(trigger, outer);
    trigger.focus();

    const disposeOuter = focusTrap(outer);
    await Promise.resolve();
    expect(document.activeElement).toBe(outerButton);

    const disposeInner = focusTrap(inner);
    await Promise.resolve();
    expect(document.activeElement).toBe(innerButton);

    outerButton.focus();
    pressTab(outerButton);
    expect(document.activeElement).toBe(innerButton);

    disposeInner();
    expect(document.activeElement).toBe(outerButton);
    disposeOuter();
    expect(document.activeElement).toBe(trigger);
  });
});
