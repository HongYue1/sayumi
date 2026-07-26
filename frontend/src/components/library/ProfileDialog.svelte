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
  let prerequisiteError = $state<string | null>(null);
  let checkingPrerequisite = $state(false);

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
    void loadPrerequisite();
  });

  async function loadPrerequisite(): Promise<void> {
    checkingPrerequisite = true;
    prerequisiteError = null;
    if (mode === "clone") {
      // This list is a correctness gate, not optional decoration. Profile names
      // map to directories, so a case-only duplicate can alias the same path on
      // Windows even though SQLite treats the names as distinct.
      takenNames = null;
      try {
        const profiles = await listProfiles();
        takenNames = profiles.map((profile) => profile.name.toLowerCase());
      } catch (err) {
        prerequisiteError =
          err instanceof ApiError
            ? err.message
            : "Could not check existing profile names.";
      } finally {
        checkingPrerequisite = false;
      }
      return;
    }

    // Deletion must fail closed. Treating a lookup failure as an unprotected
    // profile hides the PIN field and leaves protected profiles undeletable.
    hasPin = null;
    try {
      hasPin = await session.currentHasPin();
    } catch (err) {
      prerequisiteError =
        err instanceof ApiError
          ? err.message
          : "Could not verify this profile’s PIN protection.";
    } finally {
      checkingPrerequisite = false;
    }
  }

  const trimmedNewName = $derived(newName.trim());
  // Case-insensitive: profiles are stored as on-disk dirs, and two profiles
  // differing only by case is a footgun regardless. profileName is itself in
  // takenNames once loaded, so this also covers the current name. Clone remains
  // disabled until the list is available rather than failing this check open.
  const nameTaken = $derived(
    takenNames !== null && takenNames.includes(trimmedNewName.toLowerCase()),
  );
  const nameValid = $derived(
    /^[a-zA-Z0-9](?:[a-zA-Z0-9 _-]{0,30}[a-zA-Z0-9])?$/.test(trimmedNewName),
  );
  const nameError = $derived(
    trimmedNewName.length === 0
      ? null
      : !nameValid
        ? "Use 1–32 characters: letters, digits, spaces, dashes, or underscores; start and end with a letter or digit."
        : nameTaken
          ? "That name is already taken."
          : null,
  );
  const newPinError = $derived(
    newPin !== "" && !/^\d{4,12}$/.test(newPin)
      ? "PIN must be 4–12 digits, or left empty."
      : null,
  );
  const cloneReady = $derived(
    takenNames !== null &&
      trimmedNewName.length > 0 &&
      nameError === null &&
      newPinError === null,
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
        const name = trimmedNewName;
        const submittedPin = newPin;
        await session.clone(name, submittedPin);
        toast.show(`Created a copy: “${name}”`);
        onclose();
      } else {
        // Snapshot the name first: profileName is the reactive `session.profile`
        // prop, and deleteCurrent() nulls it — reading it after the await would
        // interpolate "null" into the toast.
        const name = profileName;
        const submittedPin = pin;
        await session.deleteCurrent(submittedPin);
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

<!-- Capture phase: the dialog mounts after the page's own window key
     listeners, so a bubble listener here runs last and can't pre-empt them;
     capture runs first regardless of registration order. -->
<svelte:window onkeydowncapture={onKeydown} />

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
      <div class="head-text">
        <p class="eyebrow">Profile</p>
        <h2 class="display">
          {mode === "clone" ? "Clone profile" : "Delete profile"}
        </h2>
      </div>
      <button
        class="icon-btn press close"
        aria-label="Close"
        onclick={onclose}
        disabled={busy}
      >
        <Icon icon={X} size={18} />
      </button>
    </header>

    <form onsubmit={submit} aria-busy={busy}>
      {#if mode === "clone"}
        <p class="lede">
          Make a copy of <strong>{profileName}</strong> — its books, settings,
          and flairs are duplicated into a new profile. You stay signed in as
          {profileName}.
        </p>
        <label class="frow">
          <span class="lbl">New profile name</span>
          <input
            class="field"
            type="text"
            bind:value={newName}
            maxlength="32"
            autocomplete="off"
            placeholder={`${profileName} (copy)`}
            aria-invalid={nameError !== null}
            aria-describedby={nameError ? "profile-name-error" : undefined}
            disabled={busy}
            {@attach (el) => (el as HTMLInputElement).focus()}
          />
        </label>
        {#if nameError}
          <p class="note" id="profile-name-error" role="alert">
            {nameError}
          </p>
        {/if}
        <label class="frow">
          <span class="lbl">PIN for the copy <em>(optional)</em></span>
          <input
            class="field"
            type="password"
            bind:value={newPin}
            inputmode="numeric"
            maxlength="12"
            autocomplete="new-password"
            placeholder="4–12 digits"
            aria-invalid={newPinError !== null}
            aria-describedby={newPinError ? "profile-pin-error" : undefined}
            disabled={busy}
          />
        </label>
        {#if newPinError}
          <p class="note" id="profile-pin-error" role="alert">
            {newPinError}
          </p>
        {/if}
      {:else}
        <div class="warn">
          <Icon icon={TriangleAlert} size={18} />
          <p>
            This permanently deletes <strong>{profileName}</strong> and all of its
            books, reading progress, and settings. This can’t be undone.
          </p>
        </div>
        <label class="frow">
          <span class="lbl">Type <strong>{profileName}</strong> to confirm</span
          >
          <input
            class="field"
            type="text"
            bind:value={confirmName}
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            disabled={busy}
            {@attach (el) => (el as HTMLInputElement).focus()}
          />
        </label>
        {#if hasPin}
          <label class="frow">
            <span class="lbl">PIN</span>
            <input
              class="field"
              type="password"
              bind:value={pin}
              inputmode="numeric"
              maxlength="12"
              autocomplete="current-password"
              disabled={busy}
            />
          </label>
        {/if}
      {/if}

      {#if checkingPrerequisite}
        <p class="prerequisite-status" role="status">
          {mode === "clone"
            ? "Checking existing profile names…"
            : "Checking PIN protection…"}
        </p>
      {:else if prerequisiteError}
        <div class="prerequisite-error">
          <p class="error" role="alert">{prerequisiteError}</p>
          <button
            type="button"
            class="btn-ghost press retry"
            onclick={() => void loadPrerequisite()}
            disabled={busy}
          >
            Retry
          </button>
        </div>
      {/if}

      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}

      <div class="actions">
        <button
          type="button"
          class="btn-ghost press"
          onclick={onclose}
          disabled={busy}
        >
          Cancel
        </button>
        <button
          type="submit"
          class={mode === "delete" ? "btn-del press" : "btn press"}
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
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .sheet {
    width: min(26rem, 100%);
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
    z-index: 1;
    background: var(--raised);
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
  .lede {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
    line-height: 1.5;
  }
  .lede strong {
    color: var(--fg);
    font-weight: 640;
  }
  .warn {
    display: flex;
    gap: var(--sp-3);
    padding: var(--sp-3);
    border-radius: var(--radius);
    border: 1px solid color-mix(in srgb, var(--danger) 30%, transparent);
    background: color-mix(in srgb, var(--danger) 10%, transparent);
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
  .frow {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .lbl {
    font-size: var(--text-xs);
    font-weight: 640;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    color: var(--muted);
  }
  .lbl strong {
    font-weight: 800;
    color: var(--fg);
  }
  .lbl em {
    color: var(--faint);
    font-style: normal;
    text-transform: none;
    letter-spacing: 0.02em;
  }
  .frow input {
    height: 2.5rem;
  }
  .frow input:disabled {
    opacity: 0.7;
    cursor: not-allowed;
  }
  .error {
    margin: 0;
    color: var(--danger);
    font-size: var(--text-sm);
  }
  /* Pulled up tight under its field (the form's flex gap would otherwise float
     it); surfaces name/PIN validation as the user types. */
  .note {
    margin: 0;
    margin-top: calc(var(--sp-3) * -1);
    color: var(--danger);
    font-size: var(--text-xs);
  }
  .prerequisite-status {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
  }
  .prerequisite-error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--sp-3);
  }
  .prerequisite-error .error {
    flex: 1;
  }
  .retry {
    flex-shrink: 0;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--sp-2);
    margin-top: var(--sp-1);
    padding-top: var(--sp-4);
    border-top: 1px solid var(--hairline);
  }
  /* Filled destructive action — deliberately the only loud red in the app. */
  .btn-del {
    font-family: var(--font-ui);
    font-size: var(--text-sm);
    font-weight: 560;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    padding: 0.5rem 0.95rem;
    border: none;
    border-radius: var(--radius);
    background: var(--danger-surface);
    color: var(--danger-surface-fg);
    cursor: pointer;
    box-shadow: var(--shadow-1);
    transition:
      filter var(--dur-fast) var(--ease-out),
      box-shadow var(--dur) var(--ease-out);
  }
  .btn-del:hover:not(:disabled) {
    filter: brightness(1.08);
    box-shadow: var(--shadow-2);
  }
  .btn-del:disabled {
    opacity: 0.45;
    cursor: default;
  }
</style>
