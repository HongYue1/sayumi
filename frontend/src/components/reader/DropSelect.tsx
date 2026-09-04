// DropSelect: a custom single-select dropdown (button trigger + listbox
// popup) replacing the native <select> elements in the reader settings.
// Native popups render each option as its own rounded pill under a dark
// color-scheme, which no author CSS can reliably flatten -- hence a fully
// owned list where rows are flat and full-bleed by construction.
//
// Solid 2.0 notes:
//   - The outside-dismiss listeners attach only while open, via a
//     compute/apply createEffect (ThemeDropdown shape).
//   - toggle() computes `next` once: reading open() right after setOpen would
//     still return the pre-write value (batched).
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
  onCleanup,
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

export default function DropSelect(p: DropSelectProps) {
  const [open, setOpen] = createSignal(false);
  let trigger: HTMLButtonElement | undefined;
  let menuEl: HTMLElement | undefined;
  let typeahead = "";
  let typeaheadTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(typeaheadTimer));

  // Flat option list in DOM order for label lookup, keyboard walk, and
  // type-ahead. Memos over props read fresh each render; the groups array is
  // rebuilt by the caller per render, so no stale cache can survive a rescan.
  const flat = createMemo(() => p.groups.flatMap((g) => g.options));
  const currentLabel = createMemo(
    () => flat().find((o) => o.value === p.value)?.label ?? p.value,
  );

  function toggle(): void {
    if (p.disabled) return;
    setOpen(!open());
  }
  function close(restoreFocus = true): void {
    setOpen(false);
    clearTimeout(typeaheadTimer);
    typeahead = "";
    if (restoreFocus) trigger?.focus();
  }
  function choose(value: string): void {
    p.onSelect(value);
    close();
  }

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
        const el = menuEl;
        if (!el) return;
        const items = Array.from(
          el.querySelectorAll<HTMLButtonElement>(".ds-pick"),
        );
        const preferred = items.find(
          (it) => it.getAttribute("tabindex") === "0",
        );
        (preferred ?? items[0] ?? el).focus();
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
    // Single printable characters feed type-ahead. Consumed for the same
    // reason as arrows: letter shortcuts (f/s/t/b) must not fire underneath.
    if (
      e.key.length === 1 &&
      !e.ctrlKey &&
      !e.metaKey &&
      !e.altKey &&
      typeAhead(e.key)
    ) {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  // Group ids must be unique per instance (two font selects share labels):
  // derive from the trigger id the caller passes.
  const groupId = (gi: number): string => `${p.id}-grp-${gi}`;

  return (
    <div
      class={["ds-root", { "ds-compact": p.compact === true }]}
      onFocusOut={onRootFocusOut}
    >
      <button
        ref={(el) => (trigger = el)}
        id={p.id}
        type="button"
        class={["ds-trigger", { open: open() }]}
        aria-haspopup="listbox"
        aria-expanded={open() ? "true" : "false"}
        aria-label={p.label}
        disabled={p.disabled}
        onClick={toggle}
      >
        <span class="ds-value">{currentLabel()}</span>
        <Icon icon={ChevronDown} size={14} class="ds-caret" decorative />
      </button>

      <Show when={open() && !p.disabled}>
        <div
          ref={(el) => (menuEl = el)}
          class="ds-menu paper"
          role="listbox"
          tabindex="-1"
          aria-label={p.label ?? currentLabel()}
          onKeyDown={onKeydown}
        >
          <For each={p.groups}>
            {(group, gi) => (
              <>
                <Show when={group.label !== ""}>
                  <p class="ds-group eyebrow" id={groupId(gi())}>
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
