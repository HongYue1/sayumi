// Profile picker / first-run create form -- the whole pre-library experience.
// Ported from Login.svelte.
//
// Solid 2.0 notes:
//   - onMount -> onSettled for the initial profile fetch.
//   - loadProfiles computes the next mode off a LOCAL, not the profiles()
//     accessor: a signal read right after a write still returns the pre-write
//     value (batched), which would leave the first-run screen stuck on "pick".
//   - {@attach focusOnMount} -> ref callbacks (run on every mount, unlike the
//     one-shot HTML autofocus attribute).
//   - The if/else-if chain -> Switch/Match with the create form as fallback;
//     the PIN form reads the selected profile through a keyed Show so the
//     value is narrowed and stable.
import { createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import {
  ApiError,
  createProfile,
  listProfiles,
  type ProfileInfo,
} from "~/api/client";
import { session } from "~/lib/session";
import Icon from "~/lib/Icon";
import { ArrowLeft, Lock, Plus, TriangleAlert } from "~/lib/icons";

// Focus the field on every (re)mount. The HTML `autofocus` attribute fires
// only ONCE per document, so a profile re-selection or a pick<->create
// switch would otherwise leave the field unfocused -- matching the explicit
// focus-on-open pattern the dialogs already use.
function focusOnMount(el: HTMLElement): void {
  el.focus({ preventScroll: true });
}

export default function Login() {
  const [profiles, setProfiles] = createSignal<ProfileInfo[]>([]);
  const [loading, setLoading] = createSignal(true);
  const [mode, setMode] = createSignal<"pick" | "create">("pick");
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [remember, setRemember] = createSignal(false);

  // PIN entry for a selected locked profile (null = showing the list).
  const [selected, setSelected] = createSignal<ProfileInfo | null>(null);
  const [pin, setPin] = createSignal("");

  // Create form.
  const [newName, setNewName] = createSignal("");
  const [newPin, setNewPin] = createSignal("");

  onSettled(() => {
    void loadProfiles();
  });

  async function loadProfiles(): Promise<void> {
    setLoading(true);
    setError("");
    try {
      const list = await listProfiles();
      setProfiles(list);
      // First run: no profiles yet -> go straight to the create form.
      setMode(list.length === 0 ? "create" : "pick");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Failed to load profiles");
    } finally {
      setLoading(false);
    }
  }

  async function pick(p: ProfileInfo): Promise<void> {
    setError("");
    if (p.hasPin) {
      setSelected(p);
      setPin("");
      return;
    }
    await doLogin(p.name, "");
  }

  function backToList(): void {
    setSelected(null);
    setPin("");
    setError("");
  }

  async function doLogin(name: string, pinValue: string): Promise<void> {
    setBusy(true);
    setError("");
    try {
      await session.login(name, pinValue, remember());
      // On success the App swaps this component out; nothing more to do.
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Sign-in failed");
      setBusy(false);
    }
  }

  async function submitPin(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const current = selected();
    if (current) await doLogin(current.name, pin());
  }

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    setBusy(true);
    setError("");
    const name = newName().trim();
    let created: { name: string };
    try {
      created = await createProfile(name, newPin());
    } catch (e2) {
      setError(
        e2 instanceof ApiError ? e2.message : "Could not create profile",
      );
      setBusy(false);
      return;
    }
    // Creation is committed independently from the follow-up login. Keep the
    // local picker in sync now so a failed login can retry the existing
    // profile instead of submitting create again and getting a name conflict.
    setProfiles([
      ...profiles(),
      { name: created.name, hasPin: newPin() !== "" },
    ]);
    try {
      await session.login(created.name, newPin(), remember());
    } catch (e2) {
      setError(
        e2 instanceof ApiError
          ? `Profile created, but sign-in failed: ${e2.message}`
          : "Profile created, but sign-in failed",
      );
      setMode("pick");
      setSelected(null);
      setNewName("");
      setNewPin("");
      setBusy(false);
    }
  }

  return (
    <div class="login-screen">
      {/* Embossed press mark behind the composition. */}
      <span class="login-watermark display" aria-hidden="true">
        S
      </span>

      <div class="login-frontispiece">
        <header class="login-head">
          <span class="fleuron login-mark" aria-hidden="true">
            ❦
          </span>
          <h1 class="login-brand wordmark">Sayumi</h1>
        </header>

        <hr class="rule-double" />

        <section class="login-body">
          <Switch
            fallback={
              <form onSubmit={submitCreate}>
                <Show
                  when={profiles().length === 0}
                  fallback={<p class="login-muted">Create a profile</p>}
                >
                  <p class="login-muted">
                    Welcome — create a profile to start your library.
                  </p>
                </Show>
                <input
                  class="field login-big"
                  type="text"
                  autocomplete="off"
                  aria-label="Profile name"
                  ref={focusOnMount}
                  value={newName()}
                  onInput={(e) => setNewName(e.currentTarget.value)}
                  placeholder="Profile name"
                  disabled={busy()}
                />
                <input
                  class="field login-big"
                  type="password"
                  inputmode="numeric"
                  autocomplete="off"
                  aria-label="PIN (optional)"
                  value={newPin()}
                  onInput={(e) => setNewPin(e.currentTarget.value)}
                  placeholder="PIN (optional)"
                  disabled={busy()}
                />
                <button
                  class="btn press login-primary"
                  type="submit"
                  disabled={busy() || newName().trim() === ""}
                >
                  {busy() ? "Creating…" : "Create & sign in"}
                </button>
                <Show when={profiles().length > 0}>
                  <button
                    class="btn-quiet press login-back"
                    type="button"
                    onClick={() => {
                      setMode("pick");
                      setError("");
                    }}
                    disabled={busy()}
                  >
                    <Icon icon={ArrowLeft} size={16} />
                    Back
                  </button>
                </Show>
              </form>
            }
          >
            <Match when={loading()}>
              {/* Loading skeleton mirroring the profile index. */}
              <ul class="login-profiles" aria-hidden="true">
                <For each={[0, 1, 2]}>
                  {() => (
                    <li>
                      <div class="login-profile login-skeleton">
                        <span class="login-initial sk-initial" />
                        <span class="sk-bar" />
                      </div>
                    </li>
                  )}
                </For>
              </ul>
              {/* <output> carries an implicit status role -- the loading
                  announcement stays live without a role attribute. */}
              <output class="login-muted">Loading profiles…</output>
            </Match>
            <Match when={mode() === "pick" && selected() === null}>
              <p class="login-muted">Choose a profile to continue</p>
              <ul class="login-profiles">
                <For each={profiles()}>
                  {(p, i) => (
                    <li style={{ "--i": String(i()) }}>
                      <button
                        class="login-profile"
                        onClick={() => void pick(p)}
                        disabled={busy()}
                      >
                        <span class="login-initial display" aria-hidden="true">
                          {p.name.slice(0, 1).toUpperCase()}
                        </span>
                        <span class="login-name">{p.name}</span>
                        <Show when={p.hasPin}>
                          <span class="login-lock">
                            <Icon icon={Lock} size={15} label="PIN protected" />
                          </span>
                        </Show>
                      </button>
                    </li>
                  )}
                </For>
              </ul>
              <button
                class="btn-quiet press login-new"
                onClick={() => {
                  setMode("create");
                  setError("");
                }}
              >
                <Icon icon={Plus} size={16} />
                New profile
              </button>
            </Match>
            <Match when={selected() !== null}>
              <Show when={selected()} keyed>
                {(p) => (
                  <form onSubmit={submitPin}>
                    <p class="login-muted">
                      Enter PIN for <em class="login-who display">{p.name}</em>
                    </p>
                    <input
                      class="field login-pin"
                      type="password"
                      inputmode="numeric"
                      autocomplete="off"
                      aria-label="PIN"
                      ref={focusOnMount}
                      value={pin()}
                      onInput={(e) => setPin(e.currentTarget.value)}
                      placeholder="PIN"
                      disabled={busy()}
                    />
                    <label class="login-remember">
                      <input
                        type="checkbox"
                        checked={remember()}
                        onChange={(e) => setRemember(e.currentTarget.checked)}
                        disabled={busy()}
                      />
                      Keep me signed in
                    </label>
                    <button
                      class="btn press login-primary"
                      type="submit"
                      disabled={busy() || pin() === ""}
                    >
                      {busy() ? "Signing in…" : "Sign in"}
                    </button>
                    <button
                      class="btn-quiet press login-back"
                      type="button"
                      onClick={backToList}
                      disabled={busy()}
                    >
                      <Icon icon={ArrowLeft} size={16} />
                      Back
                    </button>
                  </form>
                )}
              </Show>
            </Match>
          </Switch>

          <Show when={error() !== ""}>
            <p class="login-error" role="alert">
              <Icon icon={TriangleAlert} size={16} />
              <span>{error()}</span>
            </p>
          </Show>
        </section>
      </div>

      <footer class="login-colophon eyebrow">
        Local-first · No accounts · Yours
      </footer>
    </div>
  );
}
