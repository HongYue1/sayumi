// Profile picker / first-run create form -- the whole pre-library experience.
//
// Solid 2.0 notes:
//   - onSettled's teardown is RETURNED from the callback: onCleanup() inside
//     it throws CLEANUP_IN_FORBIDDEN_SCOPE.
//   - loadProfiles computes the next mode off a LOCAL, not the profiles()
//     accessor: a signal read right after a write still returns the pre-write
//     value (batched). On FIRST run both reads are [] so the bug cannot show
//     there; the reachable case is a RETURNING user, where the stale [] would
//     send a profile owner to the create form instead of the picker.
//   - A <For> index accessor stays inside JSX. The row factory is untracked,
//     so reading i() there snapshots the index and emits STRICT_READ_UNTRACKED.
//   - The PIN form reads the selected profile through a keyed Show so the
//     value is narrowed and stable.
import { createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import {
  ApiError,
  createProfile,
  listProfiles,
  type ProfileInfo,
} from "~/api/client";
import { getErrorMessage } from "~/lib/errors";
import { session } from "~/lib/session";
import Icon from "~/lib/Icon";
import { ArrowLeft, Lock, Plus, TriangleAlert } from "~/lib/icons";

// Focus the field on every (re)mount. The HTML `autofocus` attribute fires
// only ONCE per document, so a profile re-selection or a pick<->create
// switch would otherwise leave the field unfocused -- matching the explicit
// focus-on-open pattern the dialogs already use.
//
// preventScroll is load-bearing rather than polite: .login-screen is
// overflow:hidden around an absolutely positioned watermark parked far outside
// the viewport (app.css: right:-4rem; bottom:-14rem; font-size:44rem), so a
// scroll-into-view on focus would shunt the whole composition sideways.
function focusOnMount(el: HTMLElement): void {
  // Deferred by one microtask. A ref callback fires while the node is still
  // being positioned, and detaching a focused element blurs it -- including
  // the detach/attach pair that a move is made of -- so an inline focus() is
  // dropped the instant the node is inserted. One turn later it has landed.
  queueMicrotask(() => {
    if (el.isConnected) el.focus({ preventScroll: true });
  });
}

// Mirrors internal/api/auth.go (validateProfileName): the same regex, the same
// path-hostile characters, and the same Windows device names. Without it the
// form posts "Jose", "_bob", "bob-" or "nul" and the server answers 400 with a
// message that does not say which rule was broken.
const NAME_PATTERN = /^[a-zA-Z0-9]([a-zA-Z0-9 _-]{0,30}[a-zA-Z0-9])?$/;
const NAME_ILLEGAL = /[/\\:*?"<>|]|\.\./;
const RESERVED_NAMES = new Set([
  "con",
  "prn",
  "aux",
  "nul",
  "com1",
  "com2",
  "com3",
  "com4",
  "com5",
  "com6",
  "com7",
  "com8",
  "com9",
  "lpt1",
  "lpt2",
  "lpt3",
  "lpt4",
  "lpt5",
  "lpt6",
  "lpt7",
  "lpt8",
  "lpt9",
]);

// Rendered into a permanently-mounted element and content-toggled, so it lives
// here instead of inline in the JSX.
const PIN_CONSEQUENCE =
  "Without a PIN this profile opens for anyone who can reach this server, " +
  "including other devices on the network when Sayumi runs with -network.";

// "" when the name is acceptable, otherwise the message to show.
function nameProblem(raw: string): string {
  const name = raw.trim();
  if (name === "") return "Enter a profile name.";
  if (NAME_ILLEGAL.test(name) || !NAME_PATTERN.test(name)) {
    return "Use letters, numbers, spaces, _ or -, up to 32 characters, starting and ending with a letter or number.";
  }
  if (RESERVED_NAMES.has(name.toLowerCase())) {
    return `${name} is a name Windows reserves for a device. Pick another.`;
  }
  return "";
}

export default function Login() {
  const [profiles, setProfiles] = createSignal<ProfileInfo[]>([]);
  const [loading, setLoading] = createSignal(true);
  // A failed list is NOT an empty list: conflating them invites a returning
  // user to create a duplicate profile, with no retry and no way back.
  const [loadFailed, setLoadFailed] = createSignal(false);
  const [mode, setMode] = createSignal<"pick" | "create">("pick");
  const [busy, setBusy] = createSignal(false);
  const [busyWhat, setBusyWhat] = createSignal<"login" | "create">("login");
  const [error, setError] = createSignal("");
  const [remember, setRemember] = createSignal(false);

  // PIN entry for a selected locked profile (null = the picker OR, when mode()
  // is "create", the create form -- selected() === null on its own does not
  // mean the list is on screen, hence the conjunct in the Match below).
  const [selected, setSelected] = createSignal<ProfileInfo | null>(null);
  const [pin, setPin] = createSignal("");

  // Create form.
  const [newName, setNewName] = createSignal("");
  const [newPin, setNewPin] = createSignal("");

  // Set when the picker is reached by going BACK, so the destination can take
  // focus; forward transitions focus their own first field instead.
  // A plain variable, not a signal: the ref callback that reads it fires
  // during the flush of the very writes that set it, and a read inside
  // that flush still returns the pre-write value. The read is deliberately
  // non-reactive anyway, so a signal bought nothing here.
  let returning = false;

  // busy() is a signal: two activations in the same tick both read the
  // pre-write value, so it cannot serialise submits on its own.
  let inFlight = false;
  // Flipped by the onSettled teardown. App.tsx unmounts this route the moment
  // the session authenticates, which can happen mid-request, so every
  // continuation that resumes after an await has to check it.
  let disposed = false;
  // Newest create request wins; a superseded one must not write state.
  let createController: AbortController | null = null;
  // Whatever had focus when a submit began. Nothing blurs on busy any more
  // (aria-disabled keeps every control focusable); the memory still covers
  // the failure paths that remount the focused control.
  let lastFocused: HTMLElement | null = null;

  onSettled(() => {
    const boot = new AbortController();
    // The void-wrapped arrow is load-bearing: onSettled consumes the RETURN
    // value as its teardown, and onCleanup() in this scope throws
    // CLEANUP_IN_FORBIDDEN_SCOPE, so the abort must be returned instead.
    void loadProfiles(boot.signal);
    return () => {
      disposed = true;
      boot.abort();
      createController?.abort();
    };
  });

  function rememberFocus(): void {
    const el = document.activeElement;
    lastFocused = el instanceof HTMLElement ? el : null;
  }

  // Deferred by a microtask: the control is re-enabled by a signal write, and
  // a still-disabled element silently refuses focus. isConnected is checked
  // the way focusTrap does -- the node may have been unmounted by the failure.
  function restoreFocus(): void {
    const el = lastFocused;
    lastFocused = null;
    if (el === null) return;
    queueMicrotask(() => {
      if (!disposed && el.isConnected) el.focus({ preventScroll: true });
    });
  }

  // Focus the picker only when it was reached by going back, never on the
  // first paint. Ref callbacks run unowned, so this read is deliberately
  // non-reactive.
  function focusIfReturned(el: HTMLElement): void {
    if (!returning) return;
    returning = false;
    focusOnMount(el);
  }

  // ONE permanently-mounted status region for the whole screen: a region that
  // is inserted together with its text announces nothing, so the region stays
  // put and only the text changes.
  function statusText(): string {
    if (loading()) return "Loading profiles…";
    if (!busy()) return "";
    return busyWhat() === "create" ? "Creating profile…" : "Signing in…";
  }

  // Deliberately does NOT clear error() on entry: callers that want a clean
  // slate clear it themselves, so a resync triggered by a failed sign-in
  // cannot wipe the message that triggered it.
  async function loadProfiles(signal?: AbortSignal): Promise<void> {
    setLoading(true);
    setLoadFailed(false);
    try {
      const list = await listProfiles(signal);
      if (disposed || signal?.aborted === true) return;
      setProfiles(list);
      // First run: no profiles yet -> go straight to the create form.
      setMode(list.length === 0 ? "create" : "pick");
    } catch (e) {
      if (disposed || signal?.aborted === true) return;
      setLoadFailed(true);
      setError(getErrorMessage(e, "Failed to load profiles"));
    } finally {
      if (!disposed && signal?.aborted !== true) setLoading(false);
    }
  }

  async function pick(p: ProfileInfo): Promise<void> {
    // aria-disabled rows stay clickable; the guard refuses to switch targets
    // (or start a second request) while a sign-in is in flight.
    if (busy()) return;
    setError("");
    if (p.hasPin) {
      returning = false;
      setSelected(p);
      setPin("");
      return;
    }
    await doLogin(p.name, "", remember());
  }

  function backToList(): void {
    // Guarded like every control here: leaving mid-request would unmount the
    // form whose request is still running.
    if (busy()) return;
    // FIRST: the ref callback on the re-created picker heading reads this flag
    // during the render that the writes below trigger, so setting it last
    // leaves focus on <body>.
    returning = true;
    setSelected(null);
    setPin("");
    setError("");
    // One shared flag drives every sign-in path here, so a tick left over from
    // a locked profile would silently grant a 30-day session (and a token at
    // rest) to whichever profile is picked next.
    setRemember(false);
  }

  function openCreate(): void {
    if (busy()) return;
    returning = false;
    setMode("create");
    setError("");
  }

  function leaveCreate(): void {
    if (busy()) return;
    // Same ordering contract as backToList.
    returning = true;
    setMode("pick");
    setError("");
    // The same reset the create-then-login-failed path performs, so a
    // half-typed draft never reappears when the form is opened again.
    setNewName("");
    setNewPin("");
  }

  // Branch on the code, never on the prose: middleware.go's literals are
  // fixed but not contractual, and "invalid name or PIN" is useless in a form
  // where the name was chosen by clicking a row.
  function loginErrorMessage(e: unknown): string {
    if (!(e instanceof ApiError)) return "Sign-in failed";
    switch (e.code) {
      case "invalid_credentials":
        return "That PIN did not match. Check it and try again.";
      case "rate_limited":
        return "Too many attempts. Wait a minute, then try again.";
      case "not_found":
        return "That profile is no longer available.";
      case "network_error":
        return "Cannot reach Sayumi. Check that the server is still running.";
      default:
        return e.message || "Sign-in failed";
    }
  }

  async function doLogin(
    name: string,
    pinValue: string,
    keepSignedIn: boolean,
  ): Promise<void> {
    if (inFlight) return;
    inFlight = true;
    rememberFocus();
    setBusyWhat("login");
    setBusy(true);
    setError("");
    try {
      await session.login(name, pinValue, keepSignedIn);
      // On success the App swaps this component out. busy() stays true so the
      // form cannot flash back to its enabled state during the swap.
    } catch (e) {
      if (disposed) return;
      const code = e instanceof ApiError ? e.code : undefined;
      setError(loginErrorMessage(e));
      // A rejected PIN should not stay in the signal or on screen. A transport
      // failure keeps it, because retrying is then one click.
      if (code === "invalid_credentials") setPin("");
      if (code === "not_found") {
        // The picker is a mount-time snapshot and this row is gone. Resync
        // instead of spending throttle attempts on a profile that cannot win.
        setSelected(null);
        void loadProfiles();
      }
      setBusy(false);
      restoreFocus();
    } finally {
      inFlight = false;
    }
  }

  async function submitPin(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    const current = selected();
    if (current === null) return;
    // The button stays focusable (aria-disabled, not disabled), so Enter on an
    // empty field has to say why nothing happened.
    if (pin() === "") {
      setError("Enter the PIN for this profile.");
      return;
    }
    await doLogin(current.name, pin(), remember());
  }

  async function submitCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (inFlight) return;
    const name = newName().trim();
    const problem = nameProblem(name);
    if (problem !== "") {
      setError(problem);
      return;
    }
    inFlight = true;
    rememberFocus();
    setBusyWhat("create");
    setBusy(true);
    setError("");
    const controller = new AbortController();
    createController = controller;
    // Read once: the fields are reset on some failure paths, and the login
    // that follows must use what was actually submitted.
    const pinValue = newPin();
    const keepSignedIn = remember();
    let created: { name: string };
    try {
      created = await createProfile(name, pinValue, controller.signal);
    } catch (e2) {
      inFlight = false;
      if (disposed || controller !== createController) return;
      createController = null;
      const code = e2 instanceof ApiError ? e2.code : undefined;
      setError(getErrorMessage(e2, "Could not create profile"));
      setBusy(false);
      restoreFocus();
      // The row can exist even though this response failed: the DB row is
      // written before the profile directory, and a lost 201 turns the retry
      // into a 409. Resync rather than stranding the user on a form whose Back
      // button is hidden on first run.
      if (code !== "invalid_name" && code !== "invalid_pin") {
        await loadProfiles();
        if (disposed) return;
        if (profiles().some((p) => p.name === name)) setMode("pick");
      }
      return;
    }
    if (disposed || controller !== createController) {
      inFlight = false;
      return;
    }
    createController = null;
    // Creation is committed independently from the follow-up login. Keep the
    // local picker in sync now so a failed login can retry the existing
    // profile instead of submitting create again and getting a name conflict.
    setProfiles([
      ...profiles(),
      { name: created.name, hasPin: pinValue !== "" },
    ]);
    setBusyWhat("login");
    try {
      await session.login(created.name, pinValue, keepSignedIn);
    } catch (e2) {
      // A reachability report inside the create response can fire session's
      // armed boot re-probe, authenticate us, and dispose this route
      // mid-await. Never write signals (or log in again) after that.
      if (disposed || session.authenticated) return;
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
      restoreFocus();
    } finally {
      inFlight = false;
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
                {/* Content-toggled, not Show-toggled: text swaps inside a
                    stable element, the same mounted-region doctrine the live
                    regions below rely on — a region (or sentence) inserted in
                    the same tick as its text announces nothing. */}
                <p class="login-muted">
                  {profiles().length === 0 && !loadFailed()
                    ? "Welcome — create a profile to start your library."
                    : "Create a profile"}
                </p>
                <input
                  class="field login-big"
                  type="text"
                  autocomplete="username"
                  aria-label="Profile name"
                  ref={focusOnMount}
                  value={newName()}
                  onInput={(e) => setNewName(e.currentTarget.value)}
                  placeholder="Profile name"
                  readonly={busy()}
                  aria-disabled={busy() ? "true" : "false"}
                />
                <Show
                  when={
                    newName().trim() !== "" && nameProblem(newName()) !== ""
                  }
                >
                  <p class="login-muted login-hint">{nameProblem(newName())}</p>
                </Show>
                <input
                  class="field login-big"
                  type="password"
                  inputmode="numeric"
                  autocomplete="new-password"
                  aria-label="PIN (optional)"
                  value={newPin()}
                  onInput={(e) => setNewPin(e.currentTarget.value)}
                  placeholder="PIN (optional)"
                  readonly={busy()}
                  aria-disabled={busy() ? "true" : "false"}
                />
                {/* Same reason as the sentence above, demonstrated here: this
                    one starts true on an empty PIN, and the Show never took it
                    back down -- it stayed put and then duplicated on the next
                    toggle. */}
                <p class="login-muted login-hint">
                  {newPin() === "" ? PIN_CONSEQUENCE : ""}
                </p>
                {/* aria-disabled, not disabled: a disabled default button
                    drops out of the tab order and makes Enter a silent
                    no-op, so the guard lives in submitCreate instead. */}
                <button
                  class="btn press login-primary"
                  type="submit"
                  aria-disabled={
                    busy() || nameProblem(newName()) !== "" ? "true" : "false"
                  }
                >
                  {busy() ? "Creating…" : "Create & sign in"}
                </button>
                <Show when={profiles().length > 0 || loadFailed()}>
                  <button
                    class="btn-quiet press login-back"
                    type="button"
                    onClick={leaveCreate}
                    aria-disabled={busy() ? "true" : "false"}
                  >
                    <Icon icon={ArrowLeft} size={16} decorative />
                    Back
                  </button>
                </Show>
              </form>
            }
          >
            <Match when={loading()}>
              {/* Loading skeleton mirroring the profile index. Solid 2.0 has
                  no Index export and this list is static, so .map() is the
                  idiom -- <For> would keep a reconciler alive for three nodes
                  that never change. */}
              <ul class="login-profiles" aria-hidden="true">
                {[0, 1, 2].map(() => (
                  <li>
                    <div class="login-profile login-skeleton">
                      <span class="login-initial sk-initial" />
                      <span class="sk-bar" />
                    </div>
                  </li>
                ))}
              </ul>
            </Match>
            <Match when={loadFailed() && mode() === "pick"}>
              <p class="login-muted" tabindex={-1} ref={focusIfReturned}>
                Could not load your profiles.
              </p>
              <button
                class="btn press login-primary"
                type="button"
                onClick={() => {
                  if (busy()) return;
                  setError("");
                  void loadProfiles();
                }}
                aria-disabled={busy() ? "true" : "false"}
              >
                Try again
              </button>
              <button
                class="btn-quiet press login-new"
                type="button"
                onClick={openCreate}
                aria-disabled={busy() ? "true" : "false"}
              >
                <Icon icon={Plus} size={16} decorative />
                New profile
              </button>
            </Match>
            <Match when={mode() === "pick" && selected() === null}>
              <p class="login-muted" tabindex={-1} ref={focusIfReturned}>
                Choose a profile to continue
              </p>
              {/* eslint-disable jsx-a11y/no-redundant-roles -- Safari and VoiceOver drop list semantics from a ul styled list-style: none, which .login-profiles is; the role keeps the aria-label attached to a list. */}
              <ul class="login-profiles" role="list" aria-label="Profiles">
                <For each={profiles()}>
                  {(p, i) => (
                    <li style={{ "--i": String(i()) }}>
                      <button
                        class="login-profile"
                        onClick={() => void pick(p)}
                        aria-disabled={busy() ? "true" : "false"}
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
              {/* Gated on busy() like every other control here: an ungated
                  escape hatch during an in-flight sign-in unmounts the form
                  whose request is still running. */}
              <button
                class="btn-quiet press login-new"
                type="button"
                onClick={openCreate}
                aria-disabled={busy() ? "true" : "false"}
              >
                <Icon icon={Plus} size={16} decorative />
                New profile
              </button>
            </Match>
            <Match when={selected()}>
              <Show when={selected()} keyed>
                {(p) => (
                  <form onSubmit={submitPin}>
                    <p class="login-muted">
                      Enter PIN for <em class="login-who display">{p.name}</em>
                    </p>
                    {/* Password managers key on an identity field: with
                        none in the form they offer profile A's saved PIN in
                        profile B's prompt. Hidden from sight and from AT --
                        the line above already names the profile. */}
                    <input
                      class="sr-only"
                      type="text"
                      name="username"
                      autocomplete="username"
                      value={p.name}
                      readonly
                      tabindex={-1}
                      aria-hidden="true"
                    />
                    <input
                      class="field login-pin"
                      type="password"
                      inputmode="numeric"
                      autocomplete="current-password"
                      aria-label="PIN"
                      ref={focusOnMount}
                      value={pin()}
                      onInput={(e) => setPin(e.currentTarget.value)}
                      placeholder="PIN"
                      readonly={busy()}
                      aria-disabled={busy() ? "true" : "false"}
                    />
                    <button
                      class="btn press login-primary"
                      type="submit"
                      aria-disabled={busy() || pin() === "" ? "true" : "false"}
                    >
                      {busy() ? "Signing in…" : "Sign in"}
                    </button>
                    <button
                      class="btn-quiet press login-back"
                      type="button"
                      onClick={backToList}
                      aria-disabled={busy() ? "true" : "false"}
                    >
                      <Icon icon={ArrowLeft} size={16} decorative />
                      Back
                    </button>
                  </form>
                )}
              </Show>
            </Match>
          </Switch>

          {/* One checkbox for every sign-in path on this screen. Inside the
              PIN form it could never remember a PIN-less profile, while a
              tick left over from a locked one silently applied a 30-day
              session to whatever was picked next. */}
          <Show when={!loading() && !loadFailed()}>
            <label class="login-remember">
              <input
                type="checkbox"
                checked={remember()}
                onChange={(e) => {
                  // aria-disabled + guard, not disabled: a busy checkbox would
                  // otherwise drop out of the tab order mid-sign-in.
                  if (busy()) return;
                  setRemember(e.currentTarget.checked);
                }}
                aria-disabled={busy() ? "true" : "false"}
              />
              Keep me signed in on this device for 30 days
            </label>
          </Show>

          {/* Both regions are mounted for the lifetime of the screen and only
              their text changes: a live region inserted together with its
              content is never announced (WCAG 4.1.3), which is why the old
              Show-wrapped alert was silent. <output> carries an implicit
              status role; .login-live collapses them while empty without
              leaving the accessibility tree (app.css). */}
          <output class="login-muted login-live">{statusText()}</output>
          <p class="login-error login-live" role="alert">
            <Show when={error() !== ""}>
              <Icon icon={TriangleAlert} size={16} decorative />
              <span>{error()}</span>
            </Show>
          </p>
        </section>
      </div>

      <footer class="login-colophon eyebrow">
        Local-first · No accounts · Yours
      </footer>
    </div>
  );
}
