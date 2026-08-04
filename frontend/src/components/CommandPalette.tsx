// Fuzzy command palette (Ctrl+K / Cmd+K): navigation, actions, books, and
// theme switching in one list. Ported from CommandPalette.svelte.
//
// Solid 2.0 notes:
//   - The open/reset $effect becomes a compute/apply createEffect pair. The
//     Svelte version needed an explicit untrack() around the lazy loads; the
//     apply phase here never tracks, which is exactly that guarantee.
//   - Arrow-key stepping computes `next` in a local before setActive: reading
//     active() right after writing would still return the pre-write value
//     (batched), and scrollActiveIntoView would chase the previous row.
//   - {@attach focusTrap} -> ref={trap()} (two-phase factory — beta.29 ref callbacks are unowned, so the old ref + onCleanup(...) form never tore the trap down); bind:this ->
//     ref callbacks; bind:value -> value={query()} + onInput.
//   - The backdrop dismiss uses the shared untabbable-button pattern
//     (.backdrop-dismiss) instead of the Svelte's stopPropagation-on-the-sheet
//     trick, which jsx-a11y rejects.
import { createEffect, createMemo, createSignal, For, Show } from "solid-js";
import { ui } from "~/lib/ui";
import { library } from "~/lib/library";
import { settings } from "~/lib/settings";
import { router } from "~/lib/router";
import { session } from "~/lib/session";
import { applyTheme } from "~/lib/theme";
import { THEMES } from "~/lib/themes";
import { customThemes } from "~/lib/customThemes";
import Icon from "~/lib/Icon";
import { Search } from "~/lib/icons";
import { trap } from "~/lib/focusTrap";

interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
  /** Precomputed lowercased "label + hint" so filtering avoids per-key allocs. */
  haystack: string;
}

function close(): void {
  ui.closeOverlays();
}

function choose(cmd: Command | undefined): void {
  if (!cmd) return;
  close();
  cmd.run();
}

// Escape closes the palette from anywhere, via a window CAPTURE listener
// (attached only while open): capture runs before the input's own handler
// and before App's/Read's bubble listeners, so registration order can't
// strand an Esc in the reader (the ShortcutsHelp pattern).
function onPaletteEscape(e: KeyboardEvent): void {
  if (e.key !== "Escape") return;
  e.preventDefault();
  e.stopImmediatePropagation();
  close();
}

export default function CommandPalette() {
  const [query, setQuery] = createSignal("");
  const [active, setActive] = createSignal(0);
  let input: HTMLInputElement | undefined;
  let listEl: HTMLUListElement | undefined;

  // Only build the (potentially large) command list while the palette is open.
  const commands = createMemo<Command[]>(() => {
    if (!ui.palette) return [];
    const list: Omit<Command, "haystack">[] = [
      {
        id: "nav-library",
        label: "Go to Library",
        hint: "Navigate",
        run: () => router.navigate("/"),
      },
      {
        id: "act-rescan",
        label: "Rescan library folder",
        hint: "Action",
        run: () => void library.rescan(),
      },
      {
        id: "act-shortcuts",
        label: "Keyboard shortcuts",
        hint: "Help",
        run: () => ui.openShortcuts(),
      },
      {
        id: "act-signout",
        label: "Sign out",
        hint: "Account",
        run: () => void session.logout(),
      },
    ];
    for (const b of library.books) {
      list.push({
        id: `book-${b.id}`,
        label: b.title,
        hint: b.author || "Open book",
        run: () => router.navigate(`/read/${encodeURIComponent(b.id)}`),
      });
    }
    const pushTheme = (t: (typeof THEMES)[number], custom: boolean): void => {
      list.push({
        id: `theme-${t.id}`,
        label: `Theme: ${t.label}`,
        hint: `${custom ? "Custom · " : ""}${
          t.group === "dark" ? "Dark" : "Light"
        }`,
        run: () => {
          settings.update({ theme: t.id });
          applyTheme(t.id);
        },
      });
    };
    for (const t of THEMES) pushTheme(t, false);
    for (const t of customThemes.list) pushTheme(t, true);
    // A plain loop rather than .map((c) => ({ ...c, ... })): oxc flags
    // object spreads inside map callbacks (no-map-spread).
    const out: Command[] = [];
    for (const c of list) {
      out.push({
        ...c,
        haystack: (c.label + " " + (c.hint ?? "")).toLowerCase(),
      });
    }
    return out;
  });

  const filtered = createMemo<Command[]>(() => {
    const words = query().trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (words.length === 0) return commands().slice(0, 50);
    // Match every typed word somewhere in the label/hint (order-independent),
    // so "theme sepia" matches "Theme: Sepia".
    return commands()
      .filter((c) => words.every((w) => c.haystack.includes(w)))
      .slice(0, 50);
  });

  // Clamp the raw selection into range as the filtered set shrinks (computed,
  // not stored, so no effect is needed to keep it valid).
  const sel = createMemo(() => {
    const f = filtered();
    return f.length ? Math.min(active(), f.length - 1) : 0;
  });

  // Keep the highlighted option visible as the arrows walk past the viewport.
  function scrollActiveIntoView(i: number): void {
    queueMicrotask(() => {
      listEl
        ?.querySelector<HTMLElement>(`#cmd-opt-${i}`)
        ?.scrollIntoView({ block: "nearest" });
    });
  }

  function onKeydown(e: KeyboardEvent): void {
    switch (e.key) {
      case "ArrowDown": {
        e.preventDefault();
        // Step from the clamped `sel`, not raw `active`, so a shrunk filter
        // set can't make the first arrow skip a row.
        const next = filtered().length ? (sel() + 1) % filtered().length : 0;
        setActive(next);
        scrollActiveIntoView(next);
        break;
      }
      case "ArrowUp": {
        e.preventDefault();
        const next = filtered().length
          ? (sel() - 1 + filtered().length) % filtered().length
          : 0;
        setActive(next);
        scrollActiveIntoView(next);
        break;
      }
      case "Enter":
        e.preventDefault();
        choose(filtered()[sel()]);
        break;
    }
  }

  createEffect(
    () => ui.palette,
    (open) => {
      if (!open) return undefined;
      window.addEventListener("keydown", onPaletteEscape, true);
      return () => window.removeEventListener("keydown", onPaletteEscape, true);
    },
  );

  // Reset transient state whenever the palette opens; focus the input. Ensure
  // books are loaded so quick-open works even when deep-linked into the reader.
  createEffect(
    () => ui.palette,
    (open) => {
      if (!open) return undefined;
      setQuery("");
      setActive(0);
      // Trigger lazy/retry loads. The apply phase never tracks, so arriving
      // data can't re-run this effect and wipe the user's query -- the job
      // Svelte's explicit untrack() block did. Both stores dedupe
      // current-profile requests.
      void library.loadForProfile(session.profile);
      void customThemes.load();
      queueMicrotask(() => input?.focus());
      return undefined;
    },
  );

  return (
    <Show when={ui.palette}>
      <div class="cmd-overlay" role="presentation">
        <button
          type="button"
          class="backdrop-dismiss"
          aria-label="Close"
          tabindex="-1"
          onClick={close}
        />
        {/* a11y suppressions, justified: div+role kept over a native <dialog>
            for visual parity with the Svelte original; the listbox/option rows
            are the WAI combobox pattern -- keyboard interaction lives on the
            combobox input via aria-activedescendant, not on the rows (mirrors
            the Svelte a11y ignores). */}
        {/* eslint-disable jsx-a11y/prefer-tag-over-role, jsx-a11y/no-noninteractive-element-to-interactive-role, jsx-a11y/click-events-have-key-events */}
        <div
          class="cmd-palette paper"
          role="dialog"
          tabindex="-1"
          aria-modal="true"
          aria-label="Command palette"
          ref={trap()}
        >
          <div class="cmd-search">
            <Icon icon={Search} size={18} class="cmd-search-icon" />
            <input
              ref={(el) => (input = el)}
              class="cmd-input"
              type="text"
              role="combobox"
              placeholder="Type a command, book, or theme…"
              aria-label="Command palette search"
              aria-controls="cmd-list"
              aria-autocomplete="list"
              aria-expanded="true"
              aria-activedescendant={
                filtered().length ? `cmd-opt-${sel()}` : undefined
              }
              autocomplete="off"
              spellcheck="false"
              value={query()}
              onInput={(e) => {
                setQuery(e.currentTarget.value);
                setActive(0);
                scrollActiveIntoView(0);
              }}
              onKeyDown={onKeydown}
            />
          </div>
          <ul
            class="cmd-list"
            id="cmd-list"
            role="listbox"
            aria-label="Commands"
            ref={(el) => (listEl = el)}
          >
            <For
              each={filtered()}
              fallback={
                <li class="cmd-empty" role="presentation">
                  No matches
                </li>
              }
            >
              {(cmd, i) => (
                <li
                  id={`cmd-opt-${i()}`}
                  class={["cmd", { active: i() === sel() }]}
                  role="option"
                  aria-selected={i() === sel() ? "true" : "false"}
                  onMouseEnter={() => setActive(i())}
                  onClick={() => choose(cmd)}
                >
                  <span class="cmd-label">{cmd.label}</span>
                  <Show when={cmd.hint}>
                    <span class="cmd-hint">{cmd.hint}</span>
                  </Show>
                </li>
              )}
            </For>
          </ul>
          <footer class="cmd-foot" aria-hidden="true">
            <span>
              <kbd class="kbd">↑</kbd>
              <kbd class="kbd">↓</kbd> navigate
            </span>
            <span>
              <kbd class="kbd">↵</kbd> open
            </span>
            <span>
              <kbd class="kbd">esc</kbd> close
            </span>
          </footer>
        </div>
        {/* eslint-enable jsx-a11y/prefer-tag-over-role, jsx-a11y/no-noninteractive-element-to-interactive-role, jsx-a11y/click-events-have-key-events */}
      </div>
    </Show>
  );
}
