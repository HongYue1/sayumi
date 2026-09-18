// DropSelect: a custom single-select dropdown (button trigger + listbox
// popup) replacing the native <select> elements in the reader settings.
// Native popups render each option as its own rounded pill under a dark
// color-scheme, which no author CSS can reliably flatten -- hence a fully
// owned list where rows are flat and full-bleed by construction.
//
// Solid 2.0 notes:
//   - The outside-dismiss listeners attach only while open, via a
//     compute/apply createEffect (ThemeDropdown shape).
//   - toggle() and close() read the openNow mirror, never open(): a signal
//     read cannot see its own write in the same tick (batched), so anything
//     landing in the tick of a write sees the pre-write value -- a toggle
//     would re-open the list that just closed, and a second dismissal would
//     re-focus the trigger after focus had legitimately moved on.
//   - Props are read as accessors (p.value), never destructured.
//   - Focus moves into the menu one microtask after open: Solid runs element
//     refs while the node is still detached, so focusing in the ref no-ops.
//   - Keyboard ownership: arrows/Home/End/type-ahead chars are consumed here
//     with stopPropagation, or the reader's window-level shortcuts (page
//     turns, panel toggles) fire underneath the open menu. Enter/Space need
//     no handling -- native buttons activate on them, and neither is a reader
//     shortcut on buttons. Escape closes the menu (not the panel): the
//     SettingsPanel root also listens for Escape, so it must not bubble.
import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onSettled,
  Show,
} from "solid-js";
import Icon from "~/lib/Icon";
import { Check, ChevronDown } from "~/lib/icons";

export interface DropSelectOption {
  value: string;
  label: string;
}

export interface DropSelectGroup {
  /** Empty label renders no header (a lone ungrouped option list). */
  label: string;
  options: DropSelectOption[];
}

interface DropSelectProps {
  /** Trigger id (replaces the native select id for label/test hooks). */
  id: string;
  /** Accessible name for the trigger. Optional when a wrapping label names it. */
  label?: string;
  value: string;
  groups: DropSelectGroup[];
  disabled?: boolean;
  /** Compact trigger for tight rows (the per-role file pickers). */
  compact?: boolean;
  onSelect: (value: string) => void;
}

// Type-ahead reset: a pause longer than this starts a fresh search.
const TYPEAHEAD_TIMEOUT_MS = 800;

/**
 * Whether the menu should open upward. Pure so the suites can pin the
 * trade-off directly: flip only when the menu does not fit below AND fits
 * better above, so a tall menu in a short panel keeps the downward anchor
 * instead of clipping both ways.
 */
export function shouldDropUp(
  below: number,
  above: number,
  menuHeight: number,
): boolean {
  return menuHeight > below && above > below;
}

export default function DropSelect(p: DropSelectProps) {
  const [open, setOpen] = createSignal(false);
  let trigger: HTMLButtonElement | undefined;
  let menuEl: HTMLElement | undefined;
  let typeahead = "";
  let typeaheadTimer: ReturnType<typeof setTimeout> | undefined;
  // Plain mirror of open() for the dismissal guard: a signal read cannot see
  // its own write in the same tick, so every write goes through setOpenState
  // and guards read openNow.
  let openNow = false;
  function setOpenState(next: boolean): void {
    openNow = next;
    setOpen(next);
  }
  // Teardown returned from onSettled: 2.0 keeps component teardown paired
  // with setup here and reserves onCleanup for custom-primitive internals.
  onSettled(() => () => clearTimeout(typeaheadTimer));

  // Flat option list in DOM order for label lookup, keyboard walk, and
  // type-ahead. Memos over props read fresh each render; the groups array is
  // rebuilt by the caller per render, so no stale cache can survive a rescan.
  const flat = createMemo(() => p.groups.flatMap((g) => g.options));
  const currentLabel = createMemo(
    () => flat().find((o) => o.value === p.value)?.label ?? p.value,
  );

  function toggle(): void {
    if (p.disabled) return;
    // The mirror, not open(): the batching described above would hand a toggle
    // that lands in the tick of a dismissal the pre-write value, re-opening
    // the list that just closed.
    setOpenState(!openNow);
  }
  function close(restoreFocus = true): void {
    // A dismissal that lands after the menu already closed must not pull
    // focus back off whatever legitimately took it.
    if (!openNow) return;
    setOpenState(false);
    clearTimeout(typeaheadTimer);
    typeahead = "";
    if (restoreFocus) trigger?.focus();
  }
  function choose(value: string): void {
    p.onSelect(value);
    close();
  }

  // A disabled flip collapses the menu through the same mirror the toggle
  // reads. <Show> hides the listbox on disabled while open()/openNow stay
  // true, which would leave the trigger reporting aria-expanded="true" with
  // no listbox -- and the first toggle after re-enable would close the
  // (already hidden) menu instead of opening it. No focus restore: a
  // programmatic disable must not yank focus, and the menu unmounting drops
  // it exactly as an outside dismissal does.
  createEffect(
    () => p.disabled,
    (disabled) => {
      if (disabled) close(false);
      return undefined;
    },
  );

  // Dismiss on outside pointerdown. No fixed scrim: containing blocks in the
  // panel subtree would clip it, so a window listener is container-proof
  // (ThemeDropdown/ProfileMenu shape).
  function onOutside(e: PointerEvent): void {
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (menuEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }

  // Bubble phase, deliberately: overlays stacked above this menu register
  // capture listeners that stopImmediatePropagation, so an Escape belonging
  // to a surface on top never reaches here (ThemeDropdown split).
  function onWindowKeyDown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    close();
  }

  // Dismiss when focus leaves the dropdown entirely. A null relatedTarget
  // (window blur, devtools) must NOT close, or an alt-tab would drop it.
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
      window.addEventListener("pointerdown", onOutside);
      window.addEventListener("keydown", onWindowKeyDown);
      return () => {
        window.removeEventListener("pointerdown", onOutside);
        window.removeEventListener("keydown", onWindowKeyDown);
      };
    },
  );

  // Entry focus for the menu: the active option, else the first. Shared by
  // the open effect's microtask and the trigger's arrow keys.
  function focusMenuEntry(): void {
    const el = menuEl;
    if (!el) return;
    const items = Array.from(
      el.querySelectorAll<HTMLButtonElement>(".ds-pick"),
    );
    const preferred = items.find((it) => it.getAttribute("tabindex") === "0");
    (preferred ?? items[0] ?? el).focus();
  }

  // Upward-open state for triggers near the bottom of the scrolling settings
  // panel, whose own overflow would clip a downward menu. Measured on open
  // (in the same microtask as entry focus, when layout is ready): the menu
  // scrolls with its trigger afterwards, so no re-measure on scroll.
  const [dropUp, setDropUp] = createSignal(false);
  function measureDropUp(): boolean {
    const menu = menuEl;
    const bar = trigger;
    if (!menu || !bar) return false;
    // Nearest scrolling ancestor is what clips the absolutely positioned
    // menu; without one the viewport is the bound.
    let node: HTMLElement | null = menu.parentElement;
    let clip: DOMRect | null = null;
    while (node !== null) {
      const overflowY = getComputedStyle(node).overflowY;
      if (overflowY === "auto" || overflowY === "scroll") {
        clip = node.getBoundingClientRect();
        break;
      }
      node = node.parentElement;
    }
    const menuHeight = menu.getBoundingClientRect().height;
    const triggerRect = bar.getBoundingClientRect();
    const below = (clip?.bottom ?? window.innerHeight) - triggerRect.bottom;
    const above = triggerRect.top - (clip?.top ?? 0);
    return shouldDropUp(below, above, menuHeight);
  }

  // Move focus into the menu on open, onto the active option (or the first
  // when the value matches nothing). One microtask after open, matching
  // ThemeDropdown/ProfileMenu; the tabindex="0" option is the entry point
  // the markup nominates.
  let menuGen = 0;
  createEffect(
    () => open(),
    (isOpen) => {
      const gen = ++menuGen;
      if (!isOpen) return undefined;
      queueMicrotask(() => {
        if (gen !== menuGen) return;
        setDropUp(measureDropUp());
        focusMenuEntry();
      });
      return undefined;
    },
  );

  function options(): HTMLButtonElement[] {
    const el = menuEl;
    if (!el) return [];
    return Array.from(el.querySelectorAll<HTMLButtonElement>(".ds-pick"));
  }

  function focusByIndex(index: number): void {
    const items = options();
    if (index < 0 || index >= items.length) return;
    items[index].focus();
  }

  // Native-select parity for long lists: typing jumps to the first label
  // with the typed prefix; repeating one character cycles its matches.
  function typeAhead(key: string): boolean {
    const items = options();
    if (items.length === 0) return false;
    clearTimeout(typeaheadTimer);
    typeaheadTimer = setTimeout(() => {
      typeahead = "";
    }, TYPEAHEAD_TIMEOUT_MS);
    const char = key.toLowerCase();
    const first = char.charAt(0);
    const repeated =
      typeahead.length > 0 && typeahead.split("").every((c) => c === first);
    typeahead = repeated ? first : typeahead + char;
    const labels = items.map((it) =>
      (it.textContent ?? "").trim().toLowerCase(),
    );
    let at: number;
    if (repeated) {
      const active = document.activeElement;
      const cur =
        active instanceof HTMLButtonElement ? items.indexOf(active) : -1;
      at = labels.findIndex(
        (label, i) => i > cur && label.startsWith(typeahead),
      );
      if (at < 0) at = labels.findIndex((label) => label.startsWith(typeahead));
    } else {
      at = labels.findIndex((label) => label.startsWith(typeahead));
    }
    if (at < 0) return false;
    focusByIndex(at);
    return true;
  }

  function onKeydown(e: KeyboardEvent): void {
    // IME composition owns Escape (and the keystrokes forming it): never
    // dismiss or consume while composing.
    if (e.isComposing) return;
    if (e.key === "Escape") {
      // The SettingsPanel root also closes on Escape: stop this one here so
      // it dismisses the menu, not the whole panel.
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    // Tab leaves the menu (APG Listbox Button): close and let the browser
    // move focus onward.
    if (e.key === "Tab") {
      close();
      return;
    }
    if (
      e.key === "ArrowDown" ||
      e.key === "ArrowUp" ||
      e.key === "Home" ||
      e.key === "End"
    ) {
      // Reader shortcuts own these keys at window level (page turns in paged
      // mode); consume them here or turning pages happens under the menu.
      e.preventDefault();
      e.stopPropagation();
      const items = options();
      if (items.length === 0) return;
      const active = document.activeElement;
      const cur =
        active instanceof HTMLButtonElement ? items.indexOf(active) : -1;
      switch (e.key) {
        case "Home":
          focusByIndex(0);
          break;
        case "End":
          focusByIndex(items.length - 1);
          break;
        case "ArrowDown":
          focusByIndex(cur < 0 ? 0 : (cur + 1) % items.length);
          break;
        default:
          focusByIndex(
            cur < 0
              ? items.length - 1
              : (cur - 1 + items.length) % items.length,
          );
          break;
      }
      return;
    }
    // Single printable characters feed type-ahead. Consumed whether or not
    // one matches: an unmatched letter is still menu-owned, and letting it
    // bubble would fire a letter shortcut (f/s/t/b) underneath the open menu.
    if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      typeAhead(e.key);
      e.preventDefault();
      e.stopPropagation();
    }
  }

  // Group ids must be unique per instance (two font selects share labels):
  // derive from the trigger id the caller passes.
  const groupId = (gi: number): string => `${p.id}-grp-${gi}`;

  // The collapsed trigger owns its navigation keys. A closed trigger is still
  // a plain button, so the shared keyboard contract leaves arrows to the
  // reader shortcuts -- ArrowDown on a focused trigger would turn the page
  // instead of opening the list. Open (entry focus lands through the effect
  // above) and consume, matching the open menu's ownership. A disabled
  // trigger refuses like toggle() does, without consuming.
  function onTriggerKeydown(e: KeyboardEvent): void {
    if (e.isComposing || p.disabled) return;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
      case "Home":
      case "End":
        e.preventDefault();
        e.stopPropagation();
        if (!openNow) setOpenState(true);
        else focusMenuEntry();
        break;
      default:
        break;
    }
  }

  return (
    <div
      class={["ds-root", { "ds-compact": p.compact === true }]}
      onFocusOut={onRootFocusOut}
    >
      {/* aria-disabled, not disabled: these triggers live inside panels that
          trap focus, and a real attribute blurs the trigger the moment the
          mode flips under it, dropping focus to body. toggle() refuses. */}
      <button
        ref={(el) => (trigger = el)}
        id={p.id}
        type="button"
        class={["ds-trigger", { open: open() }]}
        aria-haspopup="listbox"
        aria-expanded={open() ? "true" : "false"}
        aria-label={p.label}
        aria-disabled={p.disabled ? "true" : "false"}
        onClick={toggle}
        onKeyDown={onTriggerKeydown}
      >
        <span class="ds-value">{currentLabel()}</span>
        <Icon icon={ChevronDown} size={14} class="ds-caret" decorative />
      </button>

      <Show when={open() && !p.disabled}>
        <div
          ref={(el) => (menuEl = el)}
          class={["ds-menu paper", { "ds-up": dropUp() }]}
          role="listbox"
          tabindex="-1"
          aria-label={p.label ?? currentLabel()}
          onKeyDown={onKeydown}
        >
          <For each={p.groups}>
            {(group, gi) => (
              <>
                {/* A listbox owns options and groups only, so this heading
                    stays out of the accessibility tree. The group below
                    names itself from it, and a direct aria-labelledby
                    reference still reads hidden text. */}
                <Show when={group.label !== ""}>
                  <p
                    class="ds-group eyebrow"
                    id={groupId(gi())}
                    aria-hidden="true"
                  >
                    {group.label}
                  </p>
                </Show>
                <div
                  role="group"
                  aria-labelledby={
                    group.label !== "" ? groupId(gi()) : undefined
                  }
                >
                  <For each={group.options}>
                    {(o) => {
                      const active = () => o.value === p.value;
                      return (
                        <button
                          type="button"
                          class={["ds-pick", { active: active() }]}
                          role="option"
                          aria-selected={active() ? "true" : "false"}
                          tabindex={active() ? "0" : "-1"}
                          title={o.label}
                          onClick={() => choose(o.value)}
                        >
                          <span class="ds-label">{o.label}</span>
                          <Show when={active()}>
                            <span class="ds-check" aria-hidden="true">
                              <Icon icon={Check} size={12} decorative />
                            </span>
                          </Show>
                        </button>
                      );
                    }}
                  </For>
                </div>
              </>
            )}
          </For>
        </div>
      </Show>
    </div>
  );
}
