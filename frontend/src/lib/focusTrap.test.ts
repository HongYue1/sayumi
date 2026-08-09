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
  document.documentElement.removeAttribute("style");
  document.body.removeAttribute("style");
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

  it("locks document scrolling until the final nested trap releases it", async () => {
    document.documentElement.style.overflow = "clip";
    document.body.style.position = "relative";
    const outer = document.createElement("div");
    const inner = document.createElement("div");
    outer.append(inner);
    document.body.append(outer);

    const disposeOuter = focusTrap(outer);
    const disposeInner = focusTrap(inner);
    try {
      expect(document.documentElement.style.overflow).toBe("hidden");
      expect(document.body.style.position).toBe("fixed");

      disposeInner();
      expect(document.documentElement.style.overflow).toBe("hidden");
      expect(document.body.style.position).toBe("fixed");
    } finally {
      disposeInner();
      disposeOuter();
    }

    expect(document.documentElement.style.overflow).toBe("clip");
    expect(document.body.style.position).toBe("relative");
    document.documentElement.removeAttribute("style");
    document.body.removeAttribute("style");
  });

  it("includes summary, frames, media controls, and editors in the tab ring", async () => {
    const dialog = document.createElement("div");
    const inertEditor = document.createElement("div");
    inertEditor.setAttribute("contenteditable", "FALSE");
    const invalidEditor = document.createElement("div");
    invalidEditor.setAttribute("contenteditable", "bogus");
    const inertAudio = document.createElement("audio");
    const details = document.createElement("details");
    details.open = true;
    const summary = document.createElement("summary");
    details.append(summary);
    const frame = document.createElement("iframe");
    const audio = document.createElement("audio");
    audio.controls = true;
    const video = document.createElement("video");
    video.controls = true;
    const editor = document.createElement("div");
    editor.setAttribute("contenteditable", "plaintext-only");
    for (const element of [
      inertEditor,
      invalidEditor,
      inertAudio,
      summary,
      frame,
      audio,
      video,
      editor,
    ]) {
      markVisible(element);
    }
    dialog.append(
      inertEditor,
      invalidEditor,
      inertAudio,
      details,
      frame,
      audio,
      video,
      editor,
    );
    document.body.append(dialog);

    const dispose = focusTrap(dialog);
    try {
      await Promise.resolve();
      expect(document.activeElement).toBe(summary);

      editor.focus();
      const forward = pressTab(editor);
      expect(forward.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(summary);

      summary.focus();
      const backward = pressTab(summary, true);
      expect(backward.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(editor);
    } finally {
      dispose();
    }
  });

  it("recognizes every added native surface as a sole tab stop", async () => {
    const cases: Array<{
      name: string;
      create: () => { root: HTMLElement; target: HTMLElement };
    }> = [
      {
        name: "summary",
        create: () => {
          const root = document.createElement("details");
          root.open = true;
          const target = document.createElement("summary");
          root.append(target);
          return { root, target };
        },
      },
      {
        name: "iframe",
        create: () => {
          const target = document.createElement("iframe");
          return { root: target, target };
        },
      },
      {
        name: "audio controls",
        create: () => {
          const target = document.createElement("audio");
          target.controls = true;
          return { root: target, target };
        },
      },
      {
        name: "video controls",
        create: () => {
          const target = document.createElement("video");
          target.controls = true;
          return { root: target, target };
        },
      },
      {
        name: "editing host",
        create: () => {
          const target = document.createElement("div");
          target.setAttribute("contenteditable", "TRUE");
          return { root: target, target };
        },
      },
    ];

    for (const { name, create } of cases) {
      document.body.replaceChildren();
      const dialog = document.createElement("div");
      const { root, target } = create();
      markVisible(target);
      dialog.append(root);
      document.body.append(dialog);

      const dispose = focusTrap(dialog);
      try {
        await Promise.resolve();
        expect(document.activeElement, name).toBe(target);
        const forward = pressTab(target);
        expect(forward.defaultPrevented, name).toBe(true);
        expect(document.activeElement, name).toBe(target);
      } finally {
        dispose();
      }
    }
  });

  it("rejects false, invalid, non-control, hidden, and negative candidates", async () => {
    const dialog = document.createElement("div");
    const inertAudio = document.createElement("audio");
    const falseEditor = document.createElement("div");
    falseEditor.setAttribute("contenteditable", "FALSE");
    const invalidEditor = document.createElement("div");
    invalidEditor.setAttribute("contenteditable", "bogus");
    const negativeSummary = document.createElement("summary");
    negativeSummary.tabIndex = -1;
    const hiddenButton = document.createElement("button");
    Object.defineProperty(hiddenButton, "getClientRects", {
      configurable: true,
      value: () => [],
    });
    const validButton = document.createElement("button");
    for (const element of [
      inertAudio,
      falseEditor,
      invalidEditor,
      negativeSummary,
      validButton,
    ]) {
      markVisible(element);
    }
    dialog.append(
      inertAudio,
      falseEditor,
      invalidEditor,
      negativeSummary,
      hiddenButton,
      validButton,
    );
    document.body.append(dialog);

    const dispose = focusTrap(dialog);
    try {
      await Promise.resolve();
      expect(document.activeElement).toBe(validButton);
      const forward = pressTab(validButton);
      expect(forward.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(validButton);
    } finally {
      dispose();
    }
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

  it("counts only controls that are in the tab order", async () => {
    const dialog = document.createElement("div");
    dialog.tabIndex = -1;
    const close = document.createElement("button");
    const rowBefore = document.createElement("button");
    const rowStop = document.createElement("button");
    const rowAfter = document.createElement("button");
    rowBefore.tabIndex = -1;
    rowStop.tabIndex = 0;
    rowAfter.tabIndex = -1;
    markVisible(close);
    markVisible(rowBefore);
    markVisible(rowStop);
    markVisible(rowAfter);
    dialog.append(close, rowBefore, rowStop, rowAfter);
    document.body.append(dialog);

    const dispose = focusTrap(dialog);
    await Promise.resolve();

    // TocPanel's virtualized rows are the last elements in the panel and all
    // but one carry tabindex="-1". Counting them made rowAfter the wrap point,
    // so Tab from the real last stop was never intercepted and walked out of
    // the dialog, and Shift+Tab drove focus onto a row nothing can tab back to.
    rowStop.focus();
    const forward = pressTab(rowStop);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(close);

    const backward = pressTab(close, true);
    expect(backward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(rowStop);

    dispose();
  });

  it("keeps Tab inside when the container itself holds focus", async () => {
    const outside = document.createElement("button");
    markVisible(outside);
    const dialog = document.createElement("div");
    dialog.tabIndex = -1;
    const first = document.createElement("button");
    const last = document.createElement("button");
    markVisible(first);
    markVisible(last);
    dialog.append(first, last);
    document.body.append(outside, dialog);

    const dispose = focusTrap(dialog);
    await Promise.resolve();

    dialog.focus();
    const forward = pressTab(dialog);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(first);

    dialog.focus();
    const backward = pressTab(dialog, true);
    expect(backward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(last);

    dispose();
  });

  it("moves focus in when tab stops arrive after the fallback", async () => {
    const dialog = document.createElement("div");
    document.body.append(dialog);

    const dispose = focusTrap(dialog);
    await Promise.resolve();
    expect(document.activeElement).toBe(dialog);

    // The reader panels are clientOnly: the wrapper mounts a microtask ahead of
    // its contents, so the fallback fires against an empty container.
    const button = document.createElement("button");
    markVisible(button);
    dialog.append(button);
    await Promise.resolve();
    await Promise.resolve();

    expect(document.activeElement).toBe(button);
    dispose();
  });

  it("does not override a dialog that placed focus itself", async () => {
    const dialog = document.createElement("div");
    document.body.append(dialog);

    const dispose = focusTrap(dialog);
    await Promise.resolve();

    const close = document.createElement("button");
    const field = document.createElement("input");
    markVisible(close);
    markVisible(field);
    dialog.append(close, field);
    field.focus();
    await Promise.resolve();
    await Promise.resolve();

    expect(document.activeElement).toBe(field);
    dispose();
  });

  it("ignores a second dispose", async () => {
    const trigger = document.createElement("button");
    const elsewhere = document.createElement("button");
    const dialog = document.createElement("div");
    const inner = document.createElement("button");
    markVisible(inner);
    dialog.append(inner);
    document.body.append(trigger, elsewhere, dialog);
    trigger.focus();

    const dispose = focusTrap(dialog);
    await Promise.resolve();
    dispose();
    expect(document.activeElement).toBe(trigger);

    elsewhere.focus();
    dispose();

    expect(document.activeElement).toBe(elsewhere);
  });

  it("leaves focus alone when something else took it before teardown", async () => {
    const trigger = document.createElement("button");
    const elsewhere = document.createElement("button");
    const dialog = document.createElement("div");
    const inner = document.createElement("button");
    markVisible(inner);
    dialog.append(inner);
    document.body.append(trigger, elsewhere, dialog);
    trigger.focus();

    const dispose = focusTrap(dialog);
    await Promise.resolve();
    expect(document.activeElement).toBe(inner);

    elsewhere.focus();
    dispose();

    expect(document.activeElement).toBe(elsewhere);
  });
});
