// Theme selector in the library masthead: light and dark themes as swatches,
// the built-ins plus the profile's own custom themes.
//
// Solid 2.0 notes:
//   - The outside-dismiss window listener attaches only while the menu is
//     open, via a compute/apply createEffect.
//   - toggle() computes `next` once: reading open() right after setOpen would
//     still return the pre-write value (batched).
//   - No `as` casts: event targets are narrowed with instanceof.
//   - Class names get a .td- prefix: .trigger/.pick/.caret are shared by
//     convention across the library subtree and would collide in the one
//     global sheet.
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { settings } from "~/lib/settings";
import { THEMES, getTheme } from "~/lib/themes";
import { customThemes } from "~/lib/customThemes";
import Icon from "~/lib/Icon";
import { Check, ChevronDown } from "~/lib/icons";

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
  // Built-ins plus the profile's custom themes, split by group -- the same
  // derivation the reader's SettingsPanel uses, so a theme created in the
  // reader is offered here too.
  //
  // Memos, not module-level constants: customThemes.list is reactive and starts
  // empty, so a module-level filter over THEMES alone could never grow. That is
  // why a saved custom theme was resolvable by the trigger (which reads the
  // registry) yet had no row to pick it from in this menu. Same derivation the
  // reader's SettingsPanel uses, so a theme created there is offered here too.
  const lightThemes = createMemo(() => [
    ...THEMES.filter((t) => t.group === "light"),
    ...customThemes.list.filter((t) => t.group === "light"),
  ]);
  const darkThemes = createMemo(() => [
    ...THEMES.filter((t) => t.group === "dark"),
    ...customThemes.list.filter((t) => t.group === "dark"),
  ]);

  // With every item at tabindex -1 the menu would be a keyboard dead-end (no
  // initial focus, and arrows/Home/End find activeElement outside the menu).
  // That is now only reachable when the saved id matches nothing on offer -- a
  // theme deleted elsewhere, or customs that have not loaded yet -- so fall
  // back to treating the first light swatch as the roving-focus entry point.
  const hasActive = createMemo(() =>
    [...lightThemes(), ...darkThemes()].some(
      (t) => t.id === settings.value.theme,
    ),
  );

  function toggle(): void {
    const next = !open();
    setOpen(next);
    // App boot normally loads the profile registry. If that non-fatal request
    // failed, opening a theme selector is an explicit, bounded retry point.
    if (next && !customThemes.loaded) void customThemes.load();
  }
  function close(restoreFocus = true): void {
    setOpen(false);
    if (restoreFocus) trigger?.focus();
  }
  function choose(id: string): void {
    settings.update({ theme: id });
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

  // Escape from wherever focus happens to be while the menu is open. Bubble
  // phase, deliberately: every overlay that can stack above this menu
  // (.cmd-overlay, .shortcuts-overlay, .sd-overlay, .eb-overlay, .pd-overlay,
  // all z-index 60) registers a CAPTURE keydown listener that calls
  // stopImmediatePropagation, so an Escape belonging to a surface stacked on
  // top never reaches here. Menus bubble, dialogs capture -- the same split
  // as BookCard and ProfileMenu.
  function onWindowKeyDown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    close();
  }

  // Dismiss when focus leaves the dropdown entirely. relatedTarget is the
  // element about to receive focus; a null relatedTarget means focus is
  // leaving the document (window blur, devtools) and must NOT close the menu,
  // or an alt-tab would drop it. No focus restore here -- focus is
  // deliberately going somewhere else.
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

  // Move focus into the menu on open. The swatches' self-focusing refs could
  // never do it: Solid runs element refs while the node is still detached, so
  // .focus() no-oped and the active element stayed on the trigger -- which
  // left the roving arrow keys below unreachable (they listen on the menu,
  // and key events never bubble UP to it) and made aria-expanded assert a
  // focus move that never happened. One microtask after open, matching
  // ProfileMenu; the tabindex="0" swatch -- the active theme, or the first
  // light swatch when a custom theme is active -- is the entry point the
  // markup nominates.
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
          el.querySelectorAll<HTMLButtonElement>(".td-pick"),
        );
        const preferred = items.find(
          (it) => it.getAttribute("tabindex") === "0",
        );
        (preferred ?? items[0] ?? el).focus();
      });
      return undefined;
    },
  );

  function onKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    // An IME uses Escape to abandon a composition; that Escape is not a
    // dismissal (the same guard as ProfileMenu and the dialogs).
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    // Tab leaves the menu (WCAG 2.1.2 / APG Menu Button): menus must not
    // trap Tab, so close and let the browser move focus to whatever follows.
    if (e.key === "Tab") {
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
    <div class="theme-dd" onFocusOut={onRootFocusOut}>
      <button
        ref={(el) => (trigger = el)}
        id="td-trigger"
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
        <Icon icon={ChevronDown} size={14} class="td-caret" labelFromParent />
      </button>

      <Show when={open()}>
        <div
          ref={(el) => (menuEl = el)}
          class="td-menu paper"
          role="menu"
          tabindex="-1"
          aria-labelledby="td-trigger"
          onKeyDown={onKeydown}
        >
          <p class="td-group eyebrow" id="theme-grp-light">
            Light
          </p>
          <fieldset class="td-swatches" aria-labelledby="theme-grp-light">
            <For each={lightThemes()}>
              {(t, i) => {
                const active = () => settings.value.theme === t.id;
                const focusEntry = () =>
                  active() || (i() === 0 && !hasActive());
                return (
                  <button
                    class={["td-pick", { active: active() }]}
                    role="menuitemradio"
                    aria-checked={active() ? "true" : "false"}
                    tabindex={focusEntry() ? "0" : "-1"}
                    title={t.label}
                    aria-label={t.label}
                    onClick={() => choose(t.id)}
                  >
                    <span
                      class="td-preview"
                      style={{ background: t.bg, color: t.fg }}
                    >
                      <span class="td-aa">Aa</span>
                      <span class="td-dot" style={{ background: t.accent }} />
                      <Show when={active()}>
                        <span class="td-check" aria-hidden="true">
                          <Icon icon={Check} size={11} decorative />
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
            <For each={darkThemes()}>
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
                  >
                    <span
                      class="td-preview"
                      style={{ background: t.bg, color: t.fg }}
                    >
                      <span class="td-aa">Aa</span>
                      <span class="td-dot" style={{ background: t.accent }} />
                      <Show when={active()}>
                        <span class="td-check" aria-hidden="true">
                          <Icon icon={Check} size={11} decorative />
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
