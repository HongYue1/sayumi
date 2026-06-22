<script lang="ts">
  import { onMount } from "svelte";
  import { session } from "~/lib/session.svelte";
  import { ApiError, listProfiles } from "~/api/client";
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
  let hasPin = $state<boolean | null>(null);
  // Existing profile names, lowercased (clone mode only), for a case-insensitive
  // duplicate check; null while still loading.
  let takenNames = $state<string[] | null>(null);

  onMount(() => {
    // Clone never needs the PIN gate; resolve it immediately so deleteReady
    // (unused in clone mode) isn't left pending on null. Also load the existing
    // names so a duplicate is blocked client-side before the server 400s.
    if (mode !== "delete") {
      hasPin = false;
      listProfiles()
        .then((ps) => (takenNames = ps.map((p) => p.name.toLowerCase())))
        .catch(() => (takenNames = []));
      return;
    }
    session
      .currentHasPin()
      .then((v) => (hasPin = v))
      .catch(() => (hasPin = false));
  });

  const trimmedNewName = $derived(newName.trim());
  // Case-insensitive: profiles are stored as on-disk dirs, and two profiles
  // differing only by case is a footgun regardless. profileName is itself in
  // takenNames once loaded, so this also covers the current name; the explicit
  // !== profileName below gives instant feedback before the list arrives.
  const nameTaken = $derived(
    takenNames !== null && takenNames.includes(trimmedNewName.toLowerCase()),
  );
  const cloneReady = $derived(
    trimmedNewName.length > 0 && trimmedNewName !== profileName && !nameTaken,
  );
  // Require an exact name match, plus a PIN when the profile has one. While
  // hasPin is still loading (null) the delete stays disabled.
  const deleteReady = $derived(
    hasPin !== null &&
      confirmName === profileName &&
      (!hasPin || pin.length > 0),
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
        // Snapshot the name first: profileName is the reactive `session.profile`
        // prop, and deleteCurrent() nulls it — reading it after the await would
        // interpolate "null" into the toast.
        const name = profileName;
        await session.deleteCurrent(pin);
        // session.profile is now null — App swaps to the login screen, which
        // unmounts the library (and this dialog) on its own.
        toast.show(`Deleted profile “${name}”`);
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
      <button
        class="close"
        aria-label="Close"
        onclick={onclose}
        disabled={busy}
      >
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
            {@attach (el) => (el as HTMLInputElement).focus()}
          />
        </label>
        {#if nameTaken}
          <p class="note" role="alert">That name is already taken.</p>
        {/if}
        <label class="field">
          <span>PIN for the copy <em>(optional)</em></span>
          <input
            type="password"
            bind:value={newPin}
            inputmode="numeric"
            maxlength="12"
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
            {@attach (el) => (el as HTMLInputElement).focus()}
          />
        </label>
        {#if hasPin}
          <label class="field">
            <span>PIN</span>
            <input
              type="password"
              bind:value={pin}
              inputmode="numeric"
              maxlength="12"
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
    padding: var(--sp-6);
    background: color-mix(in srgb, #000 38%, transparent);
    animation: app-overlay-in var(--dur-fast) var(--ease-out);
  }
  .sheet {
    width: min(26rem, 100%);
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
  /* Pulled up tight under the name field (the form's flex gap would otherwise
     float it); flags a duplicate name as the user types. */
  .note {
    margin: 0;
    margin-top: calc(var(--sp-3) * -1);
    color: var(--danger);
    font-size: var(--text-xs);
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
