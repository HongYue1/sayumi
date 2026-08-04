// Theme selector in the library masthead: light and dark built-ins as
// swatches, custom themes resolved through the registry. Ported from
// ThemeDropdown.svelte.
//
// Solid 2.0 notes:
//   - The conditional <svelte:window onpointerdown> becomes a compute/apply
//     createEffect that attaches the outside-dismiss listener only while open.
//   - toggle() computes `next` once: reading open() right after setOpen would
//     still return the pre-write value (batched).
//   - The retry promise chain is an async function instead of a .then
//     callback, so promise/always-return has nothing to lint (the App.tsx
//     precedent).
//   - {@attach ...focus()} per swatch -> ref callbacks; bind:this -> ref
//     assignments; no `as` casts (instanceof narrowing instead).
//   - Class names get a .td- prefix: .trigger/.pick/.caret are shared by
//     convention across the library subtree's scoped styles.
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { settings } from "~/lib/settings";
import { THEMES } from "~/lib/themes";
import { applyTheme, getTheme } from "~/lib/theme";
import { customThemes } from "~/lib/customThemes";
import Icon from "~/lib/Icon";
import { Check, ChevronDown } from "~/lib/icons";

const lightThemes = THEMES.filter((t) => t.group === "light");
const darkThemes = THEMES.filter((t) => t.group === "dark");

// The retry promise chain lives in an async function, not a .then callback,
// so promise/always-return has nothing to lint and the await point is
// explicit (the App.tsx precedent).
async function retryThemeLoad(): Promise<void> {
  await customThemes.load();
  if (customThemes.loaded) applyTheme(settings.value.theme);
}

export default function ThemeDropdown() {
  const [open, setOpen] = createSignal(false);
  let trigger: HTMLButtonElement | undefined;
  let menuEl: HTMLElement | undefined;

  // Read through the reactive profile list before the global resolver so a
  // custom-theme load/retry refreshes the trigger even when the saved theme
  // id itself did not change.
  const current = createMemo(
    () =>
      customThemes.get(settings.value.theme) ?? getTheme(settings.value.theme),
  );
  // When the active theme is a CUSTOM one, no built-in swatch is active — and
  // with every item at tabindex -1 the menu would be a keyboard dead-end (no
  // initial focus, arrows/Home/End find activeElement outside the menu). Fall
  // back to treating the first light swatch as the roving-focus entry point.
  const hasBuiltInActive = createMemo(() =>
    THEMES.some((t) => t.id === settings.value.theme),
  );

  function toggle(): void {
    const next = !open();
    setOpen(next);
    // App boot normally loads the profile registry. If that non-fatal request
    // failed, opening a theme selector is an explicit, bounded retry point.
    if (next && !customThemes.loaded) void retryThemeLoad();
  }
  function close(restoreFocus = true): void {
    setOpen(false);
    if (restoreFocus) trigger?.focus();
  }
  function choose(id: string): void {
    settings.update({ theme: id });
    applyTheme(id);
    close();
  }

  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the
  // sticky masthead's backdrop-filter establishes a containing block, which
  // clips a position:fixed scrim to the masthead box (so clicks on the shelf
  // below would never reach it). A window listener is container-proof,
  // matching ProfileMenu.
  function onOutside(e: PointerEvent): void {
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (menuEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }

  createEffect(
    () => open(),
    (isOpen) => {
      if (!isOpen) return undefined;
      window.addEventListener("pointerdown", onOutside);
      return () => window.removeEventListener("pointerdown", onOutside);
    },
  );

  function onKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End"
    ) {
      return;
    }
    // Roving focus across all swatches (light + dark) so the menu role's
    // keyboard model works, matching the flair menu in BookCard.
    const items = Array.from(
      e.currentTarget.querySelectorAll<HTMLButtonElement>(".td-pick"),
    );
    if (items.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const active = document.activeElement;
    const cur =
      active instanceof HTMLButtonElement ? items.indexOf(active) : -1;
    let next: number;
    switch (e.key) {
      case "Home":
        next = 0;
        break;
      case "End":
        next = items.length - 1;
        break;
      case "ArrowDown":
        next = cur < 0 ? 0 : (cur + 1) % items.length;
        break;
      default:
        next =
          cur < 0 ? items.length - 1 : (cur - 1 + items.length) % items.length;
    }
    items[next].focus();
  }

  return (
    <div class="theme-dd">
      <button
        ref={(el) => (trigger = el)}
        class={["td-trigger", { open: open() }]}
        aria-haspopup="menu"
        aria-expanded={open() ? "true" : "false"}
        aria-label={`Change theme (current: ${current().label})`}
        onClick={toggle}
      >
        <span
          class="td-swatch"
          style={{ background: current().bg, color: current().fg }}
          aria-hidden="true"
        >
          <span class="td-aa">Aa</span>
          <span class="td-dot" style={{ background: current().accent }} />
        </span>
        <Icon icon={ChevronDown} size={14} class="td-caret" />
      </button>

      <Show when={open()}>
        <div
          ref={(el) => (menuEl = el)}
          class="td-menu paper"
          role="menu"
          tabindex="-1"
          aria-label="Theme"
          onKeyDown={onKeydown}
        >
          <p class="td-group eyebrow" id="theme-grp-light">
            Light
          </p>
          <fieldset class="td-swatches" aria-labelledby="theme-grp-light">
            <For each={lightThemes}>
              {(t, i) => {
                const active = () => settings.value.theme === t.id;
                const focusEntry = () =>
                  active() || (i() === 0 && !hasBuiltInActive());
                return (
                  <button
                    class={["td-pick", { active: active() }]}
                    role="menuitemradio"
                    aria-checked={active() ? "true" : "false"}
                    tabindex={focusEntry() ? "0" : "-1"}
                    title={t.label}
                    aria-label={t.label}
                    onClick={() => choose(t.id)}
                    ref={(el) => {
                      // Focus the currently-selected theme on open
                      // (menuitemradio model), or the first swatch when a
                      // custom theme is active.
                      if (focusEntry()) el.focus();
                    }}
                  >
                    <span
                      class="td-preview"
                      style={{ background: t.bg, color: t.fg }}
                    >
                      <span class="td-aa">Aa</span>
                      <span class="td-dot" style={{ background: t.accent }} />
                      <Show when={active()}>
                        <span class="td-check" aria-hidden="true">
                          <Icon icon={Check} size={11} />
                        </span>
                      </Show>
                    </span>
                    <span class="td-name">{t.label}</span>
                  </button>
                );
              }}
            </For>
          </fieldset>
          <p class="td-group eyebrow" id="theme-grp-dark">
            Dark
          </p>
          <fieldset class="td-swatches" aria-labelledby="theme-grp-dark">
            <For each={darkThemes}>
              {(t) => {
                const active = () => settings.value.theme === t.id;
                return (
                  <button
                    class={["td-pick", { active: active() }]}
                    role="menuitemradio"
                    aria-checked={active() ? "true" : "false"}
                    tabindex={active() ? "0" : "-1"}
                    title={t.label}
                    aria-label={t.label}
                    onClick={() => choose(t.id)}
                    ref={(el) => {
                      if (active()) el.focus();
                    }}
                  >
                    <span
                      class="td-preview"
                      style={{ background: t.bg, color: t.fg }}
                    >
                      <span class="td-aa">Aa</span>
                      <span class="td-dot" style={{ background: t.accent }} />
                      <Show when={active()}>
                        <span class="td-check" aria-hidden="true">
                          <Icon icon={Check} size={11} />
                        </span>
                      </Show>
                    </span>
                    <span class="td-name">{t.label}</span>
                  </button>
                );
              }}
            </For>
          </fieldset>
        </div>
      </Show>
    </div>
  );
}
