<script lang="ts">
  import { onMount } from "svelte";
  import { session } from "~/lib/session.svelte";
  import { ApiError } from "~/api/client";
  import { toast } from "~/lib/toast.svelte";
  import { focusTrap } from "~/lib/focusTrap";
  import Icon from "~/lib/Icon.svelte";
  import { X, TriangleAlert } from "@lucide/svelte";

  interface Props {
    mode: "clone" | "delete";
    profileName: string;
    onclose: () => void;
  }
  let { mode, profileName, onclose }: Props = $props();

  let busy = $state(false);
  let error = $state<string | null>(null);

  // Clone fields.
  let newName = $state("");
  let newPin = $state("");

  // Delete fields. hasPin = null while we're still loading whether the current
  // profile is PIN-protected (decides if the PIN field is required at all).
  let confirmName = $state("");
  let pin = $state("");
  let hasPin = $state<boolean | null>(mode === "clone" ? false : null);

  onMount(() => {
    if (mode !== "delete") return;
    session
      .currentHasPin()
      .then((v) => (hasPin = v))
      .catch(() => (hasPin = false));
  });

  const cloneReady = $derived(
    newName.trim().length > 0 && newName.trim() !== profileName,
  );
  // Require an exact name match, plus a PIN when the profile has one. While
  // hasPin is still loading (null) the delete stays disabled.
  const deleteReady = $derived(
    hasPin !== null && confirmName === profileName && (!hasPin || pin.length > 0),
  );
  const canSubmit = $derived(
    !busy && (mode === "clone" ? cloneReady : deleteReady),
  );

  async function submit(e: Event): Promise<void> {
    e.preventDefault();
    if (!canSubmit) return;
    busy = true;
    error = null;
    try {
      if (mode === "clone") {
        const name = newName.trim();
        await session.clone(name, newPin);
        toast.show(`Created a copy: “${name}”`);
        onclose();
      } else {
        await session.deleteCurrent(pin);
        // session.profile is now null — App swaps to the login screen, which
        // unmounts the library (and this dialog) on its own.
        toast.show(`Deleted profile “${profileName}”`);
      }
    } catch (err) {
      error = err instanceof ApiError ? err.message : "Something went wrong.";
      busy = false;
    }
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on it.
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
    aria-label={mode === "clone" ? "Clone profile" : "Delete profile"}
    onclick={(e) => e.stopPropagation()}
    {@attach focusTrap}
  >
    <header>
      <h2>{mode === "clone" ? "Clone profile" : "Delete profile"}</h2>
      <button class="close" aria-label="Close" onclick={onclose} disabled={busy}>
        <Icon icon={X} size={18} />
      </button>
    </header>

    <form onsubmit={submit}>
      {#if mode === "clone"}
        <p class="lede">
          Make a copy of <strong>{profileName}</strong> — its books, settings,
          and flairs are duplicated into a new profile. You stay signed in as
          {profileName}.
        </p>
        <label class="field">
          <span>New profile name</span>
          <input
            type="text"
            bind:value={newName}
            maxlength="32"
            autocomplete="off"
            placeholder={`${profileName} (copy)`}
          />
        </label>
        <label class="field">
          <span>PIN for the copy <em>(optional)</em></span>
          <input
            type="password"
            bind:value={newPin}
            inputmode="numeric"
            autocomplete="new-password"
            placeholder="4–12 digits"
          />
        </label>
      {:else}
        <div class="warn">
          <Icon icon={TriangleAlert} size={18} />
          <p>
            This permanently deletes <strong>{profileName}</strong> and all of its
            books, reading progress, and settings. This can’t be undone.
          </p>
        </div>
        <label class="field">
          <span>Type <strong>{profileName}</strong> to confirm</span>
          <input
            type="text"
            bind:value={confirmName}
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </label>
        {#if hasPin}
          <label class="field">
            <span>PIN</span>
            <input
              type="password"
              bind:value={pin}
              inputmode="numeric"
              autocomplete="current-password"
            />
          </label>
        {/if}
      {/if}

      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}

      <div class="actions">
        <button
          type="button"
          class="btn ghost"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button
          type="submit"
          class={`btn ${mode === "delete" ? "danger" : "primary"}`}
          disabled={!canSubmit}
        >
          {#if mode === "clone"}
            {busy ? "Creating…" : "Create copy"}
          {:else}
            {busy ? "Deleting…" : "Delete profile"}
          {/if}
        </button>
      </div>
    </form>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: grid;
    place-items: center;
    padding: 1.5rem;
    background: color-mix(in srgb, #000 38%, transparent);
    animation: ov-in var(--dur-fast) var(--ease-out);
  }
  @keyframes ov-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  .sheet {
    width: min(26rem, 100%);
    background: var(--bg);
    border: 1px solid var(--hairline-strong);
    border-radius: 0.75rem;
    animation: sh-in var(--dur) var(--ease-out);
  }
  @keyframes sh-in {
    from {
      opacity: 0;
      transform: translateY(-8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1.1rem;
    border-bottom: 1px solid var(--hairline);
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-xl);
    font-weight: 500;
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
      color var(--dur) var(--ease-out);
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
    padding: 1.1rem;
  }
  .lede {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
    line-height: 1.5;
  }
  .lede strong {
    color: var(--fg);
    font-weight: 600;
  }
  .warn {
    display: flex;
    gap: var(--sp-3);
    padding: var(--sp-3);
    border-radius: var(--radius);
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    color: var(--danger);
  }
  .warn p {
    margin: 0;
    font-size: var(--text-sm);
    line-height: 1.5;
  }
  .warn strong {
    font-weight: 700;
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
  .field strong {
    font-weight: 600;
  }
  .field em {
    color: var(--muted);
    font-style: normal;
  }
  .field input {
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
  .field input:hover {
    border-color: var(--accent);
  }
  .field input:focus-visible {
    outline: none;
    border-color: var(--accent);
    box-shadow: var(--focus);
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--sp-2);
    margin-top: var(--sp-1);
  }
  .btn {
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
  .btn.danger {
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
  }
  .btn.danger:hover:not(:disabled) {
    opacity: 0.9;
  }
</style>
