<script lang="ts">
  import { onMount } from "svelte";
  import {
    listProfiles,
    createProfile,
    ApiError,
    type ProfileInfo,
  } from "~/api/client";
  import { session } from "~/lib/session.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { Lock, ArrowLeft, Plus, TriangleAlert } from "@lucide/svelte";

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
    <header class="head">
      <h1 class="brand">Sayumi</h1>
      <p class="tagline">“A quiet room for your books.”</p>
    </header>

    {#if loading}
      <!-- Loading skeleton mirroring the profile list. -->
      <ul class="profiles" aria-hidden="true">
        {#each [0, 1, 2] as i (i)}
          <li>
            <div class="profile skeleton">
              <span class="avatar sk-avatar"></span>
              <span class="sk-bar"></span>
            </div>
          </li>
        {/each}
      </ul>
      <p class="muted" role="status">Loading profiles…</p>
    {:else if mode === "pick" && !selected}
      <p class="muted">Choose a profile to continue</p>
      <ul class="profiles">
        {#each profiles as p (p.name)}
          <li>
            <button class="profile" onclick={() => pick(p)} disabled={busy}>
              <span class="avatar">{p.name.slice(0, 1).toUpperCase()}</span>
              <span class="name">{p.name}</span>
              {#if p.hasPin}
                <span class="lock">
                  <Icon icon={Lock} size={16} label="PIN protected" />
                </span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>
      <button class="link" onclick={() => { mode = "create"; error = ""; }}>
        <Icon icon={Plus} size={16} />
        New profile
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
          <Icon icon={ArrowLeft} size={16} />
          Back
        </button>
      </form>
    {:else}
      <form onsubmit={submitCreate}>
        {#if profiles.length === 0}
          <p class="muted">Welcome — create a profile to start your library.</p>
        {:else}
          <p class="muted">Create a profile</p>
        {/if}
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
            <Icon icon={ArrowLeft} size={16} />
            Back
          </button>
        {/if}
      </form>
    {/if}

    {#if error}
      <p class="error" role="alert">
        <Icon icon={TriangleAlert} size={16} />
        <span>{error}</span>
      </p>
    {/if}
  </div>
</div>

<style>
  .screen {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: var(--sp-6);
  }

  .card {
    width: 100%;
    max-width: 22rem;
    display: flex;
    flex-direction: column;
    gap: var(--sp-4);
    animation: rise var(--dur-slow) var(--ease-out) both;
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

  /* Wordmark + tagline, separated from the list by a hairline rule. */
  .head {
    text-align: center;
    padding-bottom: var(--sp-4);
    border-bottom: 1px solid var(--hairline);
  }
  .brand {
    margin: 0;
    font-family: var(--font-display);
    font-size: var(--text-3xl);
    font-weight: 500;
    line-height: var(--lh-tight);
    letter-spacing: 0.01em;
  }
  .tagline {
    margin: var(--sp-3) 0 0;
    font-family: var(--font-display);
    font-variant: small-caps;
    letter-spacing: 0.08em;
    font-size: var(--text-base);
    line-height: var(--lh-snug);
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
    gap: var(--sp-2);
  }

  .profile {
    display: flex;
    align-items: center;
    gap: var(--sp-3);
    width: 100%;
    min-height: 44px;
    padding: var(--sp-2) var(--sp-3);
    border: 1px solid var(--hairline);
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    text-align: left;
    cursor: pointer;
    transition:
      border-color var(--dur) var(--ease-out),
      background var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .profile:hover:not(:disabled) {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 7%, transparent);
  }
  .profile:active:not(:disabled) {
    transform: scale(0.99);
  }
  .profile:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .avatar {
    flex: none;
    display: grid;
    place-items: center;
    width: 2.1rem;
    height: 2.1rem;
    border-radius: 50%;
    background: color-mix(in srgb, var(--accent) 18%, transparent);
    color: var(--fg);
    font-family: var(--font-display);
    font-size: var(--text-lg);
    font-weight: 600;
  }

  .name {
    flex: 1;
    min-width: 0;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .lock {
    flex: none;
    display: inline-flex;
    align-items: center;
    color: var(--muted);
  }

  /* Skeleton placeholders shown while profiles load. */
  .skeleton {
    pointer-events: none;
    cursor: default;
  }
  .sk-avatar {
    background: var(--surface-hover);
  }
  .sk-bar {
    height: 0.8rem;
    width: 55%;
    border-radius: var(--radius);
    background: var(--surface-hover);
  }
  .sk-avatar,
  .sk-bar {
    animation: pulse 1.2s var(--ease) infinite;
  }
  @keyframes pulse {
    0%,
    100% {
      opacity: 0.55;
    }
    50% {
      opacity: 1;
    }
  }

  form {
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
  }

  .field {
    width: 100%;
    min-height: 44px;
    padding: var(--sp-2) var(--sp-3);
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    /* ≥ 16px keeps iOS Safari from zooming the viewport on focus. */
    font-size: var(--text-base);
    transition: border-color var(--dur) var(--ease-out);
  }
  .field::placeholder {
    color: var(--muted);
  }
  .field:focus-visible {
    /* Ring comes from the global :focus-visible rule; just tint the border. */
    border-color: var(--accent);
  }

  .remember {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    color: var(--muted);
    font-size: var(--text-sm);
  }

  .primary {
    min-height: 44px;
    padding: var(--sp-2) var(--sp-4);
    border: none;
    border-radius: var(--radius);
    background: var(--accent);
    color: #fff;
    font: inherit;
    font-weight: 700;
    cursor: pointer;
    transition:
      opacity var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .primary:hover:not(:disabled) {
    opacity: 0.88;
  }
  .primary:active:not(:disabled) {
    transform: scale(0.97);
  }
  .primary:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .link {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-1);
    align-self: center;
    min-height: 44px;
    padding: var(--sp-1) var(--sp-3);
    border: none;
    border-radius: var(--radius);
    background: transparent;
    color: var(--muted);
    font: inherit;
    cursor: pointer;
    transition: color var(--dur) var(--ease-out);
  }
  .link:hover:not(:disabled) {
    color: var(--fg);
  }
  .link:active:not(:disabled) {
    transform: scale(0.97);
  }
  .link:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .error {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    margin: 0;
    color: #b3402f;
    text-align: left;
    font-size: var(--text-sm);
    line-height: var(--lh-snug);
  }
  .error :global(svg) {
    flex: none;
  }
</style>
