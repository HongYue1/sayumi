// Shared keyboard ownership for the shell, reader route, and book frame.
// Keep this pure: frame.ts bundles it into the sandboxed srcdoc application.

const BUTTON_INPUT_TYPES = new Set([
  "button",
  "checkbox",
  "file",
  "reset",
  "submit",
]);
const MEDIA_KEYS = new Set([
  " ",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "ArrowUp",
  "End",
  "Home",
]);

function firstHTMLElement(target: EventTarget | null): HTMLElement | null {
  let element = target instanceof Element ? target : null;
  // Keyboard events can originate on SVG descendants inside an editable host.
  // Climb to the first HTML element so inherited contenteditable still wins.
  while (element !== null && !(element instanceof HTMLElement)) {
    element = element.parentElement;
  }
  return element;
}

/** True when ordinary reader/shell shortcuts belong to the focused target. */
export function isKeyboardConsumer(target: EventTarget | null): boolean {
  const element = firstHTMLElement(target);
  if (element === null) return false;
  if (element.isContentEditable) return true;

  if (element.localName === "textarea" || element.localName === "select") {
    return true;
  }
  if (element.localName !== "input") return false;
  return !BUTTON_INPUT_TYPES.has((element as HTMLInputElement).type);
}

function targetOwnsKey(target: EventTarget | null, key: string): boolean {
  const element = firstHTMLElement(target);
  if (element === null) return false;
  if (isKeyboardConsumer(element)) return true;

  // Keep letters and Escape available as reader shortcuts on buttons, while
  // leaving Space to the native activation behavior. The same distinction
  // protects disclosure widgets retained in EPUB content.
  if (key === " ") {
    if (element.localName === "button" || element.localName === "summary") {
      return true;
    }
    if (
      element.localName === "input" &&
      BUTTON_INPUT_TYPES.has((element as HTMLInputElement).type)
    ) {
      return true;
    }
  }

  return (
    (element.localName === "audio" || element.localName === "video") &&
    element.hasAttribute("controls") &&
    MEDIA_KEYS.has(key)
  );
}

/** Composition wins before any target or shortcut rule. The fallback covers
 * synthetic/window-dispatched events whose target is not the active element. */
export function keyboardEventIsOwnedByTarget(
  event: KeyboardEvent,
  fallback: EventTarget | null = null,
): boolean {
  return (
    event.isComposing ||
    targetOwnsKey(event.target, event.key) ||
    targetOwnsKey(fallback, event.key)
  );
}
