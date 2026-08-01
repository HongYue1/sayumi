// BookmarksPanel: bookmark list with inline label/note editing — Solid 2.0 port.
//
// Solid 2.0 notes:
//   - Rendered state is signals (editingId/editLabel/editComment/editError);
//     returnFocusId stays a plain let — it's consumed only by the edit
//     button's ref callback, never rendered.
//   - {@attach (el) => el.focus()} -> a ref callback on the label input: it
//     fires when the edit form mounts (editingId flips), which is exactly the
//     Svelte attach timing.
//   - {@attach restoreEditFocus} -> a ref callback on the row's edit button;
//     when editing ends the display branch remounts and the ref restores
//     focus without a tick/flush (refs run after the node is in the DOM).
//   - keyed #each (bm.id) -> <For> keyed by Bookmark object identity: the
//     store hands us stable references, so rows keep their DOM nodes.
//   - bind:value -> value + onInput (the error-clearing oninput folds into
//     the same handler).
import { createMemo, createSignal, For, Show } from "solid-js";
import type { Bookmark } from "~/api/client";
import Icon from "~/lib/Icon";
import { X, Pencil, Trash2 } from "~/lib/icons";

interface Props {
  bookmarks: Bookmark[];
  /** Resolves a chapter index to its TOC heading (null → fall back). */
  chapterTitle?: (chapter: number) => string | null;
  onnavigate: (bm: Bookmark) => void;
  ondelete: (id: string) => void;
  onupdate: (id: string, label: string, comment: string) => void;
  onclose: () => void;
}

const MAX_BOOKMARK_TEXT_BYTES = 2000;
const textEncoder = new TextEncoder();

export default function BookmarksPanel(props: Props) {
  const [editingId, setEditingId] = createSignal<string | null>(null);
  const [editLabel, setEditLabel] = createSignal("");
  const [editComment, setEditComment] = createSignal("");
  const [editError, setEditError] = createSignal("");
  let returnFocusId: string | null = null;

  const chapterTitle = (chapter: number): string | null =>
    (props.chapterTitle ?? (() => null))(chapter);

  const sorted = createMemo(() =>
    [...props.bookmarks].sort(
      (a, b) => a.chapter - b.chapter || a.percent - b.percent,
    ),
  );

  function bookmarkName(bm: Bookmark): string {
    // Prefer the user's label, then the chapter's real TOC heading.
    return bm.label || chapterTitle(bm.chapter) || `Chapter ${bm.chapter + 1}`;
  }

  function startEdit(bm: Bookmark): void {
    setEditingId(bm.id);
    setEditLabel(bm.label);
    setEditComment(bm.comment);
    setEditError("");
    returnFocusId = null;
  }

  function saveEdit(id: string): void {
    const label = editLabel().trim();
    const comment = editComment().trim();
    const labelBytes = textEncoder.encode(label).byteLength;
    const commentBytes = textEncoder.encode(comment).byteLength;
    if (labelBytes > MAX_BOOKMARK_TEXT_BYTES) {
      setEditError(
        `Label must be ${MAX_BOOKMARK_TEXT_BYTES} UTF-8 bytes or fewer (currently ${labelBytes}).`,
      );
      return;
    }
    if (commentBytes > MAX_BOOKMARK_TEXT_BYTES) {
      setEditError(
        `Note must be ${MAX_BOOKMARK_TEXT_BYTES} UTF-8 bytes or fewer (currently ${commentBytes}).`,
      );
      return;
    }
    props.onupdate(id, label, comment);
    finishEdit(id);
  }

  function finishEdit(id: string): void {
    returnFocusId = id;
    setEditingId(null);
    setEditError("");
  }

  // Ref on the row's edit button: when the display branch remounts after an
  // edit, put focus back on it (preventScroll keeps the list position).
  function restoreEditFocus(node: HTMLButtonElement, id: string): void {
    if (returnFocusId !== id) return;
    returnFocusId = null;
    node.focus({ preventScroll: true });
  }

  // Keep edit-mode keys local: Esc cancels (and is stopped from bubbling to the
  // reader's own Esc handler), Enter in the single-line label field saves. The
  // note textarea keeps Enter for newlines. Attached to the .bmp-edit WRAPPER
  // so it also covers the Save/Cancel buttons — with it only on the fields,
  // Escape while a button had focus bubbled to the reader's window handler,
  // which closed the whole panel and dropped the draft.
  function onEditKey(e: KeyboardEvent, id: string): void {
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.stopPropagation();
      e.preventDefault();
      finishEdit(id);
    } else if (e.key === "Enter" && e.target instanceof HTMLInputElement) {
      e.preventDefault();
      saveEdit(id);
    }
  }

  return (
    <div class="bmp">
      <header class="bmp-head">
        <div class="bmp-head-text">
          <p class="eyebrow">Reader</p>
          <h2 class="display bmp-title">Bookmarks</h2>
        </div>
        <button
          class="icon-btn press bmp-close"
          onClick={props.onclose}
          aria-label="Close bookmarks"
        >
          <Icon icon={X} size={18} />
        </button>
      </header>

      <div class="bmp-list">
        <Show
          when={sorted().length > 0}
          fallback={
            <p class="bmp-empty">
              No bookmarks yet. Press <kbd class="kbd">B</kbd> while reading to
              add one.
            </p>
          }
        >
          <ul class="bmp-items">
            <For each={sorted()}>
              {(bm) => (
                <li class="bmp-item">
                  <Show
                    when={editingId() === bm.id}
                    fallback={
                      <>
                        <span class="bmp-ribbon" aria-hidden="true" />
                        <button
                          class="bmp-open"
                          onClick={() => props.onnavigate(bm)}
                          aria-label={`Go to bookmark: ${bookmarkName(bm)}, chapter ${bm.chapter + 1}, ${Math.round(bm.percent * 100)}%`}
                        >
                          <span class="bmp-label">{bookmarkName(bm)}</span>
                          <span class="bmp-meta tnum">
                            Ch {bm.chapter + 1} · {Math.round(bm.percent * 100)}
                            %
                          </span>
                          <Show when={bm.comment}>
                            <span class="bmp-comment">{bm.comment}</span>
                          </Show>
                        </button>
                        <div class="bmp-actions">
                          <button
                            class="bmp-row-btn press"
                            onClick={() => startEdit(bm)}
                            aria-label={`Edit bookmark: ${bookmarkName(bm)}`}
                            ref={(node) => restoreEditFocus(node, bm.id)}
                          >
                            <Icon icon={Pencil} size={15} />
                          </button>
                          <button
                            class="bmp-row-btn press danger"
                            onClick={() => props.ondelete(bm.id)}
                            aria-label={`Delete bookmark: ${bookmarkName(bm)}`}
                          >
                            <Icon icon={Trash2} size={15} />
                          </button>
                        </div>
                      </>
                    }
                  >
                    <div
                      class="bmp-edit"
                      onKeyDown={(e) => onEditKey(e, bm.id)}
                    >
                      <input
                        class="field"
                        value={editLabel()}
                        placeholder="Label"
                        aria-label="Bookmark label"
                        aria-describedby={
                          editError()
                            ? `bookmark-edit-error-${bm.id}`
                            : undefined
                        }
                        maxlength={String(MAX_BOOKMARK_TEXT_BYTES)}
                        onInput={(e) => {
                          setEditLabel(e.currentTarget.value);
                          setEditError("");
                        }}
                        ref={(el) => {
                          el.focus();
                        }}
                      />
                      <textarea
                        class="field"
                        value={editComment()}
                        placeholder="Note…"
                        rows={3}
                        aria-label="Bookmark note"
                        aria-describedby={
                          editError()
                            ? `bookmark-edit-error-${bm.id}`
                            : undefined
                        }
                        maxlength={String(MAX_BOOKMARK_TEXT_BYTES)}
                        onInput={(e) => {
                          setEditComment(e.currentTarget.value);
                          setEditError("");
                        }}
                      />
                      <Show when={editError()}>
                        <p
                          class="bmp-edit-error"
                          id={`bookmark-edit-error-${bm.id}`}
                          role="alert"
                        >
                          {editError()}
                        </p>
                      </Show>
                      <div class="bmp-actions bmp-edit-actions">
                        <button
                          class="btn press bmp-small"
                          onClick={() => saveEdit(bm.id)}
                        >
                          Save
                        </button>
                        <button
                          class="btn-ghost press bmp-small"
                          onClick={() => finishEdit(bm.id)}
                        >
                          Cancel
                        </button>
                      </div>
                    </div>
                  </Show>
                </li>
              )}
            </For>
          </ul>
        </Show>
      </div>
    </div>
  );
}
