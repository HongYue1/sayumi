// Card size control in the library masthead: a trigger that opens a small
// popover holding one slider, which drives the shelf's column floor
// (lib/cardSize.ts -> --card-size -> .lib-grid).
//
// Its own component rather than more state inside Library.tsx: a popover needs
// two window listeners, a focus-out guard and a focus-into-popover microtask
// with its own generation counter, and Library.tsx already carries one copy of
// that machinery for the sort menu.
//
// The patterns are ProfileMenu's, for the reasons documented in full there:
//   - Outside dismiss is a window pointerdown listener, never a fixed scrim:
//     the sticky masthead's backdrop-filter establishes a containing block, so
//     a fixed scrim is clipped to the masthead and never covers the shelf.
//   - The Escape listener is BUBBLE phase, so an Escape belonging to an
//     overlay stacked above this popover (they all capture and call
//     stopImmediatePropagation) never reaches it.
//   - Focus moves into the popover from a queueMicrotask guarded by a
//     generation counter: Solid runs refs while the node is still detached, so
//     a self-focusing ref would silently no-op.
import { createEffect, createSignal, Show } from "solid-js";
import {
  cardSize,
  CARD_SIZE_MAX,
  CARD_SIZE_MIN,
  CARD_SIZE_SEED,
} from "~/lib/cardSize";
import Icon from "~/lib/Icon";
import { LayoutGrid } from "~/lib/icons";

export default function CardSizeControl() {
  const [open, setOpen] = createSignal(false);
  let trigger: HTMLButtonElement | undefined;
  let popEl: HTMLElement | undefined;
  let slider: HTMLInputElement | undefined;

  // "Auto" is a real state, not a number: the shelf keeps its fluid default
  // until a size is chosen, so the label has to be able to say so.
  const label = (): string =>
    cardSize.value === null ? "Auto" : `${cardSize.value}px`;

  function close(restoreFocus = true): void {
    if (!open()) return;
    setOpen(false);
    if (restoreFocus) trigger?.focus();
  }

  function onWindowPointerDown(e: PointerEvent): void {
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (popEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }

  function onWindowKeyDown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    close();
  }

  // A null relatedTarget means focus is leaving the document (window blur,
  // devtools), which must not close the popover mid-drag.
  function onRootFocusOut(
    e: FocusEvent & { currentTarget: HTMLDivElement },
  ): void {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget.contains(next)) return;
    close(false);
  }

  createEffect(
    () => open(),
    (isOpen) => {
      if (!isOpen) return undefined;
      window.addEventListener("pointerdown", onWindowPointerDown);
      window.addEventListener("keydown", onWindowKeyDown);
      return () => {
        window.removeEventListener("pointerdown", onWindowPointerDown);
        window.removeEventListener("keydown", onWindowKeyDown);
      };
    },
  );

  let popGen = 0;
  createEffect(
    () => open(),
    (isOpen) => {
      // Bumped on open AND close, so a queued focus for a popover that has
      // since closed is dropped instead of stealing focus off the trigger.
      const gen = ++popGen;
      if (!isOpen) return undefined;
      queueMicrotask(() => {
        if (gen !== popGen) return;
        (slider ?? popEl)?.focus();
      });
      return undefined;
    },
  );

  return (
    <div class="lib-size" onFocusOut={onRootFocusOut}>
      <button
        type="button"
        ref={(el) => (trigger = el)}
        id="lib-size-trigger"
        class={["icon-btn press lib-size-trigger", { open: open() }]}
        aria-haspopup="true"
        aria-expanded={open() ? "true" : "false"}
        aria-label={`Card size: ${label()}`}
        title="Card size"
        onClick={() => setOpen(!open())}
      >
        <Icon icon={LayoutGrid} size={17} labelFromParent />
      </button>

      <Show when={open()}>
        <div
          ref={(el) => (popEl = el)}
          class="lib-size-pop paper"
          role="group"
          tabindex="-1"
          aria-labelledby="lib-size-trigger"
        >
          <div class="lib-size-head">
            <p class="eyebrow">Card size</p>
            <span class="lib-size-value tnum">{label()}</span>
          </div>
          <input
            ref={(el) => (slider = el)}
            class="lib-size-range"
            type="range"
            min={CARD_SIZE_MIN}
            max={CARD_SIZE_MAX}
            step="4"
            value={String(cardSize.value ?? CARD_SIZE_SEED)}
            aria-label="Card width in pixels"
            aria-valuetext={label()}
            onInput={(e) => cardSize.set(Number(e.currentTarget.value))}
          />
          <div class="lib-size-foot">
            <span class="lib-size-hint">Smaller</span>
            <button
              type="button"
              class="lib-size-auto"
              // aria-disabled rather than disabled: a real disabled attribute
              // on the only other control in here would move focus out of the
              // popover the moment a size is reset.
              aria-disabled={cardSize.value === null ? "true" : "false"}
              onClick={() => {
                if (cardSize.value === null) return;
                cardSize.reset();
              }}
            >
              Auto
            </button>
            <span class="lib-size-hint">Larger</span>
          </div>
        </div>
      </Show>
    </div>
  );
}
