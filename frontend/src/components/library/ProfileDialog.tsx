// Clone / delete profile dialog. Ported from ProfileDialog.svelte.
//
// Solid 2.0 notes:
//   - onMount -> onSettled for the prerequisite fetch (names list or PIN
//     check); <svelte:window onkeydowncapture> -> a mount-scoped capture
//     listener.
//   - {@attach ...focus()} -> onSettled + queueMicrotask (a self-focusing
//     ref runs while the node is still detached and is a silent no-op);
//     no `as` casts anywhere.
//   - Submit freezes the profile name and PIN before the first await:
//     profileName is the reactive session.profile and deleteCurrent() nulls
//     it -- reading it after the await would interpolate "null" into the
//     toast (unchanged from the Svelte reasoning).
//   - The backdrop dismiss is the shared .backdrop-dismiss button (guarded by
//     !busy, mirroring the Svelte's conditional overlay click).
import { createMemo, createSignal, onSettled, Show } from "solid-js";
import { session } from "~/lib/session";
import { listProfiles } from "~/api/client";
import { getErrorMessage } from "~/lib/errors";
import { toast } from "~/lib/toast";
import { trap } from "~/lib/focusTrap";
import Icon from "~/lib/Icon";
import { TriangleAlert, X } from "~/lib/icons";

// The server additionally refuses Windows device names (validateProfileName
// in internal/api/auth.go): a profile name becomes a directory verbatim, and
// on Windows "nul" stats as an existing device, so a clone of one dies
// server-side after the client waved it through -- with a message naming
// rules the name satisfies. Same set Login carries; extracting a shared
// validator is the X-list item, not this batch.
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

interface Props {
  mode: "clone" | "delete";
  profileName: string;
  onclose: () => void;
}

export default function ProfileDialog(props: Props) {
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [prerequisiteError, setPrerequisiteError] = createSignal<string | null>(
    null,
  );
  const [checkingPrerequisite, setCheckingPrerequisite] = createSignal(false);

  // Clone fields.
  const [newName, setNewName] = createSignal("");
  const [newPin, setNewPin] = createSignal("");

  // Delete fields. hasPin = null while we're still loading whether the
  // current profile is PIN-protected (decides if the PIN field is required).
  const [confirmName, setConfirmName] = createSignal("");
  const [pin, setPin] = createSignal("");
  const [hasPin, setHasPin] = createSignal<boolean | null>(null);
  // Existing profile names, lowercased (clone mode only), for a
  // case-insensitive duplicate check; null while still loading.
  const [takenNames, setTakenNames] = createSignal<string[] | null>(null);

  // The mount fetch must not outlive the dialog, and a Retry overlapping a
  // live attempt must not let the older response win. Every other async
  // surface owns an AbortController (Login's boot fetch is the template);
  // this dialog dropping the signal listProfiles accepts was the odd one
  // out. The returned teardown is load-bearing: onCleanup() inside onSettled
  // throws, so the abort is returned. currentHasPin() takes no signal, so
  // the generation counter alone guards the delete arm's writes.
  let prerequisiteGeneration = 0;
  let prerequisiteAbort: AbortController | undefined;

  onSettled(() => {
    void loadPrerequisite();
    return () => {
      prerequisiteGeneration += 1;
      prerequisiteAbort?.abort();
    };
  });

  async function loadPrerequisite(): Promise<void> {
    const generation = (prerequisiteGeneration += 1);
    const superseded = (): boolean => generation !== prerequisiteGeneration;
    prerequisiteAbort?.abort();
    const controller = new AbortController();
    prerequisiteAbort = controller;
    setCheckingPrerequisite(true);
    setPrerequisiteError(null);
    if (props.mode === "clone") {
      // This list is a correctness gate, not optional decoration. Profile
      // names map to directories, so a case-only duplicate can alias the same
      // path on Windows even though SQLite treats the names as distinct.
      setTakenNames(null);
      try {
        const profiles = await listProfiles(controller.signal);
        if (superseded()) return;
        setTakenNames(profiles.map((profile) => profile.name.toLowerCase()));
      } catch (err) {
        if (superseded()) return;
        setPrerequisiteError(
          getErrorMessage(err, "Could not check existing profile names."),
        );
      } finally {
        if (!superseded()) setCheckingPrerequisite(false);
      }
      return;
    }

    // Deletion must fail closed. Treating a lookup failure as an unprotected
    // profile hides the PIN field and leaves protected profiles undeletable.
    setHasPin(null);
    try {
      const pinProtected = await session.currentHasPin();
      if (superseded()) return;
      setHasPin(pinProtected);
    } catch (err) {
      if (superseded()) return;
      setPrerequisiteError(
        getErrorMessage(err, "Could not verify this profile’s PIN protection."),
      );
    } finally {
      if (!superseded()) setCheckingPrerequisite(false);
    }
  }

  const trimmedNewName = createMemo(() => newName().trim());
  // Case-insensitive: profiles are stored as on-disk dirs, and two profiles
  // differing only by case is a footgun regardless. profileName is itself in
  // takenNames once loaded, so this also covers the current name. Clone
  // remains disabled until the list is available rather than failing this
  // check open.
  const nameTaken = createMemo(() => {
    const names = takenNames();
    return names !== null && names.includes(trimmedNewName().toLowerCase());
  });
  const nameValid = createMemo(() =>
    /^[a-zA-Z0-9](?:[a-zA-Z0-9 _-]{0,30}[a-zA-Z0-9])?$/.test(trimmedNewName()),
  );
  const nameError = createMemo(() => {
    const name = trimmedNewName();
    if (name.length === 0) return null;
    if (!nameValid()) {
      return "Use 1–32 characters: letters, digits, spaces, dashes, or underscores; start and end with a letter or digit.";
    }
    if (RESERVED_NAMES.has(name.toLowerCase())) {
      return `${name} is a name Windows reserves for a device. Pick another.`;
    }
    return nameTaken() ? "That name is already taken." : null;
  });
  const newPinError = createMemo(() =>
    newPin() !== "" && !/^\d{4,12}$/.test(newPin())
      ? "PIN must be 4–12 digits, or left empty."
      : null,
  );
  const cloneReady = createMemo(
    () =>
      takenNames() !== null &&
      trimmedNewName().length > 0 &&
      nameError() === null &&
      newPinError() === null,
  );
  // Require an exact name match, plus a PIN when the profile has one. While
  // hasPin is still loading (null) the delete stays disabled.
  const deleteReady = createMemo(
    () =>
      hasPin() !== null &&
      confirmName() === props.profileName &&
      (!hasPin() || pin().length > 0),
  );
  const canSubmit = createMemo(
    () => !busy() && (props.mode === "clone" ? cloneReady() : deleteReady()),
  );

  // Every message is announced from the one pre-mounted region below, never
  // from the visible paragraphs: a live region inserted in the same tick as
  // its text is not announced by NVDA or JAWS (b27, WCAG 4.1.3), and all
  // four Show-wrapped paragraphs here mount together with their text.
  // Freshest first: a submit failure outranks the field validators.
  const announcement = createMemo(
    () =>
      error() ??
      prerequisiteError() ??
      (props.mode === "clone" ? (nameError() ?? newPinError()) : null),
  );

  async function submit(e: Event): Promise<void> {
    e.preventDefault();
    if (!canSubmit()) return;
    setBusy(true);
    setError(null);
    try {
      if (props.mode === "clone") {
        const name = trimmedNewName();
        const submittedPin = newPin();
        await session.clone(name, submittedPin);
        toast.show(`Created a copy: “${name}”`);
        props.onclose();
      } else {
        // Snapshot the name first: profileName is the reactive
        // session.profile prop, and deleteCurrent() nulls it -- reading it
        // after the await would interpolate "null" into the toast.
        const name = props.profileName;
        const submittedPin = pin();
        await session.deleteCurrent(submittedPin);
        toast.show(`Deleted profile “${name}”`);
        // Own the teardown. deleteCurrent() can resolve WITHOUT clearing the
        // session (its epoch early return), so "session.profile is now null
        // and App unmounts this dialog" is not a guaranteed postcondition.
        // Closing and unblocking here makes the external unmount an
        // optimisation rather than the contract; without it that path leaves
        // a busy modal whose every exit is dead.
        setBusy(false);
        props.onclose();
      }
    } catch (err) {
      setError(getErrorMessage(err, "Something went wrong."));
      setBusy(false);
    }
  }

  function onKeydown(e: KeyboardEvent): void {
    // Capture beats the focused field, but a composing Escape belongs to the
    // IME candidate window and must remain entirely untouched.
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on it.
      e.stopImmediatePropagation();
      if (!busy()) props.onclose();
    }
  }

  // Capture phase: the dialog mounts after the page's own window key
  // listeners, so a bubble listener here runs last and can't pre-empt them;
  // capture runs first regardless of registration order. Attached only while
  // the dialog is mounted.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    return () => window.removeEventListener("keydown", onKeydown, true);
  });

  // Focus the field this mode exists for. A ref cannot do it: refs run while
  // the node is still detached (b28 probe), so ref={(el) => el.focus()} was
  // a silent no-op and focusTrap's fallback took the first focusable in the
  // sheet -- the header close button, where Enter dismisses. Deferring one
  // microtask lands after the trap's own queueMicrotask; if this runs first
  // instead, the trap's !node.contains(activeElement) guard stands down. The
  // mode never changes on a mounted instance, so one branch's element is
  // always undefined.
  let confirmNameEl: HTMLInputElement | undefined;
  let newNameEl: HTMLInputElement | undefined;
  onSettled(() => {
    queueMicrotask(() =>
      (props.mode === "clone" ? newNameEl : confirmNameEl)?.focus(),
    );
  });

  return (
    <div class="pd-overlay" role="presentation">
      <button
        type="button"
        class="backdrop-dismiss"
        aria-label="Close"
        tabindex="-1"
        onClick={() => {
          if (!busy()) props.onclose();
        }}
      />
      {/* eslint-disable jsx-a11y/prefer-tag-over-role -- div+role kept over a native <dialog>: visual parity with the Svelte original is the port's contract. */}
      <div
        class="pd-sheet"
        role="dialog"
        tabindex="-1"
        aria-modal="true"
        aria-label={props.mode === "clone" ? "Clone profile" : "Delete profile"}
        ref={trap()}
      >
        <header>
          <div class="pd-head-text">
            <p class="eyebrow">Profile</p>
            <h2 class="display">
              {props.mode === "clone" ? "Clone profile" : "Delete profile"}
            </h2>
          </div>
          <button
            class="icon-btn press pd-close"
            aria-label="Close"
            onClick={() => props.onclose()}
            disabled={busy()}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>
        </header>

        <form
          onSubmit={(e) => void submit(e)}
          aria-busy={busy() ? "true" : "false"}
        >
          <Show
            when={props.mode === "clone"}
            fallback={
              <>
                <div class="pd-warn">
                  <Icon icon={TriangleAlert} size={18} decorative />
                  <p>
                    This permanently deletes{" "}
                    <strong>{props.profileName}</strong> and all of its books,
                    reading progress, and settings. This can’t be undone.
                  </p>
                </div>
                <label class="pd-frow">
                  <span class="pd-lbl">
                    Type <strong>{props.profileName}</strong> to confirm
                  </span>
                  <input
                    class="field"
                    type="text"
                    value={confirmName()}
                    onInput={(e) => setConfirmName(e.currentTarget.value)}
                    autocomplete="off"
                    autocapitalize="off"
                    spellcheck="false"
                    disabled={busy()}
                    ref={(el) => (confirmNameEl = el)}
                  />
                </label>
                <Show when={hasPin()}>
                  <label class="pd-frow">
                    <span class="pd-lbl">PIN</span>
                    <input
                      class="field"
                      type="password"
                      value={pin()}
                      onInput={(e) => setPin(e.currentTarget.value)}
                      inputmode="numeric"
                      maxlength="12"
                      autocomplete="current-password"
                      disabled={busy()}
                    />
                  </label>
                </Show>
              </>
            }
          >
            <p class="pd-lede">
              Make a copy of <strong>{props.profileName}</strong> — its books,
              settings, and flairs are duplicated into a new profile. You stay
              signed in as {props.profileName}.
            </p>
            <label class="pd-frow">
              <span class="pd-lbl">New profile name</span>
              <input
                class="field"
                type="text"
                value={newName()}
                onInput={(e) => setNewName(e.currentTarget.value)}
                maxlength="32"
                autocomplete="off"
                placeholder={`${props.profileName} (copy)`}
                aria-invalid={nameError() !== null ? "true" : "false"}
                aria-describedby={
                  nameError() ? "profile-name-error" : undefined
                }
                disabled={busy()}
                ref={(el) => (newNameEl = el)}
              />
            </label>
            <Show when={nameError()}>
              {(message) => (
                <p class="pd-note" id="profile-name-error">
                  {message()}
                </p>
              )}
            </Show>
            <label class="pd-frow">
              <span class="pd-lbl">
                PIN for the copy <em>(optional)</em>
              </span>
              <input
                class="field"
                type="password"
                value={newPin()}
                onInput={(e) => setNewPin(e.currentTarget.value)}
                inputmode="numeric"
                maxlength="12"
                autocomplete="new-password"
                placeholder="4–12 digits"
                aria-invalid={newPinError() !== null ? "true" : "false"}
                aria-describedby={
                  newPinError() ? "profile-pin-error" : undefined
                }
                disabled={busy()}
              />
            </label>
            <Show when={newPinError()}>
              {(message) => (
                <p class="pd-note" id="profile-pin-error">
                  {message()}
                </p>
              )}
            </Show>
          </Show>

          <Show when={checkingPrerequisite()}>
            <p class="pd-prereq-status" role="status">
              {props.mode === "clone"
                ? "Checking existing profile names…"
                : "Checking PIN protection…"}
            </p>
          </Show>
          <Show when={!checkingPrerequisite() && prerequisiteError()}>
            {(message) => (
              <div class="pd-prereq-error">
                <p class="pd-error">{message()}</p>
                <button
                  type="button"
                  class="btn-ghost press pd-retry"
                  onClick={() => void loadPrerequisite()}
                  disabled={busy()}
                >
                  Retry
                </button>
              </div>
            )}
          </Show>

          <Show when={error()}>
            {(message) => <p class="pd-error">{message()}</p>}
          </Show>

          {/* Pre-mounted live region. Every visible message above is inserted
              in the same tick as its text, which NVDA and JAWS do not
              announce (b27, WCAG 4.1.3) -- this region exists from first
              paint and only its text changes, so the paragraphs carry no
              role="alert". */}
          <p class="sr-only" role="alert">
            {announcement() ?? ""}
          </p>

          <div class="pd-actions">
            <button
              type="button"
              class="btn-ghost press"
              onClick={() => props.onclose()}
              disabled={busy()}
            >
              Cancel
            </button>
            <button
              type="submit"
              class={props.mode === "delete" ? "pd-btn-del press" : "btn press"}
              disabled={!canSubmit()}
            >
              {props.mode === "clone"
                ? busy()
                  ? "Creating…"
                  : "Create copy"
                : busy()
                  ? "Deleting…"
                  : "Delete profile"}
            </button>
          </div>
        </form>
      </div>
      {/* eslint-enable jsx-a11y/prefer-tag-over-role */}
    </div>
  );
}
