<script lang="ts">
  import { onMount } from "svelte";
  import {
    listProfiles,
    createProfile,
    ApiError,
    type ProfileInfo,
  } from "~/api/client";
  import { session } from "~/lib/session.svelte";

  let profiles = $state<ProfileInfo[]>([]);
  let loading = $state(true);
  let mode = $state<"pick" | "create">("pick");
  let busy = $state(false);
  let error = $state("");
  let remember = $state(false);

  // PIN entry for a selected locked profile (null = showing the list).
  let selected = $state<ProfileInfo | null>(null);
  let pin = $state("");

  // Create form.
  let newName = $state("");
  let newPin = $state("");

  onMount(loadProfiles);

  async function loadProfiles(): Promise<void> {
    loading = true;
    error = "";
    try {
      profiles = await listProfiles();
      // First run: no profiles yet → go straight to the create form.
      mode = profiles.length === 0 ? "create" : "pick";
    } catch (e) {
      error = e instanceof ApiError ? e.message : "Failed to load profiles";
    } finally {
      loading = false;
    }
  }

  async function pick(p: ProfileInfo): Promise<void> {
    error = "";
    if (p.hasPin) {
      selected = p;
      pin = "";
      return;
    }
    await doLogin(p.name, "");
  }

  function backToList(): void {
    selected = null;
    pin = "";
    error = "";
  }

  async function doLogin(name: string, pinValue: string): Promise<void> {
    busy = true;
    error = "";
    try {
      await session.login(name, pinValue, remember);
      // On success the App swaps this component out; nothing more to do.
    } catch (e) {
      error = e instanceof ApiError ? e.message : "Sign-in failed";
      busy = false;
    }
  }

  async function submitPin(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (selected) await doLogin(selected.name, pin);
  }

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    busy = true;
    error = "";
    const name = newName.trim();
    try {
      await createProfile(name, newPin);
      await session.login(name, newPin, remember);
    } catch (e2) {
      error = e2 instanceof ApiError ? e2.message : "Could not create profile";
      busy = false;
    }
  }
</script>

<div class="screen">
  <div class="card">
    <div class="head">
      <h1 class="brand">Sayumi</h1>
      <p class="tagline">A reading room</p>
    </div>

    {#if loading}
      <p class="muted">Loading…</p>
    {:else if mode === "pick" && !selected}
      <p class="muted">Choose a profile</p>
      <ul class="profiles">
        {#each profiles as p (p.name)}
          <li>
            <button class="profile" onclick={() => pick(p)} disabled={busy}>
              <span class="avatar">{p.name.slice(0, 1).toUpperCase()}</span>
              <span class="name">{p.name}</span>
              {#if p.hasPin}<span class="lock" aria-label="PIN protected">●</span>{/if}
            </button>
          </li>
        {/each}
      </ul>
      <button class="link" onclick={() => { mode = "create"; error = ""; }}>
        + New profile
      </button>
    {:else if selected}
      <form onsubmit={submitPin}>
        <p class="muted">Enter PIN for <strong>{selected.name}</strong></p>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          class="field"
          type="password"
          inputmode="numeric"
          autocomplete="off"
          autofocus
          bind:value={pin}
          placeholder="PIN"
          disabled={busy}
        />
        <label class="remember">
          <input type="checkbox" bind:checked={remember} disabled={busy} />
          Keep me signed in
        </label>
        <button class="primary" type="submit" disabled={busy || pin === ""}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
        <button class="link" type="button" onclick={backToList} disabled={busy}>
          ← Back
        </button>
      </form>
    {:else}
      <form onsubmit={submitCreate}>
        <p class="muted">Create a profile</p>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          class="field"
          type="text"
          autocomplete="off"
          autofocus
          bind:value={newName}
          placeholder="Profile name"
          disabled={busy}
        />
        <input
          class="field"
          type="password"
          inputmode="numeric"
          autocomplete="off"
          bind:value={newPin}
          placeholder="PIN (optional)"
          disabled={busy}
        />
        <button class="primary" type="submit" disabled={busy || newName.trim() === ""}>
          {busy ? "Creating…" : "Create & sign in"}
        </button>
        {#if profiles.length > 0}
          <button
            class="link"
            type="button"
            onclick={() => { mode = "pick"; error = ""; }}
            disabled={busy}
          >
            ← Back
          </button>
        {/if}
      </form>
    {/if}

    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
</div>

<style>
  .screen {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 1.5rem;
  }

  .card {
    width: 100%;
    max-width: 22rem;
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
    animation: rise 0.4s var(--ease) both;
  }
  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(10px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .head {
    text-align: center;
    padding-bottom: var(--sp-4);
    margin-bottom: var(--sp-2);
    border-bottom: 1px solid var(--hairline);
  }
  .brand {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-3xl);
    font-weight: 500;
    line-height: 1;
    letter-spacing: 0.01em;
  }
  .tagline {
    margin: 0.45rem 0 0;
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: 0.18em;
    text-transform: uppercase;
    color: var(--muted);
  }

  .muted {
    margin: 0;
    color: var(--muted);
    text-align: center;
    font-size: var(--text-sm);
  }

  .profiles {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .profile {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    width: 100%;
    padding: 0.6rem 0.8rem;
    border: 1px solid color-mix(in srgb, var(--fg) 12%, transparent);
    border-radius: 0.6rem;
    background: transparent;
    color: var(--fg);
    font: inherit;
    cursor: pointer;
    transition: border-color 0.15s, background 0.15s;
  }
  .profile:hover:not(:disabled) {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 7%, transparent);
  }

  .avatar {
    display: grid;
    place-items: center;
    width: 2rem;
    height: 2rem;
    border-radius: 50%;
    background: color-mix(in srgb, var(--accent) 18%, transparent);
    color: var(--fg);
    font-family: var(--font-display);
    font-size: 1.05rem;
    font-weight: 600;
  }

  .name {
    flex: 1;
    text-align: left;
  }

  .lock {
    color: var(--muted, #6b6661);
    font-size: 0.7rem;
  }

  .field {
    width: 100%;
    padding: 0.6rem 0.75rem;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.55rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
  }
  .field:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }

  form {
    display: flex;
    flex-direction: column;
    gap: 0.7rem;
  }

  .remember {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--muted, #6b6661);
    font-size: 0.9rem;
  }

  .primary {
    padding: 0.6rem 0.9rem;
    border: none;
    border-radius: 0.55rem;
    background: var(--accent);
    color: #fff;
    font: inherit;
    font-weight: 700;
    cursor: pointer;
    transition: opacity 0.15s var(--ease);
  }
  .primary:hover:not(:disabled) {
    opacity: 0.88;
  }
  .primary:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .link {
    align-self: center;
    border: none;
    background: transparent;
    color: var(--muted, #6b6661);
    font: inherit;
    cursor: pointer;
    padding: 0.25rem;
  }
  .link:hover:not(:disabled) {
    color: var(--fg);
  }

  .error {
    margin: 0;
    color: #b3402f;
    text-align: center;
    font-size: 0.9rem;
  }
</style>
