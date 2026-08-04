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
import Icon from "~/lib/Icon";
import { ChevronDown, Copy, LogOut, Trash2, User } from "~/lib/icons";

interface Props {
  onclone: () => void;
  ondelete: () => void;
}

function signOut(): void {
  // logout clears local session state in its finally block even if the
  // request fails; consume that rejection after cleanup so it never becomes
  // unhandled.
  void session.logout().catch(() => undefined);
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

  createEffect(
    () => open(),
    (isOpen) => {
      if (!isOpen) return undefined;
      window.addEventListener("pointerdown", onWindowPointerDown);
      return () =>
        window.removeEventListener("pointerdown", onWindowPointerDown);
    },
  );

  function onMenuKeydown(
    e: KeyboardEvent & { currentTarget: HTMLDivElement },
  ): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
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
    <div class="profile-menu">
      <button
        ref={(el) => (trigger = el)}
        class={["pm-trigger", { open: open() }]}
        aria-haspopup="menu"
        aria-expanded={open() ? "true" : "false"}
        aria-label={`Profile: ${session.profile ?? ""}`}
        onClick={toggle}
      >
        <Icon icon={User} size={15} class="pm-who-icon" />
        <span class="pm-who" title={session.profile ?? ""}>
          {session.profile}
        </span>
        <Icon icon={ChevronDown} size={14} class="pm-caret" />
      </button>

      <Show when={open()}>
        <div
          ref={(el) => (menuEl = el)}
          class="pm-menu paper"
          role="menu"
          tabindex="-1"
          aria-label="Profile"
          onKeyDown={onMenuKeydown}
        >
          <button
            class="pm-item"
            role="menuitem"
            tabindex="0"
            onClick={() => pick(props.onclone)}
            ref={(el) => el.focus()}
          >
            <Icon icon={Copy} size={16} />
            Clone profile…
          </button>
          <button
            class="pm-item danger"
            role="menuitem"
            tabindex="-1"
            onClick={() => pick(props.ondelete)}
          >
            <Icon icon={Trash2} size={16} />
            Delete profile…
          </button>
          <hr class="pm-sep" aria-hidden="true" />
          <button
            class="pm-item"
            role="menuitem"
            tabindex="-1"
            onClick={() => pick(signOut)}
          >
            <Icon icon={LogOut} size={16} />
            Sign out
          </button>
        </div>
      </Show>
    </div>
  );
}
