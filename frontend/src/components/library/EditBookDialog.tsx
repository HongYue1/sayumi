// Edit-book dialog: staged metadata + cover save.
//
//   - Signals initialize from props once; the saved* baselines advance only
//     after a successful stage.
//   - The file input's event is typed via an intersection, not an `as` cast
//     (lint).
//   - Every dismiss path -- Close, Cancel, Escape and the shared
//     .backdrop-dismiss button -- goes through requestClose, inert while busy.
//   - submit() is serialised by a plain mirror rather than the busy signal
//     (two activations in the same tick both read its pre-write value), and
//     it ends at finish(), which clears the busy state BEFORE onclose(). A
//     host that does not unmount is then left with a live dialog instead of
//     one whose every exit is dead -- ProfileDialog's delete arm documents
//     that hazard -- and the same latch stops the cleared state from
//     re-arming a save that already succeeded.
import { createMemo, createSignal, onSettled, Show } from "solid-js";
import { getCoverUrl, type BookMeta } from "~/api/client";
import { getErrorMessage } from "~/lib/errors";
import { library } from "~/lib/library";
import { toast } from "~/lib/toast";
import { trap } from "~/lib/focusTrap";
import Icon from "~/lib/Icon";
import { ImageUp, X } from "~/lib/icons";

const MAX_META_BYTES = 512;
const MAX_COVER_BYTES = 20 * 1024 * 1024;
const AUTHOR_TOO_LONG = "Author is too long (512-byte limit).";
const textEncoder = new TextEncoder();

interface Props {
  book: BookMeta;
  onclose: () => void;
}

export default function EditBookDialog(props: Props) {
  // Start from the values at open, then advance these baselines after a
  // successful metadata stage. If a following cover upload fails, the dialog
  // retries only that failed stage instead of presenting saved details as dirty.
  const [savedTitle, setSavedTitle] = createSignal(props.book.title);
  const [savedAuthor, setSavedAuthor] = createSignal(props.book.author);

  const [title, setTitle] = createSignal(props.book.title);
  const [author, setAuthor] = createSignal(props.book.author);

  const [coverFile, setCoverFile] = createSignal<File | null>(null);
  const [coverPreview, setCoverPreview] = createSignal<string | null>(null);

  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [coverPickError, setCoverPickError] = createSignal<string | null>(null);

  const trimmedTitle = createMemo(() => title().trim());
  const trimmedAuthor = createMemo(() => author().trim());
  const titleTooLong = createMemo(
    () => textEncoder.encode(trimmedTitle()).byteLength > MAX_META_BYTES,
  );
  const authorTooLong = createMemo(
    () => textEncoder.encode(trimmedAuthor()).byteLength > MAX_META_BYTES,
  );
  const titleError = createMemo(() =>
    trimmedTitle().length === 0
      ? "Title can’t be empty."
      : titleTooLong()
        ? "Title is too long (512-byte limit)."
        : null,
  );
  const dirty = createMemo(
    () =>
      trimmedTitle() !== savedTitle() ||
      trimmedAuthor() !== savedAuthor() ||
      coverFile() !== null,
  );
  const canSubmit = createMemo(
    () => !busy() && titleError() === null && !authorTooLong() && dirty(),
  );

  // Every message is announced from the one pre-mounted region below, never
  // from the visible paragraphs. Freshest first: a submit failure outranks the
  // field validators.
  const announcement = createMemo(
    () =>
      error() ??
      coverPickError() ??
      titleError() ??
      (authorTooLong() ? AUTHOR_TOO_LONG : null),
  );

  // Focus the field this dialog exists to edit. A ref cannot do it: refs run
  // while the node is still detached, so ref={(el) => el.focus()}
  // was a silent no-op and focusTrap's fallback took the first focusable in the
  // sheet -- the header close button, where Enter dismisses. Deferring one
  // microtask lands after the trap's own queueMicrotask; if this runs first
  // instead, the trap's !node.contains(activeElement) guard stands down.
  let titleEl: HTMLInputElement | undefined;
  onSettled(() => {
    queueMicrotask(() => titleEl?.focus());
    // The chosen file's object URL is revoked when replaced (below) and on
    // destroy, so a dialog opened/closed repeatedly doesn't leak blob URLs.
    // Returned teardown, not onCleanup(): 2.0 pairs component setup and
    // teardown in one onSettled and keeps onCleanup for primitive internals.
    return () => {
      const preview = coverPreview();
      if (preview) URL.revokeObjectURL(preview);
    };
  });

  function onCoverPick(e: Event & { currentTarget: HTMLInputElement }): void {
    const input = e.currentTarget;
    // aria-disabled leaves the picker live (see requestClose), so the refusal
    // belongs here: submit() froze its cover when the save began, so a pick
    // landing now would preview a file no request is going to upload.
    if (busy()) {
      input.value = "";
      return;
    }
    const file = input.files?.[0] ?? null;
    if (!file) return;
    setError(null);
    // A rejected pick leaves an already-staged cover alone: discarding it threw
    // away a good selection and said only "choose a JPEG", with the preview
    // silently reverting to the book's existing cover. Clearing the input's
    // value is enough to let the same file be re-picked.
    if (!/^image\/(jpeg|png|webp)$/.test(file.type)) {
      setCoverPickError("Choose a JPEG, PNG, or WebP image.");
      input.value = "";
      return;
    }
    if (file.size > MAX_COVER_BYTES) {
      setCoverPickError("Choose an image no larger than 20 MB.");
      input.value = "";
      return;
    }
    setCoverPickError(null);
    const preview = coverPreview();
    if (preview) URL.revokeObjectURL(preview);
    setCoverFile(file);
    setCoverPreview(URL.createObjectURL(file));
  }

  const currentCover = createMemo(
    () =>
      coverPreview() ??
      (props.book.hasCover
        ? getCoverUrl(props.book.id, props.book.updatedAt)
        : null),
  );

  // See the header: canSubmit() reads the busy signal, so it cannot
  // serialise this on its own.
  let submitting = false;
  let closed = false;

  // The one terminal exit: unblock every dismissal, latch the save shut, and
  // only then hand off to the host.
  function finish(): void {
    closed = true;
    submitting = false;
    setBusy(false);
    props.onclose();
  }

  async function submit(e: Event): Promise<void> {
    e.preventDefault();
    if (submitting || closed || !canSubmit()) return;
    submitting = true;

    // Freeze one coherent submission. The controls below refuse input while
    // busy, but these snapshots also prevent a late file-picker event from
    // changing which values an already-running save commits.
    const submittedTitle = trimmedTitle();
    const submittedAuthor = trimmedAuthor();
    const submittedCover = coverFile();
    let savedDetailsThisAttempt = false;

    setBusy(true);
    setError(null);
    setCoverPickError(null);
    try {
      const patch: { title?: string; author?: string } = {};
      if (submittedTitle !== savedTitle()) patch.title = submittedTitle;
      if (submittedAuthor !== savedAuthor()) patch.author = submittedAuthor;
      if (patch.title !== undefined || patch.author !== undefined) {
        await library.editMetadata(props.book.id, patch);
        setSavedTitle(submittedTitle);
        setSavedAuthor(submittedAuthor);
        savedDetailsThisAttempt = true;
      }
      if (submittedCover) {
        await library.replaceCover(props.book.id, submittedCover);
      }
      toast.show("Saved changes");
      finish();
    } catch (err) {
      const message = getErrorMessage(err, "Something went wrong.");
      setError(
        savedDetailsThisAttempt && submittedCover
          ? `Book details were saved, but the cover could not be replaced: ${message}`
          : message,
      );
      submitting = false;
      setBusy(false);
    }
  }

  // Close and Cancel carry aria-disabled rather than disabled: a real
  // attribute blurs whichever one holds focus the moment a save starts,
  // dropping the keyboard user to body inside the trap. That leaves this as
  // the only thing refusing a dismiss mid-save, so every path routes here.
  function requestClose(): void {
    if (busy()) return;
    props.onclose();
  }

  function onKeydown(e: KeyboardEvent): void {
    // An IME uses Escape to abandon a composition, and capture at window beats
    // the field, so without this the dialog closed and dropped the edit
    // (BookmarksPanel, SearchPanel and TocPanel guard the same way).
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader/library window key handlers don't also act on it.
      e.stopImmediatePropagation();
      requestClose();
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

  return (
    <div class="eb-overlay" role="presentation">
      <button
        type="button"
        class="backdrop-dismiss"
        aria-label="Close"
        tabindex="-1"
        onClick={requestClose}
      />
      {/* div+role kept over a native <dialog>: visual parity with the established design is the port's contract. */}
      <div
        class="eb-sheet"
        role="dialog"
        tabindex="-1"
        aria-modal="true"
        aria-label="Edit book"
        ref={trap()}
      >
        <header>
          <div class="eb-head-text">
            <p class="eyebrow">Library</p>
            <h2 class="display">Edit book</h2>
          </div>
          <button
            class="icon-btn press eb-close"
            aria-label="Close"
            aria-disabled={busy() ? "true" : "false"}
            onClick={requestClose}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>
        </header>

        <form
          onSubmit={(e) => void submit(e)}
          aria-busy={busy() ? "true" : "false"}
        >
          <div class="eb-cover-row">
            <div class="eb-cover-preview">
              <Show
                when={currentCover()}
                fallback={
                  <div class="eb-cover-placeholder display">{title()}</div>
                }
              >
                {(src) => <img src={src()} alt="" />}
              </Show>
            </div>
            <div class="eb-cover-actions">
              <label
                class={["btn-ghost press eb-file-btn", { disabled: busy() }]}
              >
                <Icon icon={ImageUp} size={16} decorative />
                {coverFile() ? "Change image" : "Replace cover"}
                <input
                  class="eb-file-input"
                  type="file"
                  accept="image/jpeg,image/png,image/webp"
                  aria-label={
                    coverFile() ? "Change cover image" : "Replace cover"
                  }
                  aria-invalid={coverPickError() !== null ? "true" : "false"}
                  aria-describedby={
                    coverPickError()
                      ? "cover-hint cover-pick-error"
                      : "cover-hint"
                  }
                  aria-disabled={busy() ? "true" : "false"}
                  onChange={onCoverPick}
                />
              </label>
              <Show when={coverFile()}>
                {(f) => (
                  <p class="eb-cover-name" title={f().name}>
                    {f().name}
                  </p>
                )}
              </Show>
              <p class="eb-hint" id="cover-hint">
                JPEG, PNG, or WebP · up to 20 MB · resized on save.
              </p>
              <Show when={coverPickError()}>
                {(message) => (
                  <p class="eb-error" id="cover-pick-error">
                    {message()}
                  </p>
                )}
              </Show>
            </div>
          </div>

          <label class="eb-frow">
            <span class="eb-lbl">Title</span>
            <input
              class="field"
              type="text"
              value={title()}
              onInput={(e) => setTitle(e.currentTarget.value)}
              maxlength="512"
              autocomplete="off"
              aria-invalid={titleError() !== null ? "true" : "false"}
              aria-describedby={titleError() ? "book-title-error" : undefined}
              readonly={busy()}
              aria-disabled={busy() ? "true" : "false"}
              ref={(el) => (titleEl = el)}
            />
          </label>
          {/* Always mounted and hidden when clean, so aria-invalid, the
              describedby link, and the error text can never disagree. A book
              with no title -- which the importer allows and PATCH then
              rejects -- is the case that needs it: the error must vanish the
              moment the field validates. */}
          <p
            class="eb-note"
            id="book-title-error"
            hidden={titleError() === null}
          >
            {titleError() ?? ""}
          </p>

          <label class="eb-frow">
            <span class="eb-lbl">Author</span>
            <input
              class="field"
              type="text"
              value={author()}
              onInput={(e) => setAuthor(e.currentTarget.value)}
              maxlength="512"
              autocomplete="off"
              aria-invalid={authorTooLong() ? "true" : "false"}
              aria-describedby={
                authorTooLong() ? "book-author-error" : undefined
              }
              readonly={busy()}
              aria-disabled={busy() ? "true" : "false"}
            />
          </label>
          <p class="eb-note" id="book-author-error" hidden={!authorTooLong()}>
            {AUTHOR_TOO_LONG}
          </p>

          <Show when={error()}>
            {(message) => <p class="eb-error">{message()}</p>}
          </Show>

          {/* Pre-mounted live region. Every visible message above is inserted
              in the same tick as its text, which NVDA and JAWS do not announce
              (WCAG 4.1.3) -- this region exists from first paint and only
              its text changes, so the paragraphs carry no role="alert". */}
          <p class="sr-only" role="alert">
            {announcement() ?? ""}
          </p>

          <div class="eb-actions">
            <button
              type="button"
              class="btn-ghost press eb-cancel"
              aria-disabled={busy() ? "true" : "false"}
              onClick={requestClose}
            >
              Cancel
            </button>
            {/* aria-disabled, not disabled: a real disabled attribute blurs
                whatever holds focus the moment the save starts, which dropped
                the keyboard user out of the dialog for the whole request and
                left them nowhere on failure. submit() holds the guard. */}
            <button
              type="submit"
              class="btn press eb-save"
              aria-disabled={!canSubmit() ? "true" : "false"}
            >
              {busy() ? "Saving…" : "Save changes"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
