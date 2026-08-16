// CustomThemeDialog: create/edit a custom theme with live preview.
//
//   - Rendered state is signals; operationController stays a plain let (never
//     rendered). Seeding-from-props-once is intentional: the dialog is
//     remounted per open (SettingsPanel gates it with a conditional), so the
//     initial signal values read props exactly once.
//   - Escape is a capture-phase window keydown listener: this dialog mounts
//     AFTER the reader route, so capture is what lets
//     stopImmediatePropagation beat Read's bubble-phase Escape handler
//     regardless of registration order.
//   - The dismiss layer is the shared .backdrop-dismiss button pattern,
//     matching CommandPalette and the library dialogs.
//   - Busy state is aria-disabled + handler-side guards, never a real
//     disabled attribute: a real disabled blurs the pressed control
//     mid-request, Enter-in-field included.
//   - The overlay is portaled to document.body. Rendered in place it lands
//     inside the settings panel, whose backdrop-filter blurs every descendant
//     AND makes the panel the containing block for position: fixed, so the
//     "whole screen" veil covered only the panel. The portal keeps the dialog
//     inside the panel's reactive owner (props, onclose, trap) while moving it
//     out of the panel's paint tree.
//   - Each color is editable as text as well as with the native picker:
//     Firefox's color picker has no hex/RGB entry field, so typing is the only
//     way to enter an exact value there.
//   - The picked palette publishes to lib/themePreview; App.tsx and the iframe
//     settings mapping read it and repaint. This dialog never paints the
//     document itself -- each surface keeps exactly one painter.
import {
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  onSettled,
  Show,
} from "solid-js";
import { Portal } from "@solidjs/web";
import { customThemes } from "~/lib/customThemes";
import { settings } from "~/lib/settings";
import { onAccentColor } from "~/lib/theme";
import { PREVIEW_THEME_ID, setThemePreview } from "~/lib/themePreview";
import { THEMES, autoAccent, themeGroupFor, type ThemeDef } from "~/lib/themes";
import type { CustomThemeInput } from "~/api/client";
import { trap } from "~/lib/focusTrap";
import Icon from "~/lib/Icon";
import { X, Trash2 } from "~/lib/icons";

interface Props {
  /** Colors to seed a new theme from (typically the active theme). */
  base: ThemeDef;
  /** When provided, edit this existing custom theme instead of creating one. */
  edit?: ThemeDef | null;
  onclose: () => void;
}

// Coerce any color to a 6-digit lowercase #rrggbb, the only form the native
// color inputs accept. Expands #rgb and falls back for anything unexpected.
function norm(hex: string, fallback: string): string {
  const s = hex.trim().replace(/^#/, "");
  if (/^[0-9a-fA-F]{3}$/.test(s)) {
    return `#${s[0]}${s[0]}${s[1]}${s[1]}${s[2]}${s[2]}`.toLowerCase();
  }
  if (/^[0-9a-fA-F]{6}$/.test(s)) return `#${s.toLowerCase()}`;
  return fallback;
}

// Parse typed text to #rrggbb, or null while it is not yet a complete color --
// a half-typed "#ab" must neither commit nor be rewritten under the caret.
// Accepts what people actually type or paste: #rgb, #rrggbb, a bare hex with no
// "#", and rgb()/rgba() in both the legacy comma and the modern space syntax,
// with 0-255 or percentage channels. Alpha parses but is dropped: a translucent
// page background would show the app through the reader.
function parseColorText(text: string): string | null {
  const s = text.trim();
  if (s === "") return null;
  const hex = norm(s, "");
  if (hex !== "") return hex;
  const call = /^rgba?\(([^)]+)\)$/i.exec(s);
  if (!call) return null;
  const parts = call[1].split(/[\s,/]+/).filter((p) => p !== "");
  if (parts.length < 3) return null;
  let out = "#";
  for (const part of parts.slice(0, 3)) {
    const pct = part.endsWith("%");
    const n = Number(pct ? part.slice(0, -1) : part);
    if (!Number.isFinite(n)) return null;
    const scaled = pct ? (n / 100) * 255 : n;
    const byte = Math.max(0, Math.min(255, Math.round(scaled)));
    out += byte.toString(16).padStart(2, "0");
  }
  return out;
}

const MAX_THEME_NAME_CHARS = 60;

// One color control: the native picker plus a text field, kept in sync. Module
// level so it closes over no dialog state. The draft signal holds raw
// keystrokes -- committing only parses, and only blur snaps the field back to
// the normalised value, so typing is never fought over by the formatter.
function ColorRow(props: {
  label: string;
  value: string;
  busy: boolean;
  onchange: (hex: string) => void;
}) {
  const [draft, setDraft] = createSignal<string | null>(null);
  const text = (): string => draft() ?? props.value;
  const invalid = (): boolean => {
    const raw = draft();
    return raw !== null && parseColorText(raw) === null;
  };

  return (
    <label class="ctd-field">
      <span>{props.label}</span>
      <div class="ctd-color-row">
        <input
          type="color"
          value={props.value}
          aria-label={`${props.label} color`}
          aria-disabled={props.busy ? "true" : "false"}
          onInput={(e) => {
            // The picker is authoritative again: drop any stale typed draft.
            setDraft(null);
            props.onchange(e.currentTarget.value);
          }}
        />
        <input
          type="text"
          class="ctd-color-text"
          value={text()}
          spellcheck={false}
          autocapitalize="off"
          autocomplete="off"
          aria-label={`${props.label} color value, hex or rgb()`}
          aria-invalid={invalid() ? "true" : undefined}
          readonly={props.busy}
          aria-disabled={props.busy ? "true" : "false"}
          onInput={(e) => {
            const raw = e.currentTarget.value;
            setDraft(raw);
            const parsed = parseColorText(raw);
            if (parsed) props.onchange(parsed);
          }}
          onBlur={() => setDraft(null)}
        />
      </div>
    </label>
  );
}

export default function CustomThemeDialog(props: Props) {
  // The dialog is remounted per open, so seeding local editing state from
  // props once is intentional.
  const seed = props.edit ?? props.base;
  const seedBg = norm(seed.bg, "#ffffff");
  const seedFg = norm(seed.fg, "#111111");

  const [name, setName] = createSignal(props.edit ? props.edit.label : "");
  const [bg, setBg] = createSignal(seedBg);
  const [fg, setFg] = createSignal(seedFg);
  const [accent, setAccent] = createSignal(
    norm(seed.accent, autoAccent(seedBg, seedFg)),
  );
  // Auto by default; when editing, stay auto if the stored accent equals what
  // auto would derive (the store resolves a blank accent to autoAccent, so
  // this round-trips the original "auto" choice without the raw record).
  const [auto, setAuto] = createSignal(
    !props.edit ||
      norm(props.edit.accent, "").toLowerCase() === autoAccent(seedBg, seedFg),
  );

  const [busy, setBusy] = createSignal(false);
  const [pendingAction, setPendingAction] = createSignal<
    "save" | "delete" | null
  >(null);
  const [deleteArmed, setDeleteArmed] = createSignal(false);
  // Mirrors SettingsPanel's resetArmed: the armed delete disarms on a timer,
  // so a stale armed state cannot fire on a stray click minutes later.
  let deleteArmTimer: ReturnType<typeof setTimeout> | undefined;
  const [nameDirty, setNameDirty] = createSignal(false);
  let operationController: AbortController | null = null;

  // Focus the name field on open. A focusing ref cannot do it: refs run while
  // the node is still detached, so focusing in a ref is a silent no-op and
  // focusTrap's fallback would take the first focusable in the sheet -- the
  // header close button, where Enter dismisses. Deferring one microtask lands
  // after the trap's own queueMicrotask; if this runs first instead, the
  // trap's !node.contains(activeElement) guard stands down.
  let nameEl: HTMLInputElement | undefined;
  onSettled(() => {
    queueMicrotask(() => nameEl?.focus());
  });

  const editing = createMemo(() => props.edit != null);
  const resolvedAccent = createMemo(() =>
    auto() ? autoAccent(bg(), fg()) : accent(),
  );
  const accentText = createMemo(() => onAccentColor(resolvedAccent()));
  const group = createMemo(() => themeGroupFor(bg()));
  const trimmedName = createMemo(() => name().trim());
  const nameError = createMemo(() =>
    trimmedName().length === 0
      ? "Enter a theme name."
      : Array.from(trimmedName()).length > MAX_THEME_NAME_CHARS
        ? `Theme names can contain at most ${MAX_THEME_NAME_CHARS} characters.`
        : "",
  );
  const visibleNameError = createMemo(() => (nameDirty() ? nameError() : ""));
  const canSave = createMemo(() => !busy() && nameError() === "");
  const closeLabel = createMemo(() =>
    pendingAction() === "delete"
      ? "Cancel deleting theme"
      : pendingAction() === "save"
        ? "Cancel saving theme"
        : "Close",
  );

  // Publish the in-progress palette so the app chrome (App.tsx) and the reader
  // frame (settings.iframe) repaint live while the colors are being picked.
  // The draft id never reaches settings.theme or the server. Clearing it on
  // unmount restores the saved theme and covers cancel, Escape, save and
  // delete alike: every one of those paths ends in props.onclose().
  createEffect(
    (): ThemeDef => ({
      id: PREVIEW_THEME_ID,
      label: trimmedName() || "Preview",
      group: group(),
      bg: bg(),
      fg: fg(),
      accent: resolvedAccent(),
    }),
    (def) => setThemePreview(def),
  );

  function beginOperation(action: "save" | "delete"): AbortController {
    const controller = new AbortController();
    operationController = controller;
    setPendingAction(action);
    setBusy(true);
    return controller;
  }

  function finishOperation(controller: AbortController): boolean {
    if (operationController !== controller || controller.signal.aborted) {
      return false;
    }
    operationController = null;
    setPendingAction(null);
    setBusy(false);
    return true;
  }

  function close(): void {
    operationController?.abort();
    operationController = null;
    props.onclose();
  }

  function onKeydown(e: KeyboardEvent): void {
    // An Escape that ends an IME composition is not a dismissal: the name
    // field is a text input, and this capture listener runs before any other
    // handler, so an unguarded consume would close the dialog mid-composition.
    if (e.isComposing) return;
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume so the reader / settings window handlers don't also act on it.
      e.stopImmediatePropagation();
      close();
    }
  }

  // Capture phase: this dialog mounts AFTER the reader route, so its bubble
  // listener would run last — Read's Escape action (close panel / navigate
  // back) would have already fired before stopImmediatePropagation could act.
  // Capture runs before every bubble-phase window listener regardless of
  // registration order.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    // Returned teardown, not onCleanup(): onCleanup inside onSettled throws
    // CLEANUP_IN_FORBIDDEN_SCOPE and the uncaught throw halts the reactive
    // system app-wide, taking the whole reader down with this dialog.
    return () => window.removeEventListener("keydown", onKeydown, true);
  });

  onCleanup(() => {
    operationController?.abort();
    operationController = null;
    if (deleteArmTimer !== undefined) clearTimeout(deleteArmTimer);
    // Drop the live preview. The dialog is remounted per open, so unmount is
    // the one point every dismissal path converges on.
    setThemePreview(null);
  });

  function toggleAuto(e: Event): void {
    const next = (e.currentTarget as HTMLInputElement).checked;
    setAuto(next);
    // Seed the manual picker from the current auto suggestion so turning the
    // override on starts from a sensible color rather than a stale one.
    if (!next) setAccent(autoAccent(bg(), fg()));
  }

  async function save(e: Event): Promise<void> {
    e.preventDefault();
    if (busy()) return;
    setNameDirty(true);
    if (nameError()) return;
    const input: CustomThemeInput = {
      name: trimmedName(),
      group: group(),
      bg: bg(),
      fg: fg(),
      accent: auto() ? "" : accent(),
    };
    const controller = beginOperation("save");
    const edit = props.edit;
    if (edit) {
      const def = await customThemes.update(edit.id, input, controller.signal);
      if (controller.signal.aborted) return;
      if (def) {
        operationController = null;
        props.onclose();
        return;
      }
    } else {
      const def = await customThemes.create(input, controller.signal);
      if (controller.signal.aborted) return;
      if (def) {
        // Apply the new theme immediately so the user sees their creation.
        settings.update({ theme: def.id });
        operationController = null;
        props.onclose();
        return;
      }
    }
    // create/update already surfaced a toast on failure; let the user retry.
    finishOperation(controller);
  }

  async function remove(): Promise<void> {
    const edit = props.edit;
    if (!edit || busy()) return;
    if (!deleteArmed()) {
      setDeleteArmed(true);
      if (deleteArmTimer !== undefined) clearTimeout(deleteArmTimer);
      deleteArmTimer = setTimeout(() => setDeleteArmed(false), 3000);
      return;
    }
    const controller = beginOperation("delete");
    const wasActive = settings.value.theme === edit.id;
    const ok = await customThemes.remove(edit.id, controller.signal);
    if (controller.signal.aborted) return;
    if (ok) {
      if (wasActive) {
        // Fall back to the first built-in sharing the deleted theme's group.
        const fallback =
          THEMES.find((th) => th.group === themeGroupFor(edit.bg))?.id ??
          THEMES[0].id;
        settings.update({ theme: fallback });
      }
      operationController = null;
      props.onclose();
      return;
    }
    finishOperation(controller);
  }

  return (
    <Portal>
      <div class="ctd-overlay" role="presentation">
        <button
          type="button"
          class="backdrop-dismiss"
          aria-label="Close"
          tabindex="-1"
          onClick={close}
        />
        <div
          class="ctd-sheet"
          role="dialog"
          tabindex="-1"
          aria-modal="true"
          aria-label={editing() ? "Edit custom theme" : "New custom theme"}
          ref={trap()}
        >
          <header class="ctd-head">
            <div class="ctd-head-text">
              <p class="eyebrow">Theme</p>
              <h2 class="display ctd-title">
                {editing() ? "Edit theme" : "New theme"}
              </h2>
            </div>
            <button
              class="icon-btn press ctd-close"
              aria-label={closeLabel()}
              onClick={close}
            >
              <Icon icon={X} size={18} labelFromParent />
            </button>
          </header>

          <form class="ctd-form" onSubmit={(e) => void save(e)}>
            <div
              class="ctd-preview"
              style={{ background: bg(), color: fg() }}
              aria-hidden="true"
            >
              <span class="ctd-preview-aa">Aa</span>
              <span class="ctd-preview-sample">The quick brown fox jumps.</span>
              <span
                class="ctd-preview-accent"
                style={{ background: resolvedAccent(), color: accentText() }}
              >
                Accent
              </span>
            </div>
            <p class="ctd-hint">
              Appears in your{" "}
              <strong>{group() === "light" ? "Light" : "Dark"}</strong> themes —
              set automatically from the background.
            </p>

            <label class="ctd-field">
              <span>Name</span>
              <input
                type="text"
                value={name()}
                maxlength={String(MAX_THEME_NAME_CHARS * 2)}
                placeholder="My theme"
                autocomplete="off"
                readonly={busy()}
                aria-disabled={busy() ? "true" : "false"}
                aria-invalid={visibleNameError() ? "true" : undefined}
                aria-describedby={
                  visibleNameError() ? "theme-name-error" : undefined
                }
                onInput={(e) => {
                  setName(e.currentTarget.value);
                  setNameDirty(true);
                }}
                ref={(el) => (nameEl = el)}
              />
              <Show when={visibleNameError()}>
                <small
                  id="theme-name-error"
                  class="ctd-field-error"
                  role="alert"
                >
                  {visibleNameError()}
                </small>
              </Show>
            </label>

            <div class="ctd-colors">
              <ColorRow
                label="Background"
                value={bg()}
                busy={busy()}
                onchange={setBg}
              />
              <ColorRow
                label="Text"
                value={fg()}
                busy={busy()}
                onchange={setFg}
              />
            </div>

            <label class="ctd-check">
              <input
                type="checkbox"
                checked={auto()}
                onChange={toggleAuto}
                aria-disabled={busy() ? "true" : "false"}
              />
              <span>
                Auto accent <small>(derive from your colors)</small>
              </span>
            </label>
            <Show when={!auto()}>
              <ColorRow
                label="Accent"
                value={accent()}
                busy={busy()}
                onchange={setAccent}
              />
            </Show>

            <div class="ctd-actions">
              <Show when={editing()}>
                <button
                  type="button"
                  class={[
                    "btn-ghost press ctd-danger-ghost",
                    { armed: deleteArmed() },
                  ]}
                  onClick={() => void remove()}
                  aria-disabled={busy() ? "true" : "false"}
                  aria-label={
                    deleteArmed()
                      ? `Confirm deleting ${trimmedName() || "custom theme"}`
                      : `Delete ${trimmedName() || "custom theme"}`
                  }
                >
                  <Icon icon={Trash2} size={16} decorative />
                  {deleteArmed() ? "Click again to delete" : "Delete"}
                </button>
              </Show>
              <span class="ctd-spacer" />
              <button type="button" class="btn-ghost press" onClick={close}>
                {pendingAction() === "delete"
                  ? "Cancel delete"
                  : pendingAction() === "save"
                    ? "Cancel save"
                    : "Cancel"}
              </button>
              <button
                type="submit"
                class="btn press"
                aria-disabled={!canSave() ? "true" : "false"}
              >
                {pendingAction() === "save"
                  ? "Saving…"
                  : editing()
                    ? "Save changes"
                    : "Create theme"}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Portal>
  );
}
