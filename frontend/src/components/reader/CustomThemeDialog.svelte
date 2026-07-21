<script lang="ts">
  import { customThemes } from "~/lib/customThemes.svelte";
  import { settings } from "~/lib/settings.svelte";
  import { applyTheme, onAccentColor } from "~/lib/theme";
  import {
    THEMES,
    autoAccent,
    themeGroupFor,
    type ThemeDef,
  } from "~/lib/themes";
  import { type CustomThemeInput } from "~/api/client";
  import { focusTrap } from "~/lib/focusTrap";
  import Icon from "~/lib/Icon.svelte";
  import { X, Trash2 } from "@lucide/svelte";

  interface Props {
    // Colors to seed a new theme from (typically the active theme).
    base: ThemeDef;
    // When provided, edit this existing custom theme instead of creating one.
    edit?: ThemeDef | null;
    onclose: () => void;
  }
  let { base, edit = null, onclose }: Props = $props();

  const editing = $derived(edit !== null);

  // Coerce any color to a 6-digit lowercase #rrggbb, the only form the native
  // color inputs accept. Expands #rgb and falls back for anything unexpected.
  function norm(hex: string, fallback: string): string {
    const s = hex.trim().replace(/^#/, "");
    if (/^[0-9a-fA-F]{3}$/.test(s)) {
      return `#${s[0]}${s[0]}${s[1]}${s[1]}${s[2]}${s[2]}`.toLowerCase();
    }
    if (/^[0-9a-fA-F]{6}$/.test(s)) return `#${s.toLowerCase()}`;
    return fallback;
  }

  // The dialog is remounted per open (SettingsPanel gates it with {#if}), so
  // seeding local editing state from props once is intentional.
  // svelte-ignore state_referenced_locally
  const seed = edit ?? base;
  const seedBg = norm(seed.bg, "#ffffff");
  const seedFg = norm(seed.fg, "#111111");

  // svelte-ignore state_referenced_locally
  let name = $state(edit ? edit.label : "");
  let bg = $state(seedBg);
  let fg = $state(seedFg);
  let accent = $state(norm(seed.accent, autoAccent(seedBg, seedFg)));
  // Auto by default; when editing, stay auto if the stored accent equals what
  // auto would derive (the store resolves a blank accent to autoAccent, so this
  // round-trips the original "auto" choice without the raw record).
  // svelte-ignore state_referenced_locally
  let auto = $state(
    !edit || norm(edit.accent, "").toLowerCase() === autoAccent(seedBg, seedFg),
  );

  let busy = $state(false);

  const resolvedAccent = $derived(auto ? autoAccent(bg, fg) : accent);
  const accentText = $derived(onAccentColor(resolvedAccent));
  const group = $derived(themeGroupFor(bg));
  const trimmedName = $derived(name.trim());
  const canSave = $derived(!busy && trimmedName.length > 0);

  function toggleAuto(e: Event): void {
    auto = (e.currentTarget as HTMLInputElement).checked;
    // Seed the manual picker from the current auto suggestion so turning the
    // override on starts from a sensible color rather than a stale one.
    if (!auto) accent = autoAccent(bg, fg);
  }

  async function save(e: Event): Promise<void> {
    e.preventDefault();
    if (!canSave) return;
    busy = true;
    const input: CustomThemeInput = {
      name: trimmedName,
      group,
      bg,
      fg,
      accent: auto ? "" : accent,
    };
    if (edit) {
      const def = await customThemes.update(edit.id, input);
      if (def) {
        // Its id is unchanged, so the settings effect won't re-fire; repaint
        // the app chrome directly when the edited theme is the active one. The
        // reader frame updates reactively via settings.iframe.
        if (settings.value.theme === def.id) applyTheme(def.id);
        onclose();
        return;
      }
    } else {
      const def = await customThemes.create(input);
      if (def) {
        // Apply the new theme immediately so the user sees their creation.
        settings.update({ theme: def.id });
        applyTheme(def.id);
        onclose();
        return;
      }
    }
    // create/update already surfaced a toast on failure; let the user retry.
    busy = false;
  }

  async function remove(): Promise<void> {
    if (!edit || busy) return;
    const t = edit;
    busy = true;
    const wasActive = settings.value.theme === t.id;
    const ok = await customThemes.remove(t.id);
    if (ok) {
      if (wasActive) {
        // Fall back to the first built-in sharing the deleted theme's group.
        const fallback =
          THEMES.find((th) => th.group === themeGroupFor(t.bg))?.id ??
          THEMES[0].id;
        settings.update({ theme: fallback });
        applyTheme(fallback);
      }
      onclose();
      return;
    }
    busy = false;
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader / settings window handlers don't also act on it.
      e.stopImmediatePropagation();
      if (!busy) onclose();
    }
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="overlay" role="presentation" onclick={() => !busy && onclose()}>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="sheet"
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label={editing ? "Edit custom theme" : "New custom theme"}
    onclick={(e) => e.stopPropagation()}
    {@attach focusTrap}
  >
    <header>
      <h2>{editing ? "Edit theme" : "New theme"}</h2>
      <button
        class="close"
        aria-label="Close"
        onclick={onclose}
        disabled={busy}
      >
        <Icon icon={X} size={18} />
      </button>
    </header>

    <form onsubmit={save}>
      <div
        class="preview"
        style:background={bg}
        style:color={fg}
        aria-hidden="true"
      >
        <span class="preview-aa">Aa</span>
        <span class="preview-sample">The quick brown fox jumps.</span>
        <span
          class="preview-accent"
          style:background={resolvedAccent}
          style:color={accentText}
        >
          Accent
        </span>
      </div>
      <p class="hint">
        Appears in your <strong>{group === "light" ? "Light" : "Dark"}</strong>
        themes — set automatically from the background.
      </p>

      <label class="field">
        <span>Name</span>
        <input
          type="text"
          bind:value={name}
          maxlength="60"
          placeholder="My theme"
          autocomplete="off"
          {@attach (el) => (el as HTMLInputElement).focus()}
        />
      </label>

      <div class="colors">
        <label class="field">
          <span>Background</span>
          <div class="color-row">
            <input type="color" bind:value={bg} aria-label="Background color" />
            <code>{bg}</code>
          </div>
        </label>
        <label class="field">
          <span>Text</span>
          <div class="color-row">
            <input type="color" bind:value={fg} aria-label="Text color" />
            <code>{fg}</code>
          </div>
        </label>
      </div>

      <label class="check">
        <input type="checkbox" checked={auto} onchange={toggleAuto} />
        <span>Auto accent <small>(derive from your colors)</small></span>
      </label>
      {#if !auto}
        <label class="field">
          <span>Accent</span>
          <div class="color-row">
            <input type="color" bind:value={accent} aria-label="Accent color" />
            <code>{accent}</code>
          </div>
        </label>
      {/if}

      <div class="actions">
        {#if editing}
          <button
            type="button"
            class="btn danger-ghost"
            onclick={remove}
            disabled={busy}
          >
            <Icon icon={Trash2} size={16} />
            Delete
          </button>
        {/if}
        <span class="spacer"></span>
        <button
          type="button"
          class="btn ghost"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button type="submit" class="btn primary" disabled={!canSave}>
          {busy ? "Saving…" : editing ? "Save changes" : "Create theme"}
        </button>
      </div>
    </form>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 70;
    display: grid;
    place-items: center;
    padding: var(--sp-6);
    background: color-mix(in srgb, #000 38%, transparent);
    animation: app-overlay-in var(--dur-fast) var(--ease-out);
  }
  .sheet {
    width: min(32rem, 100%);
    max-height: calc(100vh - var(--sp-12));
    overflow-y: auto;
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius-lg);
    animation: app-sheet-in var(--dur) var(--ease-out);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--sp-3) var(--sp-4);
    border-bottom: 1px solid var(--hairline);
    position: sticky;
    top: 0;
    background: var(--bg);
    z-index: 1;
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 500;
    line-height: 1;
  }
  .close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .close:active:not(:disabled) {
    transform: scale(0.94);
  }
  .close:hover:not(:disabled) {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .close:disabled {
    opacity: 0.5;
    cursor: default;
  }
  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    padding: var(--sp-4);
  }

  .preview {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: var(--sp-4);
    border-radius: var(--radius);
    border: 1px solid var(--hairline);
    min-height: 4.5rem;
  }
  .preview-aa {
    font-family: var(--font-display);
    font-size: 1.7rem;
    font-weight: 600;
    line-height: 1;
  }
  .preview-sample {
    flex: 1;
    font-size: var(--text-sm);
  }
  .preview-accent {
    padding: 0.25rem 0.6rem;
    border-radius: var(--radius);
    font-size: var(--text-xs);
    font-weight: 600;
    white-space: nowrap;
  }

  .hint {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--muted);
    line-height: 1.4;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .field > span {
    font-size: var(--text-sm);
    color: var(--fg);
  }
  .field input[type="text"] {
    height: 2.4rem;
    padding: 0 0.7rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    transition:
      border-color var(--dur) var(--ease-out),
      box-shadow var(--dur) var(--ease-out);
  }
  .field input[type="text"]:hover {
    border-color: var(--accent);
  }
  .field input[type="text"]:focus-visible {
    outline: none;
    border-color: var(--accent);
    box-shadow: var(--focus);
  }

  .colors {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--sp-3);
  }
  .color-row {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
  }
  .color-row input[type="color"] {
    width: 2.6rem;
    height: 2.4rem;
    padding: 2px;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    cursor: pointer;
  }
  .color-row code {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: var(--text-xs);
    color: var(--muted);
    text-transform: uppercase;
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    font-size: var(--text-sm);
    color: var(--fg);
    cursor: pointer;
  }
  .check input {
    width: 1rem;
    height: 1rem;
    accent-color: var(--accent);
  }
  .check small {
    color: var(--muted);
  }

  .actions {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    margin-top: var(--sp-1);
  }
  .spacer {
    flex: 1;
  }
  .btn {
    display: inline-flex;
    align-items: center;
    gap: var(--sp-2);
    height: 2.4rem;
    padding: 0 1rem;
    border: 1px solid transparent;
    border-radius: var(--radius);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      opacity var(--dur) var(--ease-out),
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .btn:active:not(:disabled) {
    transform: scale(0.97);
  }
  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .btn.ghost {
    background: transparent;
    border-color: var(--hairline-strong);
    color: var(--fg);
  }
  .btn.ghost:hover:not(:disabled) {
    background: var(--surface-hover);
  }
  .btn.primary {
    background: var(--accent);
    color: var(--accent-fg);
  }
  .btn.primary:hover:not(:disabled) {
    opacity: 0.88;
  }
  .btn.danger-ghost {
    background: transparent;
    border-color: transparent;
    color: var(--danger);
    padding: 0 0.6rem;
  }
  .btn.danger-ghost:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 12%, transparent);
  }
</style>
