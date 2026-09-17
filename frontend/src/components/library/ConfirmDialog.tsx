// ConfirmDialog: the shelf's one confirmation surface for destructive actions.
//
// It replaces the two native confirm() calls (delete a book, delete a custom
// flair). A native confirm is unthemed and unstyleable, blocks the whole page
// while it is up, and is suppressible -- once the browser offers "prevent this
// page from creating more dialogs" it returns false forever, so the
// destructive path silently stops working with nothing on screen to say so.
// It also cannot be driven from the DOM, which is why both call sites could
// only be tested by stubbing window.confirm.
//
// Chrome, focus hand-off, Escape and the aria-disabled doctrine are
// ProfileDialog's; the .cfm-* classes ride on that dialog's rules in app.css
// instead of restating the modal styling.
import { onSettled } from "solid-js";
import Icon from "~/lib/Icon";
import { TriangleAlert, X } from "~/lib/icons";
import { trap } from "~/lib/focusTrap";

/** What a host asks for. `onconfirm` runs on the destructive path only. */
export interface ConfirmRequest {
  /** Kicker above the title: the kind of thing being acted on. */
  eyebrow: string;
  title: string;
  /** The consequence, in plain prose. Tied to the dialog as its description. */
  message: string;
  confirmLabel: string;
  onconfirm: () => void;
}

interface Props extends ConfirmRequest {
  onclose: () => void;
}

// The description needs an id to point at, and a hardcoded one collides the
// moment two of these are ever mounted together. Minted per instance instead.
let instances = 0;

export default function ConfirmDialog(props: Props) {
  const messageId = `cfm-message-${++instances}`;

  function accept(): void {
    props.onconfirm();
    // One exit per outcome: the dialog owns dismissal on both paths, so a host
    // only has to drop its pending request in onclose.
    props.onclose();
  }

  function onKeydown(e: KeyboardEvent): void {
    // Capture beats the focused button, but a composing Escape belongs to the
    // IME candidate window and must remain entirely untouched.
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume it, or the library/reader window handlers act on it too.
      e.stopImmediatePropagation();
      props.onclose();
    }
  }

  // Capture phase, attached only while mounted: this dialog mounts after the
  // page's own window key listeners, so a bubble listener here would run last
  // and could never pre-empt them.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    return () => window.removeEventListener("keydown", onKeydown, true);
  });

  // Land on Cancel rather than on the destructive button: Enter or Space on a
  // dialog that just appeared must not be the delete. Without this, focusTrap
  // takes the first focusable in the sheet, which is the header close button.
  // A ref cannot do it -- refs run while the node is still detached -- and the
  // microtask defers past the trap's own queueMicrotask, whose
  // !node.contains(activeElement) guard then stands down.
  let cancelEl: HTMLButtonElement | undefined;
  onSettled(() => {
    queueMicrotask(() => cancelEl?.focus());
  });

  return (
    <div class="cfm-overlay" role="presentation">
      <button
        type="button"
        class="backdrop-dismiss"
        aria-label="Close"
        tabindex="-1"
        onClick={() => props.onclose()}
      />
      {/* div+role kept over a native <dialog>: visual parity with the established design is the port's contract. */}
      <div
        class="cfm-sheet"
        role="dialog"
        tabindex="-1"
        aria-modal="true"
        aria-label={props.title}
        aria-describedby={messageId}
        ref={trap()}
      >
        <header>
          <div class="cfm-head-text">
            <p class="eyebrow">{props.eyebrow}</p>
            <h2 class="display">{props.title}</h2>
          </div>
          <button
            type="button"
            class="icon-btn press cfm-close"
            aria-label="Close"
            onClick={() => props.onclose()}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>
        </header>

        <div class="cfm-body">
          <div class="cfm-warn">
            <Icon icon={TriangleAlert} size={18} decorative />
            <p id={messageId}>{props.message}</p>
          </div>

          <div class="cfm-actions">
            <button
              type="button"
              class="btn-ghost press"
              onClick={() => props.onclose()}
              ref={(el) => (cancelEl = el)}
            >
              Cancel
            </button>
            <button type="button" class="cfm-btn-del press" onClick={accept}>
              {props.confirmLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
