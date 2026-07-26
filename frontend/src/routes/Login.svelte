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
    let created: { name: string };
    try {
      created = await createProfile(name, newPin);
    } catch (e2) {
      error = e2 instanceof ApiError ? e2.message : "Could not create profile";
      busy = false;
      return;
    }
    // Creation is committed independently from the follow-up login. Keep the
    // local picker in sync now so a failed login can retry the existing profile
    // instead of submitting create again and getting a name conflict.
    profiles = [...profiles, { name: created.name, hasPin: newPin !== "" }];
    try {
      await session.login(created.name, newPin, remember);
    } catch (e2) {
      error =
        e2 instanceof ApiError
          ? `Profile created, but sign-in failed: ${e2.message}`
          : "Profile created, but sign-in failed";
      mode = "pick";
      selected = null;
      newName = "";
      newPin = "";
      busy = false;
    }
  }

  // Focus the field on every (re)mount. The HTML `autofocus` attribute fires
  // only ONCE per document, so a profile re-selection or a pick<->create switch
  // would otherwise leave the field unfocused. An attachment runs on each mount,
  // matching the explicit focus-on-open pattern the dialogs already use.
  function focusOnMount(node: HTMLElement): void {
    node.focus({ preventScroll: true });
  }
</script>

<div class="screen">
  <!-- Embossed press mark behind the composition. -->
  <span class="watermark display" aria-hidden="true">S</span>

  <div class="frontispiece">
    <header class="head">
      <span class="fleuron mark" aria-hidden="true">❦</span>
      <h1 class="brand wordmark">Sayumi</h1>
    </header>

    <hr class="rule-double" />

    <section class="body">
      {#if loading}
        <!-- Loading skeleton mirroring the profile index. -->
        <ul class="profiles" aria-hidden="true">
          {#each [0, 1, 2] as i (i)}
            <li>
              <div class="profile skeleton">
                <span class="initial sk-initial"></span>
                <span class="sk-bar"></span>
              </div>
            </li>
          {/each}
        </ul>
        <p class="muted" role="status">Loading profiles…</p>
      {:else if mode === "pick" && !selected}
        <p class="muted">Choose a profile to continue</p>
        <ul class="profiles">
          {#each profiles as p, i (p.name)}
            <li style:--i={i}>
              <button class="profile" onclick={() => pick(p)} disabled={busy}>
                <span class="initial display" aria-hidden="true"
                  >{p.name.slice(0, 1).toUpperCase()}</span
                >
                <span class="name">{p.name}</span>
                {#if p.hasPin}
                  <span class="lock">
                    <Icon icon={Lock} size={15} label="PIN protected" />
                  </span>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
        <button
          class="btn-quiet press new"
          onclick={() => {
            mode = "create";
            error = "";
          }}
        >
          <Icon icon={Plus} size={16} />
          New profile
        </button>
      {:else if selected}
        <form onsubmit={submitPin}>
          <p class="muted">
            Enter PIN for <em class="who display">{selected.name}</em>
          </p>
          <input
            class="field pin"
            type="password"
            inputmode="numeric"
            autocomplete="off"
            aria-label="PIN"
            {@attach focusOnMount}
            bind:value={pin}
            placeholder="PIN"
            disabled={busy}
          />
          <label class="remember">
            <input type="checkbox" bind:checked={remember} disabled={busy} />
            Keep me signed in
          </label>
          <button
            class="btn press primary"
            type="submit"
            disabled={busy || pin === ""}
          >
            {busy ? "Signing in…" : "Sign in"}
          </button>
          <button
            class="btn-quiet press back"
            type="button"
            onclick={backToList}
            disabled={busy}
          >
            <Icon icon={ArrowLeft} size={16} />
            Back
          </button>
        </form>
      {:else}
        <form onsubmit={submitCreate}>
          {#if profiles.length === 0}
            <p class="muted">
              Welcome — create a profile to start your library.
            </p>
          {:else}
            <p class="muted">Create a profile</p>
          {/if}
          <input
            class="field big"
            type="text"
            autocomplete="off"
            aria-label="Profile name"
            {@attach focusOnMount}
            bind:value={newName}
            placeholder="Profile name"
            disabled={busy}
          />
          <input
            class="field big"
            type="password"
            inputmode="numeric"
            autocomplete="off"
            aria-label="PIN (optional)"
            bind:value={newPin}
            placeholder="PIN (optional)"
            disabled={busy}
          />
          <button
            class="btn press primary"
            type="submit"
            disabled={busy || newName.trim() === ""}
          >
            {busy ? "Creating…" : "Create & sign in"}
          </button>
          {#if profiles.length > 0}
            <button
              class="btn-quiet press back"
              type="button"
              onclick={() => {
                mode = "pick";
                error = "";
              }}
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
    </section>
  </div>

  <footer class="colophon eyebrow">Local-first · No accounts · Yours</footer>
</div>

<style>
  .screen {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: var(--sp-6);
    overflow: hidden;
  }

  /* Giant embossed initial behind everything — pure atmosphere. */
  .watermark {
    position: absolute;
    right: -4rem;
    bottom: -14rem;
    font-size: 44rem;
    font-style: italic;
    font-weight: 500;
    line-height: 1;
    color: var(--fg);
    opacity: 0.035;
    pointer-events: none;
    user-select: none;
  }
  @media (max-width: 40rem) {
    .watermark {
      font-size: 28rem;
      right: -6rem;
      bottom: -9rem;
    }
  }

  .frontispiece {
    width: 100%;
    max-width: 23.5rem;
    display: flex;
    flex-direction: column;
    gap: var(--sp-6);
  }

  /* Staggered page-load: wordmark, rule, then the body. */
  .head {
    text-align: center;
    animation: app-rise-in var(--dur-slower) var(--ease-out) both;
  }
  .rule-double {
    animation: app-overlay-in var(--dur-slower) var(--ease-out) 120ms both;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--sp-5);
    animation: app-rise-in var(--dur-slower) var(--ease-out) 90ms both;
  }

  .mark {
    display: block;
    font-size: var(--text-sm);
    margin-bottom: var(--sp-3);
  }

  .brand {
    margin: 0;
    font-size: clamp(3.4rem, 9vw, 4.6rem);
    line-height: var(--lh-tight);
  }

  .muted {
    margin: 0;
    color: var(--muted);
    text-align: center;
    font-size: var(--text-sm);
  }

  .who {
    font-style: italic;
    font-size: var(--text-base);
    color: var(--fg);
  }

  /* The profile index — an open list ruled like a printed index, not a box. */
  .profiles {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
  }
  .profiles li {
    border-bottom: 1px solid var(--hairline);
    animation: app-rise-in var(--dur-slow) var(--ease-out) both;
    animation-delay: calc(140ms + var(--i, 0) * 45ms);
  }

  .profile {
    display: flex;
    align-items: center;
    gap: var(--sp-4);
    width: 100%;
    min-height: 3.4rem;
    padding: var(--sp-2) var(--sp-2);
    border: none;
    background: transparent;
    color: var(--fg);
    font: inherit;
    text-align: left;
    cursor: pointer;
    border-radius: var(--radius-sm);
    /* Bookmark that slides in from the left edge on hover. */
    box-shadow: inset 0 0 0 0 var(--accent);
    transition:
      background var(--dur) var(--ease-out),
      box-shadow var(--dur) var(--ease-out),
      transform var(--dur) var(--ease-out);
  }
  .profile:hover:not(:disabled),
  .profile:focus-visible {
    background: var(--surface);
    box-shadow: inset 3px 0 0 0 var(--accent);
    transform: translateX(4px);
  }
  .profile:focus-visible {
    box-shadow:
      inset 3px 0 0 0 var(--accent),
      var(--focus);
  }
  .profile:active:not(:disabled) {
    transform: translateX(4px) scale(0.99);
  }
  .profile:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  /* Drop-cap initial in the display face — the "library card" letter. */
  .initial {
    flex: none;
    width: 2.2rem;
    text-align: center;
    font-size: 1.7rem;
    font-weight: 560;
    line-height: 1;
    color: var(--faint);
    transition: color var(--dur) var(--ease-out);
  }
  .profile:hover .initial,
  .profile:focus-visible .initial {
    color: var(--accent);
  }

  .name {
    flex: 1;
    min-width: 0;
    font-size: var(--text-base);
    font-weight: 540;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .lock {
    flex: none;
    display: inline-flex;
    align-items: center;
    color: var(--faint);
  }

  /* Skeleton placeholders shown while profiles load. */
  .skeleton {
    pointer-events: none;
    cursor: default;
  }
  .sk-initial {
    flex: none;
    width: 2.2rem;
    height: 1.6rem;
    border-radius: var(--radius-sm);
    background: var(--surface-hover);
  }
  .sk-bar {
    height: 0.8rem;
    width: 55%;
    border-radius: var(--radius-sm);
    background: var(--surface-hover);
  }
  .sk-initial,
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

  /* Larger fields on this screen; ≥16px also keeps iOS Safari from zooming. */
  .field.big,
  .field.pin {
    min-height: 3rem;
    font-size: var(--text-base);
  }
  .field.pin {
    text-align: center;
    letter-spacing: 0.4em;
    font-weight: 600;
  }
  .field.pin::placeholder {
    letter-spacing: 0.08em;
    font-weight: 480;
  }

  .remember {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    color: var(--muted);
    font-size: var(--text-sm);
  }
  .remember input {
    accent-color: var(--accent);
  }

  .primary {
    min-height: 3rem;
    font-size: var(--text-base);
    font-weight: 640;
  }

  .back,
  .new {
    align-self: center;
  }

  .error {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--sp-2);
    margin: 0;
    color: var(--danger);
    text-align: left;
    font-size: var(--text-sm);
    line-height: var(--lh-snug);
  }
  .error :global(svg) {
    flex: none;
  }

  /* Colophon line pinned to the foot of the frontispiece. */
  .colophon {
    position: absolute;
    bottom: var(--sp-6);
    left: 0;
    right: 0;
    text-align: center;
    color: var(--faint);
    animation: app-overlay-in var(--dur-slower) var(--ease-out) 300ms both;
  }
</style>
