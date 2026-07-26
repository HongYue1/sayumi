<script lang="ts">
  import { untrack } from "svelte";
  import { ui } from "~/lib/ui.svelte";
  import { library } from "~/lib/library.svelte";
  import { settings } from "~/lib/settings.svelte";
  import { router } from "~/lib/router.svelte";
  import { session } from "~/lib/session.svelte";
  import { applyTheme } from "~/lib/theme";
  import { THEMES } from "~/lib/themes";
  import { customThemes } from "~/lib/customThemes.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { Search } from "@lucide/svelte";
  import { focusTrap } from "~/lib/focusTrap";

  interface Command {
    id: string;
    label: string;
    hint?: string;
    run: () => void;
    /** Precomputed lowercased "label + hint" so filtering avoids per-key allocs. */
    haystack: string;
  }

  let query = $state("");
  let active = $state(0);
  let input = $state<HTMLInputElement | null>(null);
  let listEl = $state<HTMLUListElement | null>(null);

  // Only build the (potentially large) command list while the palette is open.
  const commands = $derived.by<Command[]>(() => {
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
    return list.map((c) => ({
      ...c,
      haystack: (c.label + " " + (c.hint ?? "")).toLowerCase(),
    }));
  });

  const filtered = $derived.by<Command[]>(() => {
    const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (words.length === 0) return commands.slice(0, 50);
    // Match every typed word somewhere in the label/hint (order-independent),
    // so "theme sepia" matches "Theme: Sepia".
    return commands
      .filter((c) => words.every((w) => c.haystack.includes(w)))
      .slice(0, 50);
  });

  // Clamp the raw selection into range as the filtered set shrinks (computed,
  // not stored, so no effect is needed to keep it valid).
  const sel = $derived(
    filtered.length ? Math.min(active, filtered.length - 1) : 0,
  );

  // Reset transient state whenever the palette opens; focus the input. Ensure
  // books are loaded so quick-open works even when deep-linked into the reader.
  $effect(() => {
    if (ui.palette) {
      query = "";
      active = 0;
      // Trigger lazy/retry loads without tracking their reactive state, or this
      // effect would re-run when data arrives and wipe the user's query. Both
      // stores dedupe current-profile requests.
      untrack(() => {
        void library.loadForProfile(session.profile);
        void customThemes.load();
      });
      queueMicrotask(() => input?.focus());
    }
  });

  function close(): void {
    ui.palette = false;
  }
  function choose(cmd: Command | undefined): void {
    if (!cmd) return;
    close();
    cmd.run();
  }
  // Keep the highlighted option visible as ↑/↓ walk past the viewport edge.
  function scrollActiveIntoView(i: number): void {
    queueMicrotask(() => {
      listEl
        ?.querySelector<HTMLElement>(`#cmd-opt-${i}`)
        ?.scrollIntoView({ block: "nearest" });
    });
  }
  function onInput(): void {
    active = 0;
    scrollActiveIntoView(0);
  }
  function onKeydown(e: KeyboardEvent): void {
    switch (e.key) {
      case "Escape":
        e.preventDefault();
        // Consume the event so it doesn't bubble to the reader's window key
        // handler, which would otherwise also act on this Esc and navigate back
        // to the library.
        e.stopPropagation();
        close();
        break;
      case "ArrowDown":
        e.preventDefault();
        // Step from the clamped `sel`, not raw `active`, so a shrunk filter
        // set can't make the first arrow skip a row.
        active = filtered.length ? (sel + 1) % filtered.length : 0;
        scrollActiveIntoView(active);
        break;
      case "ArrowUp":
        e.preventDefault();
        active = filtered.length
          ? (sel - 1 + filtered.length) % filtered.length
          : 0;
        scrollActiveIntoView(active);
        break;
      case "Enter":
        e.preventDefault();
        choose(filtered[sel]);
        break;
    }
  }
  // Escape must close the palette even when focus is NOT on the input (e.g.
  // after Tab moved it inside the dialog, or a click landed on the frame).
  // The input's own handler consumes Escape first; this is the dialog-level
  // fallback for everything else.
  function onDialogKeydown(e: KeyboardEvent): void {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    close();
  }
</script>

{#if ui.palette}
  <div class="overlay" role="presentation" onclick={close}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="palette paper"
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-label="Command palette"
      onclick={(e) => e.stopPropagation()}
      onkeydown={onDialogKeydown}
      {@attach focusTrap}
    >
      <div class="cmd-search">
        <Icon icon={Search} size={18} class="cmd-search-icon" />
        <input
          bind:this={input}
          class="cmd-input"
          type="text"
          role="combobox"
          placeholder="Type a command, book, or theme…"
          aria-label="Command palette search"
          aria-controls="cmd-list"
          aria-expanded="true"
          aria-activedescendant={filtered.length ? `cmd-opt-${sel}` : undefined}
          autocomplete="off"
          spellcheck="false"
          bind:value={query}
          oninput={onInput}
          onkeydown={onKeydown}
        />
      </div>
      <ul
        class="cmd-list"
        id="cmd-list"
        role="listbox"
        aria-label="Commands"
        bind:this={listEl}
      >
        {#each filtered as cmd, i (cmd.id)}
          <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_element_interactions -->
          <li
            id="cmd-opt-{i}"
            class="cmd"
            class:active={i === sel}
            role="option"
            aria-selected={i === sel}
            onmouseenter={() => (active = i)}
            onclick={() => choose(cmd)}
          >
            <span class="cmd-label">{cmd.label}</span>
            {#if cmd.hint}<span class="cmd-hint">{cmd.hint}</span>{/if}
          </li>
        {:else}
          <li class="cmd-empty" role="presentation">No matches</li>
        {/each}
      </ul>
      <footer class="cmd-foot" aria-hidden="true">
        <span><kbd class="kbd">↑</kbd><kbd class="kbd">↓</kbd> navigate</span>
        <span><kbd class="kbd">↵</kbd> open</span>
        <span><kbd class="kbd">esc</kbd> close</span>
      </footer>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    justify-content: center;
    align-items: flex-start;
    padding: 12vh var(--sp-4) var(--sp-4);
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .palette {
    width: min(37rem, 100%);
    max-height: 70vh;
    display: flex;
    flex-direction: column;
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-3);
    overflow: hidden;
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  .cmd-search {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: 0 var(--sp-5);
    border-bottom: 1px solid var(--hairline);
  }
  .cmd-search :global(.cmd-search-icon) {
    color: var(--muted);
    flex-shrink: 0;
  }
  .cmd-input {
    flex: 1;
    min-width: 0;
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-lg);
    font-weight: 480;
    padding: 1rem 0;
  }
  .cmd-input:focus-visible {
    outline: none;
    /* The palette dialog is the focus container; the input doesn't need its
       own accent ring (the global :focus-visible box-shadow). */
    box-shadow: none;
  }
  .cmd-input::placeholder {
    color: var(--faint);
  }
  .cmd-list {
    list-style: none;
    margin: 0;
    padding: var(--sp-2);
    overflow-y: auto;
  }
  .cmd {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
    padding: 0.58rem 0.75rem;
    border: none;
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-weight: 520;
    text-align: left;
    cursor: pointer;
    /* Bookmark bar that appears on the active row. */
    box-shadow: inset 0 0 0 0 var(--accent);
    transition:
      background var(--dur-fast) var(--ease-out),
      box-shadow var(--dur-fast) var(--ease-out);
  }
  .cmd.active {
    background: var(--accent-soft);
    box-shadow: inset 3px 0 0 0 var(--accent);
  }
  .cmd-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cmd-hint {
    flex-shrink: 0;
    font-size: 0.68rem;
    font-weight: 640;
    letter-spacing: 0.12em;
    text-transform: uppercase;
    color: var(--faint);
  }
  .cmd.active .cmd-hint {
    color: var(--muted);
  }
  .cmd-empty {
    padding: 1rem 0.75rem;
    color: var(--muted);
    font-size: var(--text-sm);
    font-style: italic;
    font-family: var(--font-display);
  }
  .cmd-foot {
    display: flex;
    gap: var(--sp-4);
    padding: var(--sp-2) var(--sp-5);
    border-top: 1px solid var(--hairline);
    color: var(--faint);
    font-size: var(--text-xs);
  }
  .cmd-foot span {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
  }
</style>
