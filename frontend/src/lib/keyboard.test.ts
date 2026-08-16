import { afterEach, describe, expect, it } from "vitest";
import {
  isKeyboardConsumer,
  keyboardEventIsOwnedByTarget,
} from "~/lib/keyboard";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("isKeyboardConsumer", () => {
  it("classifies text and navigation controls", () => {
    for (const tag of ["textarea", "select"] as const) {
      expect(isKeyboardConsumer(document.createElement(tag))).toBe(true);
    }

    for (const type of [
      "text",
      "search",
      "number",
      "date",
      "color",
      "range",
      "radio",
    ]) {
      const input = document.createElement("input");
      input.type = type;
      expect(isKeyboardConsumer(input), type).toBe(true);
    }
  });

  it("leaves button-like inputs and ordinary elements shortcut-capable", () => {
    for (const type of ["button", "checkbox", "file", "image", "reset", "submit"]) {
      const input = document.createElement("input");
      input.type = type;
      expect(isKeyboardConsumer(input), type).toBe(false);
    }
    expect(isKeyboardConsumer(document.createElement("button"))).toBe(false);
    expect(isKeyboardConsumer(document.createElement("div"))).toBe(false);
    expect(isKeyboardConsumer(window)).toBe(false);
    expect(isKeyboardConsumer(null)).toBe(false);
  });

  it("respects inherited and explicitly disabled contenteditable", () => {
    const host = document.createElement("div");
    host.contentEditable = "true";
    host.innerHTML =
      '<span id="editable"><svg><text id="vector">edit</text></svg></span><span contenteditable="false"><b id="locked">locked</b></span>';
    document.body.append(host);

    expect(isKeyboardConsumer(document.getElementById("editable"))).toBe(true);
    expect(isKeyboardConsumer(document.getElementById("vector"))).toBe(true);
    expect(isKeyboardConsumer(document.getElementById("locked"))).toBe(false);
  });
});

describe("keyboardEventIsOwnedByTarget", () => {
  function key(target: Element, value: string): KeyboardEvent {
    const event = new KeyboardEvent("keydown", { key: value });
    target.dispatchEvent(event);
    return event;
  }

  it("gives composition unconditional ownership", () => {
    const event = new KeyboardEvent("keydown", {
      key: "Escape",
      isComposing: true,
    });
    expect(keyboardEventIsOwnedByTarget(event)).toBe(true);
  });

  it("uses the event target before the active-element fallback", () => {
    const editor = document.createElement("div");
    editor.contentEditable = "true";
    document.body.append(editor);
    expect(
      keyboardEventIsOwnedByTarget(
        key(editor, "k"),
        document.createElement("button"),
      ),
    ).toBe(true);

    const fallback = document.createElement("input");
    fallback.type = "text";
    const untargeted = new KeyboardEvent("keydown", { key: "?" });
    expect(keyboardEventIsOwnedByTarget(untargeted, fallback)).toBe(true);
    expect(
      keyboardEventIsOwnedByTarget(
        untargeted,
        document.createElement("button"),
      ),
    ).toBe(false);
  });

  it("preserves native activation without swallowing letter shortcuts", () => {
    const button = document.createElement("button");
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    const image = document.createElement("input");
    image.type = "image";
    const summary = document.createElement("summary");
    const video = document.createElement("video");
    video.controls = true;

    expect(keyboardEventIsOwnedByTarget(key(button, " "))).toBe(true);
    expect(keyboardEventIsOwnedByTarget(key(checkbox, " "))).toBe(true);
    expect(keyboardEventIsOwnedByTarget(key(image, " "))).toBe(true);
    expect(keyboardEventIsOwnedByTarget(key(summary, " "))).toBe(true);
    expect(keyboardEventIsOwnedByTarget(key(video, "ArrowRight"))).toBe(true);
    expect(keyboardEventIsOwnedByTarget(key(button, "s"))).toBe(false);
    expect(keyboardEventIsOwnedByTarget(key(image, "s"))).toBe(false);
  });
});
