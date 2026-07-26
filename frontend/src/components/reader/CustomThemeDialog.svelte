<script lang="ts">
  import { onDestroy } from "svelte";
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
  let pendingAction = $state<"save" | "delete" | null>(null);
  let operationController: AbortController | null = null;
  let deleteArmed = $state(false);
  let nameDirty = $state(false);

  const MAX_THEME_NAME_CHARS = 60;
  const resolvedAccent = $derived(auto ? autoAccent(bg, fg) : accent);
  const accentText = $derived(onAccentColor(resolvedAccent));
  const group = $derived(themeGroupFor(bg));
  const trimmedName = $derived(name.trim());
  const nameError = $derived(
    trimmedName.length === 0
      ? "Enter a theme name."
      : Array.from(trimmedName).length > MAX_THEME_NAME_CHARS
        ? `Theme names can contain at most ${MAX_THEME_NAME_CHARS} characters.`
        : "",
  );
  const visibleNameError = $derived(nameDirty ? nameError : "");
  const canSave = $derived(!busy && nameError === "");
  const closeLabel = $derived(
    pendingAction === "delete"
      ? "Cancel deleting theme"
      : pendingAction === "save"
        ? "Cancel saving theme"
        : "Close",
  );

  function beginOperation(action: "save" | "delete"): AbortController {
    const controller = new AbortController();
    operationController = controller;
    pendingAction = action;
    busy = true;
    return controller;
  }

  function finishOperation(controller: AbortController): boolean {
    if (operationController !== controller || controller.signal.aborted) {
      return false;
    }
    operationController = null;
    pendingAction = null;
    busy = false;
    return true;
  }

  function close(): void {
    operationController?.abort();
    operationController = null;
    onclose();
  }

  onDestroy(() => {
    operationController?.abort();
    operationController = null;
  });

  function toggleAuto(e: Event): void {
    auto = (e.currentTarget as HTMLInputElement).checked;
    // Seed the manual picker from the current auto suggestion so turning the
    // override on starts from a sensible color rather than a stale one.
    if (!auto) accent = autoAccent(bg, fg);
  }

  async function save(e: Event): Promise<void> {
    e.preventDefault();
    if (busy) return;
    nameDirty = true;
    if (nameError) return;
    const input: CustomThemeInput = {
      name: trimmedName,
      group,
      bg,
      fg,
      accent: auto ? "" : accent,
    };
    const controller = beginOperation("save");
    if (edit) {
      const def = await customThemes.update(edit.id, input, controller.signal);
      if (controller.signal.aborted) return;
      if (def) {
        // Its id is unchanged, so the settings effect won't re-fire; repaint
        // the app chrome directly when the edited theme is the active one. The
        // reader frame updates reactively via settings.iframe.
        if (settings.value.theme === def.id) applyTheme(def.id);
        operationController = null;
        onclose();
        return;
      }
    } else {
      const def = await customThemes.create(input, controller.signal);
      if (controller.signal.aborted) return;
      if (def) {
        // Apply the new theme immediately so the user sees their creation.
        settings.update({ theme: def.id });
        applyTheme(def.id);
        operationController = null;
        onclose();
        return;
      }
    }
    // create/update already surfaced a toast on failure; let the user retry.
    finishOperation(controller);
  }

  async function remove(): Promise<void> {
    if (!edit || busy) return;
    if (!deleteArmed) {
      deleteArmed = true;
      return;
    }
    const t = edit;
    const controller = beginOperation("delete");
    const wasActive = settings.value.theme === t.id;
    const ok = await customThemes.remove(t.id, controller.signal);
    if (controller.signal.aborted) return;
    if (ok) {
      if (wasActive) {
        // Fall back to the first built-in sharing the deleted theme's group.
        const fallback =
          THEMES.find((th) => th.group === themeGroupFor(t.bg))?.id ??
          THEMES[0].id;
        settings.update({ theme: fallback });
        applyTheme(fallback);
      }
      operationController = null;
      onclose();
      return;
    }
    finishOperation(controller);
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader / settings window handlers don't also act on it.
      e.stopImmediatePropagation();
      close();
    }
  }
</script>

<!-- Capture phase: this dialog mounts AFTER the reader route, so its bubble
     listener would run last — Read's Escape action (close panel / navigate
     back) would have already fired before stopImmediatePropagation could act.
     Capture runs before every bubble-phase window listener regardless of
     registration order. -->
<svelte:window onkeydowncapture={onKeydown} />

<div class="overlay" role="presentation" onclick={close}>
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
      <div class="head-text">
        <p class="eyebrow">Theme</p>
        <h2 class="display">{editing ? "Edit theme" : "New theme"}</h2>
      </div>
      <button
        class="icon-btn press close"
        aria-label={closeLabel}
        onclick={close}
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
          maxlength={MAX_THEME_NAME_CHARS * 2}
          placeholder="My theme"
          autocomplete="off"
          disabled={busy}
          aria-invalid={visibleNameError ? "true" : undefined}
          aria-describedby={visibleNameError ? "theme-name-error" : undefined}
          oninput={() => (nameDirty = true)}
          {@attach (el) => (el as HTMLInputElement).focus()}
        />
        {#if visibleNameError}
          <small id="theme-name-error" class="field-error" role="alert">
            {visibleNameError}
          </small>
        {/if}
      </label>

      <div class="colors">
        <label class="field">
          <span>Background</span>
          <div class="color-row">
            <input
              type="color"
              bind:value={bg}
              aria-label="Background color"
              disabled={busy}
            />
            <code>{bg}</code>
          </div>
        </label>
        <label class="field">
          <span>Text</span>
          <div class="color-row">
            <input
              type="color"
              bind:value={fg}
              aria-label="Text color"
              disabled={busy}
            />
            <code>{fg}</code>
          </div>
        </label>
      </div>

      <label class="check">
        <input
          type="checkbox"
          checked={auto}
          onchange={toggleAuto}
          disabled={busy}
        />
        <span>Auto accent <small>(derive from your colors)</small></span>
      </label>
      {#if !auto}
        <label class="field">
          <span>Accent</span>
          <div class="color-row">
            <input
              type="color"
              bind:value={accent}
              aria-label="Accent color"
              disabled={busy}
            />
            <code>{accent}</code>
          </div>
        </label>
      {/if}

      <div class="actions">
        {#if editing}
          <button
            type="button"
            class="btn-ghost press danger-ghost"
            class:armed={deleteArmed}
            onclick={remove}
            disabled={busy}
            aria-label={deleteArmed
              ? `Confirm deleting ${trimmedName || "custom theme"}`
              : `Delete ${trimmedName || "custom theme"}`}
          >
            <Icon icon={Trash2} size={16} />
            {deleteArmed ? "Click again to delete" : "Delete"}
          </button>
        {/if}
        <span class="spacer"></span>
        <button type="button" class="btn-ghost press" onclick={close}>
          {pendingAction === "delete"
            ? "Cancel delete"
            : pendingAction === "save"
              ? "Cancel save"
              : "Cancel"}
        </button>
        <button type="submit" class="btn press" disabled={!canSave}>
          {pendingAction === "save"
            ? "Saving…"
            : editing
              ? "Save changes"
              : "Create theme"}
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
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .sheet {
    width: min(32rem, 100%);
    max-height: calc(100vh - var(--sp-12));
    max-height: calc(100dvh - var(--sp-12));
    overflow-y: auto;
    background: var(--raised);
    border: 1px solid var(--hairline);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-3);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-5) var(--sp-5) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
    position: sticky;
    top: 0;
    background: var(--raised);
    z-index: 1;
  }
  .head-text {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .head-text .eyebrow {
    margin: 0;
  }
  h2 {
    margin: 0;
    font-size: var(--text-xl);
    font-weight: 540;
    line-height: var(--lh-tight);
  }
  .close {
    flex-shrink: 0;
  }
  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    padding: var(--sp-5);
  }

  .preview {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    padding: var(--sp-4);
    border-radius: var(--radius-lg);
    box-shadow:
      inset 0 0 0 1px color-mix(in srgb, var(--fg) 14%, transparent),
      var(--shadow-1);
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
    font-family: var(--font-display);
    font-style: italic;
    font-size: var(--text-sm);
  }
  .preview-accent {
    padding: 0.25rem 0.7rem;
    border-radius: 999px;
    font-size: var(--text-xs);
    font-weight: 700;
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
    font-size: var(--text-xs);
    font-weight: 640;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .field input[type="text"] {
    height: 2.5rem;
    padding: 0 0.8rem;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    transition:
      background var(--dur-fast) var(--ease-out),
      border-color var(--dur-fast) var(--ease-out);
  }
  .field input[type="text"]:hover {
    background: var(--surface-hover);
  }
  .field input[type="text"]:focus-visible {
    outline: none;
    background: var(--raised);
    border-color: var(--accent-line);
    box-shadow: var(--focus);
  }
  .field-error {
    color: var(--danger);
    font-size: var(--text-xs);
    line-height: 1.35;
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
    width: 2.7rem;
    height: 2.5rem;
    padding: 3px;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--surface);
    cursor: pointer;
    transition: border-color var(--dur-fast) var(--ease-out);
  }
  .color-row input[type="color"]:hover:not(:disabled) {
    border-color: var(--accent-line);
  }
  .color-row input[type="color"]:disabled,
  .check input:disabled {
    cursor: not-allowed;
  }
  .color-row code {
    font-family: var(--font-mono, ui-monospace, monospace);
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    font-size: var(--text-sm);
    font-weight: 520;
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
    padding-top: var(--sp-4);
    border-top: 1px solid var(--hairline);
  }
  .spacer {
    flex: 1;
  }
  .danger-ghost {
    border-color: transparent;
    color: var(--danger);
  }
  .danger-ghost:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 10%, transparent);
    border-color: color-mix(in srgb, var(--danger) 40%, transparent);
    color: var(--danger);
  }
  .danger-ghost.armed,
  .danger-ghost.armed:hover:not(:disabled) {
    background: var(--danger-surface);
    border-color: var(--danger-surface);
    color: var(--danger-surface-fg);
  }

  @media (max-width: 30rem) {
    .overlay {
      padding: var(--sp-3);
    }
    .sheet {
      max-height: calc(100vh - var(--sp-6));
      max-height: calc(100dvh - var(--sp-6));
    }
    .colors {
      grid-template-columns: 1fr;
    }
    .actions {
      flex-wrap: wrap;
    }
    .actions .spacer {
      display: none;
    }
    .actions button {
      flex: 1 1 auto;
      justify-content: center;
    }
    .actions .danger-ghost {
      flex-basis: 100%;
    }
  }
</style>
