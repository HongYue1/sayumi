// Profile menu in the library masthead: clone / delete / sign out behind a
// single trigger. Ported from ProfileMenu.svelte.
//
// Solid 2.0 notes:
//   - The conditional <svelte:window onpointerdown> becomes a compute/apply
//     createEffect that attaches the outside-dismiss listener only while the
//     menu is open (bubble phase: it must not consume the event, only
//     observe it).
//   - {@attach ...focus()} -> ref callbacks; bind:this -> ref assignments.
//   - No `as` casts (lint errors here): e.target and activeElement are
//     narrowed with instanceof instead.
//   - Class names get a .pm- prefix: .trigger/.item/.menu are shared by
//     convention across the library subtree's scoped styles and would collide
//     in the global sheet when ThemeDropdown/BookCard land.
import { createEffect, createSignal, Show } from "solid-js";
import { session } from "~/lib/session";
import { signOutWithFeedback } from "~/lib/signOut";
import Icon from "~/lib/Icon";
import { ChevronDown, Copy, LogOut, Trash2, User } from "~/lib/icons";

interface Props {
  onclone: () => void;
  ondelete: () => void;
}

export default function ProfileMenu(props: Props) {
  const [open, setOpen] = createSignal(false);
  let trigger: HTMLButtonElement | undefined;
  let menuEl: HTMLElement | undefined;

  function toggle(): void {
    setOpen(!open());
  }
  function close(restoreFocus = true): void {
    if (!open()) return;
    setOpen(false);
    if (restoreFocus) trigger?.focus();
  }

  // Dismiss on outside pointerdown. A fixed scrim can't be used here: the
  // sticky masthead's backdrop-filter establishes a containing block, which
  // clips a position:fixed scrim to the masthead box (so clicks on the shelf
  // below would never reach it). A window listener is container-proof.
  function onWindowPointerDown(e: PointerEvent): void {
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (menuEl?.contains(t) || trigger?.contains(t)) return;
    close(false);
  }

  // Escape from wherever focus happens to be while the menu is open. Bubble
  // phase, deliberately: every overlay that can stack above this menu
  // (.cmd-overlay, .shortcuts-overlay, .sd-overlay, .eb-overlay, .pd-overlay,
  // all z-index 60 against this menu's 21) registers a CAPTURE keydown
  // listener that calls stopImmediatePropagation, so an Escape belonging to a
  // surface stacked on top never reaches here. Capture would invert that: this
  // listener registers first, so it would run first and swallow the dialog's
  // dismissal. Menus bubble, dialogs capture -- the same split as BookCard.
  function onWindowKeyDown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    close();
  }

  // Dismiss when focus leaves the menu entirely. relatedTarget is the element
  // about to receive focus; a null relatedTarget means focus is leaving the
  // document (window blur, devtools) and must NOT close the menu, or an
  // alt-tab would drop it. No focus restore here -- focus is deliberately
  // going somewhere else.
  function onRootFocusOut(
    e: FocusEvent & { currentTarget: HTMLDivElement },
  ): void {
    const next = e.relatedTarget;
    if (!(next instanceof Node)) return;
    if (e.currentTarget.contains(next)) return;
    close(false);
  }

  createEffect(
    () => open(),
    (isOpen) => {
      if (!isOpen) return undefined;
      window.addEventListener("pointerdown", onWindowPointerDown);
      window.addEventListener("keydown", onWindowKeyDown);
      return () => {
        window.removeEventListener("pointerdown", onWindowPointerDown);
        window.removeEventListener("keydown", onWindowKeyDown);
      };
    },
  );

  // Move focus into the menu on open. The first item's self-focusing ref could
  // never do it: Solid runs element refs while the node is still detached, so
  // .focus() no-ops and the active element stays on the trigger -- which left
  // the roving arrow keys below unreachable and made aria-expanded assert a
  // focus move that never happened. Post-settle plus one microtask, matching
  // the BookCard fix.
  let menuGen = 0;
  createEffect(
    () => open(),
    (isOpen) => {
      // Bump on open AND close, so a queued focus for a menu that has since
      // closed is dropped instead of stealing focus back from the trigger.
      const gen = ++menuGen;
      if (!isOpen) return undefined;
      queueMicrotask(() => {
        if (gen !== menuGen) return;
        const el = menuEl;
        if (!el) return;
        const items = Array.from(
          el.querySelectorAll<HTMLButtonElement>(".pm-item"),
        );
        // Open on the item the markup nominates with tabindex 0; fall back to
        // the menu container so focus is at least inside the popover.
        const preferred = items.find(
          (it) => it.getAttribute("tabindex") === "0",
        );
        (preferred ?? items[0] ?? el).focus();
      });
      return undefined;
    },
  );

  function onMenuKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    if (e.key === "Escape") {
      // An Escape that ends an IME composition is not a dismissal -- it
      // cancels the candidate window. Same guard as EditBookDialog.
      if (e.isComposing) return;
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (e.key === "Tab") {
      // Tab must LEAVE the menu, never wrap inside it: WCAG 2.1.2 (No Keyboard
      // Trap) and the APG Menu Button pattern. Close and let the browser
      // continue the tab order from the restored trigger, exactly as BookCard
      // and Library's sort menu do.
      close();
      return;
    }
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "Home" &&
      e.key !== "End"
    ) {
      return;
    }
    // Roving focus across the menu items, matching ThemeDropdown / BookCard.
    const items = Array.from(
      e.currentTarget.querySelectorAll<HTMLButtonElement>(".pm-item"),
    );
    if (items.length === 0) return;
    e.preventDefault();
    e.stopPropagation();
    const active = document.activeElement;
    const cur =
      active instanceof HTMLButtonElement ? items.indexOf(active) : -1;
    let next: number;
    switch (e.key) {
      case "Home":
        next = 0;
        break;
      case "End":
        next = items.length - 1;
        break;
      case "ArrowDown":
        next = cur < 0 ? 0 : (cur + 1) % items.length;
        break;
      default:
        next =
          cur < 0 ? items.length - 1 : (cur - 1 + items.length) % items.length;
    }
    items[next].focus();
  }

  function pick(action: () => void): void {
    // Restore focus to the trigger BEFORE running the action. Clone/Delete
    // open a focusTrap'd dialog that snapshots document.activeElement on
    // mount and restores it on close; if we closed with restoreFocus=false
    // the menu item would be removed, activeElement would fall to <body>, and
    // the dialog would hand focus back to <body> instead of this trigger.
    close(true);
    action();
  }

  return (
    <div class="profile-menu" onFocusOut={onRootFocusOut}>
      <button
        ref={(el) => (trigger = el)}
        id="pm-trigger"
        class={["pm-trigger", { open: open() }]}
        aria-haspopup="menu"
        aria-expanded={open() ? "true" : "false"}
        aria-label={`Profile: ${session.profile ?? ""}`}
        onClick={toggle}
      >
        <Icon icon={User} size={15} class="pm-who-icon" decorative />
        <span class="pm-who" title={session.profile ?? ""}>
          {session.profile}
        </span>
        <Icon icon={ChevronDown} size={14} class="pm-caret" decorative />
      </button>

      <Show when={open()}>
        <div
          ref={(el) => (menuEl = el)}
          class="pm-menu paper"
          role="menu"
          tabindex="-1"
          aria-labelledby="pm-trigger"
          onKeyDown={onMenuKeydown}
        >
          <button
            class="pm-item"
            role="menuitem"
            tabindex="0"
            onClick={() => pick(props.onclone)}
          >
            <Icon icon={Copy} size={16} decorative />
            Clone profile…
          </button>
          <button
            class="pm-item danger"
            role="menuitem"
            tabindex="-1"
            onClick={() => pick(props.ondelete)}
          >
            <Icon icon={Trash2} size={16} decorative />
            Delete profile…
          </button>
          <hr class="pm-sep" aria-hidden="true" />
          <button
            class="pm-item"
            role="menuitem"
            tabindex="-1"
            onClick={() => pick(signOutWithFeedback)}
          >
            <Icon icon={LogOut} size={16} decorative />
            Sign out
          </button>
        </div>
      </Show>
    </div>
  );
}
