// SettingsPanel: reader settings surface — presets, themes, fonts, text,
// layout, chapter titles, book styling — Solid 2.0 port.
//
// Solid 2.0 notes:
//   - Rendered state is signals; resetTimer stays a plain let. `s` is a plain
//     thunk over settings.value: that getter hands back the store node itself,
//     a stable reference, so a memo over it could only ever compute once --
//     the property reads at each s().foo call site are what track.
//   - The two Svelte snippets become module-level components (AutoRow,
//     Swatch): neither closes over panel state — everything arrives via props
//     (unicorn consistent-function-scoping).
//   - The CustomThemeDialog mount gate `{#if editor}` -> <Show when={editor()}>
//     with a function child, so the dialog remounts per open and can seed its
//     local state from props once (documented in that component).
//   - onMount -> onSettled; bind:value -> value + onInput; class:active ->
//     class={[...]}; style: -> style objects; aria-pressed gets "true"/"false"
//     strings (EnumeratedPseudoBoolean).
import {
  createMemo,
  createSignal,
  For,
  onCleanup,
  onSettled,
  Show,
} from "solid-js";
import { settings, DEFAULT_USER_SETTINGS } from "~/lib/settings";
import { THEMES, getTheme, isBuiltInTheme, type ThemeDef } from "~/lib/themes";
import { customThemes } from "~/lib/customThemes";
import CustomThemeDialog from "./CustomThemeDialog";
import { READER_FONTS, getFontById } from "~/lib/fonts";
import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry";
import { toast } from "~/lib/toast";
import { router } from "~/lib/router";
import { SPECIMEN_BOOK_ID } from "~/lib/specimen";
import {
  getPresets,
  createPreset,
  deletePreset,
  type UserSettings,
  type SettingsPreset,
} from "~/api/client";
import Icon from "~/lib/Icon";
import { X, Plus, Pencil } from "~/lib/icons";
import type { FrameModeFallback } from "~/lib/frameMessages";

interface Props {
  onclose: () => void;
  /** Mode the frame is actually rendering, not merely the saved preference. */
  effectiveMode: UserSettings["displayMode"];
  modeFallback: FrameModeFallback;
}

const MODES: { id: UserSettings["displayMode"]; label: string }[] = [
  { id: "scroll", label: "Scroll" },
  { id: "paged", label: "Single page" },
  { id: "paged-two", label: "Two pages" },
];

// null = inherit the book's own heading alignment.
const TITLE_ALIGNS: {
  id: UserSettings["chapterTitleAlign"];
  label: string;
}[] = [
  { id: null, label: "Auto" },
  { id: "left", label: "Left" },
  { id: "center", label: "Center" },
  { id: "right", label: "Right" },
];

const ROLES: {
  key: "regular" | "italic" | "bold" | "boldItalic";
  label: string;
}[] = [
  { key: "regular", label: "Regular" },
  { key: "italic", label: "Italic" },
  { key: "bold", label: "Bold" },
  { key: "boldItalic", label: "Bold Italic" },
];

// A variable family emits ONE 100-900 @font-face per axis (buildUserFontFaces
// in lib/readerFontFaces): the upright file covers regular+bold and the italic
// file covers italic+bold-italic, so a bold/bold-italic file pick would be
// persisted and then silently dropped from the generated CSS. Offer only the
// axis roles for those families.
const AXIS_ROLES = ROLES.filter(
  (r) => r.key === "regular" || r.key === "italic",
);

// Heading levels for the optional per-heading size overrides.
const HEADERS: {
  key: "h1Size" | "h2Size" | "h3Size" | "h4Size" | "h5Size" | "h6Size";
  label: string;
}[] = [
  { key: "h1Size", label: "H1" },
  { key: "h2Size", label: "H2" },
  { key: "h3Size", label: "H3" },
  { key: "h4Size", label: "H4" },
  { key: "h5Size", label: "H5" },
  { key: "h6Size", label: "H6" },
];

// Named CSS weights for the weight sliders' value readout.
const WEIGHT_NAMES: Record<number, string> = {
  100: "Thin",
  200: "Extra-light",
  300: "Light",
  400: "Normal",
  500: "Medium",
  600: "Semibold",
  700: "Bold",
  800: "Extra-bold",
  900: "Black",
};
function weightName(v: number | null): string {
  if (v === null) return "";
  return WEIGHT_NAMES[v] ?? "";
}

function set<K extends keyof UserSettings>(
  key: K,
  value: UserSettings[K],
): void {
  settings.update({ [key]: value } as Partial<UserSettings>);
}

// Opens the built-in typography specimen in the reader so these settings can
// be tuned against rich sample text. Navigation remounts the reader (App keys
// it on the book id), which closes this panel. When the specimen is already
// the open book the navigate is a no-op the router reports as false (no
// hashchange fires — see lib/router.ts), so nothing remounts: close directly.
function openSpecimen(onclose: () => void): void {
  if (!router.navigate(`/read/${encodeURIComponent(SPECIMEN_BOOK_ID)}`))
    onclose();
}

/** Numeric row with an "Auto" toggle for nullable settings. */
interface AutoRowProps {
  label: string;
  value: number | null;
  min: number;
  max: number;
  step: number;
  fallback: number;
  unit: string;
  apply: (v: number | null) => void;
  disabledReason?: string | null;
  headNote?: (v: number | null) => string;
}

function AutoRow(p: AutoRowProps) {
  // Computed once per read so an empty note renders nothing at all (no orphan
  // " \u00b7 " separator) and so Auto rows can still carry one.
  const headNote = (): string => (p.headNote ? p.headNote(p.value) : "");
  return (
    <div class={["stp-row", { "stp-row-disabled": !!p.disabledReason }]}>
      <div class="stp-row-head">
        <span class="stp-label">
          {p.label}
          {headNote() ? (
            <span class="stp-head-note"> · {headNote()}</span>
          ) : null}
        </span>
        <Show
          when={p.disabledReason}
          fallback={
            <label class="stp-auto">
              <input
                type="checkbox"
                checked={p.value === null}
                aria-label={`Auto ${p.label}`}
                onChange={(e) =>
                  p.apply(e.currentTarget.checked ? null : p.fallback)
                }
              />
              Auto
            </label>
          }
        >
          <span class="stp-hint">{p.disabledReason}</span>
        </Show>
      </div>
      <div class="stp-slider">
        <input
          type="range"
          min={p.min}
          max={p.max}
          step={p.step}
          value={p.value ?? p.fallback}
          disabled={p.value === null || !!p.disabledReason}
          aria-label={p.label}
          onInput={(e) => p.apply(+e.currentTarget.value)}
        />
        <span class="stp-val">
          {p.disabledReason
            ? "\u2014"
            : p.value === null
              ? "Auto"
              : `${p.value}${p.unit}`}
        </span>
      </div>
    </div>
  );
}

/** Theme swatch: built-ins render bare; custom themes get an edit affordance. */
interface SwatchProps {
  t: ThemeDef;
  active: boolean;
  onSelect: () => void;
  onEdit: () => void;
}

function Swatch(p: SwatchProps) {
  const button = (
    <button
      class={["stp-swatch", { active: p.active }]}
      aria-pressed={p.active ? "true" : "false"}
      style={{ background: p.t.bg, color: p.t.fg }}
      title={p.t.label}
      aria-label={p.t.label}
      onClick={p.onSelect}
    >
      <span class="stp-aa">Aa</span>
      <span class="stp-dot" style={{ background: p.t.accent }} />
    </button>
  );
  return (
    <Show
      when={isBuiltInTheme(p.t.id)}
      fallback={
        <div class="stp-swatch-wrap">
          {button}
          <button
            class="stp-edit"
            title={`Edit ${p.t.label}`}
            aria-label={`Edit ${p.t.label}`}
            onClick={p.onEdit}
          >
            <Icon icon={Pencil} size={11} labelFromParent />
          </button>
        </div>
      }
    >
      {button}
    </Show>
  );
}

export default function SettingsPanel(props: Props) {
  const s = (): UserSettings => settings.value;

  // --- Presets: server-synced snapshots of the whole settings object --------
  // A preset captures every setting (including theme + fonts) and round-trips
  // through the same validator as a normal save. The list starts empty; users
  // build their own. Kept as local panel state since presets surface only here.
  const [presets, setPresets] = createSignal<SettingsPreset[]>([]);
  const [naming, setNaming] = createSignal(false);
  const [presetName, setPresetName] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  // Custom-theme editor overlay: null = closed. `base` seeds the colors (the
  // active theme for a new one); `edit` is the target when editing an existing
  // custom theme.
  const [editor, setEditor] = createSignal<{
    base: ThemeDef;
    edit: ThemeDef | null;
  } | null>(null);

  const [rescanning, setRescanning] = createSignal(false);

  // Reset-to-defaults uses a two-step confirm (no blocking dialog): the first
  // click arms it, a second click within 3s applies. Avoids nuking a tuned
  // setup on a stray tap.
  const [resetArmed, setResetArmed] = createSignal(false);
  let resetTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(resetTimer));

  // Built-ins plus the user's custom themes, split by group. Derived so that
  // creating / editing / deleting a custom theme (customThemes.list is a
  // reactive store) reflows the right row immediately.
  const lightThemes = createMemo(() => [
    ...THEMES.filter((t) => t.group === "light"),
    ...customThemes.list.filter((t) => t.group === "light"),
  ]);
  const darkThemes = createMemo(() => [
    ...THEMES.filter((t) => t.group === "dark"),
    ...customThemes.list.filter((t) => t.group === "dark"),
  ]);

  // User font families and the currently-selected one (if a user font).
  const userFamilies = () => fontRegistry.families;
  const selectedUserFamily = createMemo(() =>
    isUserFamilyId(s().fontFamily)
      ? fontRegistry.get(s().fontFamily)
      : undefined,
  );

  // The id to *display* as selected. A user font can't be validated until the
  // registry loads; once it has, an id with no matching family (e.g. the user
  // removed that font) falls back to the default so the <select> shows a real
  // option instead of going blank. Storage is left untouched (non-destructive);
  // changing the selection writes the chosen id back via onChange.
  const effectiveFontFamily = createMemo(() => {
    const id = s().fontFamily;
    if (isUserFamilyId(id)) {
      if (!fontRegistry.loaded) return id;
      return fontRegistry.get(id) ? id : DEFAULT_USER_SETTINGS.fontFamily;
    }
    return getFontById(id) ? id : DEFAULT_USER_SETTINGS.fontFamily;
  });

  // Title-font override. Unlike the body font, null is a valid choice ("inherit
  // the body font"), so an unset or unresolved id shows "Auto" (empty value)
  // instead of snapping to a concrete default. Storage is left untouched.
  const effectiveTitleFont = createMemo(() => {
    const id = s().chapterTitleFontFamily;
    if (id == null) return "";
    if (isUserFamilyId(id)) {
      if (!fontRegistry.loaded) return id;
      return fontRegistry.get(id) ? id : "";
    }
    return getFontById(id) ? id : "";
  });

  onSettled(() => {
    void loadPresets();
    // Retry a non-fatal custom-theme boot failure when the user opens the
    // settings surface that consumes and edits those themes.
    if (!customThemes.loaded) void customThemes.load();
  });

  async function loadPresets(): Promise<void> {
    try {
      setPresets(await getPresets());
    } catch {
      // Non-fatal: presets are a convenience; leave the list empty on failure.
    }
  }

  function startNaming(): void {
    setNaming(true);
    setPresetName("");
  }

  function cancelNaming(): void {
    setNaming(false);
    setPresetName("");
  }

  async function savePreset(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const name = presetName().trim();
    if (!name || saving()) return;
    // Never capture the compile-time defaults: until the load resolves,
    // settings.value is not the user's state, and the server would happily
    // store those defaults under a user-chosen name (they validate fine).
    if (!settings.loaded) {
      toast.show("Settings aren't loaded yet");
      return;
    }
    setSaving(true);
    try {
      // Copy the nested map too: { ...s() } alone would put the live store's
      // own fontRoles object in the request body (see #saveNow).
      const created = await createPreset({
        name,
        settings: { ...s(), fontRoles: { ...(s().fontRoles ?? {}) } },
      });
      setPresets([...presets(), created]);
      setNaming(false);
      setPresetName("");
      toast.show(`Saved preset "${created.name}"`);
    } catch {
      toast.show("Couldn't save preset");
    } finally {
      setSaving(false);
    }
  }

  function applyPreset(p: SettingsPreset): void {
    // Copy the nested container as well: update() assigns it into the store
    // as-is, and the store must never alias an object this cached preset list
    // keeps holding (freshDefaults() and #saveNow follow the same doctrine).
    settings.update({
      ...p.settings,
      fontRoles: { ...(p.settings.fontRoles ?? {}) },
    });
    toast.show(`Applied "${p.name}"`);
  }

  async function removePreset(p: SettingsPreset): Promise<void> {
    // Optimistic remove, rolled back by id at its old index -- never from a
    // whole-list snapshot, which resurrects presets that other in-flight
    // deletes removed after the snapshot was taken.
    const index = presets().findIndex((x) => x.id === p.id);
    setPresets(presets().filter((x) => x.id !== p.id));
    try {
      await deletePreset(p.id);
    } catch {
      const list = presets();
      if (!list.some((x) => x.id === p.id)) {
        const restored = [...list];
        restored.splice(index < 0 ? list.length : index, 0, p);
        setPresets(restored);
      }
      toast.show("Couldn't delete preset");
    }
  }

  function openCreate(): void {
    setEditor({ base: getTheme(s().theme), edit: null });
  }

  function openEdit(t: ThemeDef): void {
    setEditor({ base: t, edit: t });
  }

  function closeEditor(): void {
    setEditor(null);
  }

  // The file explicitly chosen for a role, or "" for Auto. Deliberately no
  // fall back to fam.detected: the Auto option names the detected file, so
  // preselecting it here would render the scanner's guess as the user's own
  // pick -- and make clearing back to Auto look like a no-op when it in fact
  // deletes a persisted override.
  function roleValue(
    role: "regular" | "italic" | "bold" | "boldItalic",
  ): string {
    const fam = selectedUserFamily();
    if (!fam) return "";
    return s().fontRoles?.[fam.id]?.[role] ?? "";
  }

  function setRole(
    role: "regular" | "italic" | "bold" | "boldItalic",
    file: string,
  ): void {
    const fam = selectedUserFamily();
    if (!fam) return;
    const next: Record<
      string,
      { regular?: string; italic?: string; bold?: string; boldItalic?: string }
    > = {
      ...(s().fontRoles ?? {}),
    };
    const entry = { ...(next[fam.id] ?? {}) };
    if (file) entry[role] = file;
    else delete entry[role];
    if (!entry.regular && !entry.italic && !entry.bold && !entry.boldItalic)
      delete next[fam.id];
    else next[fam.id] = entry;
    set("fontRoles", next);
  }

  async function rescan(): Promise<void> {
    if (rescanning()) return;
    setRescanning(true);
    try {
      if (!(await fontRegistry.rescan())) {
        toast.show("Couldn't rescan fonts. Please try again.");
      }
    } finally {
      setRescanning(false);
    }
  }

  function resetToDefaults(): void {
    if (!resetArmed()) {
      setResetArmed(true);
      clearTimeout(resetTimer);
      resetTimer = setTimeout(() => setResetArmed(false), 3000);
      return;
    }
    clearTimeout(resetTimer);
    setResetArmed(false);
    settings.resetToDefaults();
    toast.show("Settings reset to defaults");
  }

  return (
    // Escape is handled here, not by focusTrap (which deliberately ignores
    // Esc): range/select controls are keyboard consumers (isKeyboardConsumer
    // in Read.tsx), so the reader's window-level Esc never fires with focus on
    // a slider or font select. Toc/Search/Bookmarks handle Esc locally too.
    <div
      class="stp"
      onKeyDown={(e) => {
        // The custom-theme dialog is a descendant. Its capture listener stands
        // down for composition, so this ancestor must not dismiss underneath it.
        if (e.key !== "Escape" || e.isComposing) return;
        e.preventDefault();
        e.stopPropagation();
        props.onclose();
      }}
    >
      <header class="stp-head">
        <div class="stp-head-text">
          <p class="eyebrow">Reader</p>
          <h2 class="display stp-title">Settings</h2>
        </div>
        <button
          class="icon-btn press stp-close"
          onClick={props.onclose}
          aria-label="Close settings"
        >
          <Icon icon={X} size={18} labelFromParent />
        </button>
      </header>

      <div class="stp-body">
        <section class="stp-section">
          <h3>Presets</h3>
          <p class="stp-hint">
            Save your current settings as a preset, then tap one to apply it
            later.
          </p>

          <Show when={presets().length > 0}>
            <div class="stp-presets" role="group" aria-label="Saved presets">
              <For each={presets()}>
                {(p) => (
                  <div class="stp-preset-chip">
                    <button
                      class="stp-preset-apply"
                      onClick={() => applyPreset(p)}
                      title={`Apply ${p.name}`}
                    >
                      {p.name}
                    </button>
                    <button
                      class="stp-preset-del"
                      onClick={() => void removePreset(p)}
                      aria-label={`Delete preset ${p.name}`}
                      title="Delete preset"
                    >
                      <Icon icon={X} size={13} labelFromParent />
                    </button>
                  </div>
                )}
              </For>
            </div>
          </Show>

          <Show
            when={naming()}
            fallback={
              <button class="stp-preset-new" onClick={startNaming}>
                + Save current as preset
              </button>
            }
          >
            <form class="stp-preset-save" onSubmit={(e) => void savePreset(e)}>
              <input
                class="stp-preset-name"
                type="text"
                value={presetName()}
                placeholder="Preset name"
                maxlength="60"
                aria-label="Preset name"
                onInput={(e) => setPresetName(e.currentTarget.value)}
              />
              <button
                class="stp-preset-confirm"
                type="submit"
                aria-disabled={
                  !presetName().trim() || saving() ? "true" : "false"
                }
              >
                Save
              </button>
              <button
                class="stp-preset-cancel"
                type="button"
                onClick={cancelNaming}
              >
                Cancel
              </button>
            </form>
          </Show>
        </section>

        <section class="stp-section">
          <h3>Reading mode</h3>
          <div class="stp-segmented" role="group" aria-label="Reading mode">
            <For each={MODES}>
              {(m) => (
                <button
                  class={[{ active: s().displayMode === m.id }]}
                  aria-pressed={s().displayMode === m.id ? "true" : "false"}
                  onClick={() => set("displayMode", m.id)}
                >
                  {m.label}
                </button>
              )}
            </For>
          </div>
          <Show when={props.modeFallback === "vertical-writing"}>
            <p class="stp-hint stp-mode-note" role="status">
              Vertical writing uses Scroll for this chapter. Your paged
              preference remains active for horizontal chapters.
            </p>
          </Show>
        </section>

        <section class="stp-section">
          <h3>Theme</h3>

          <p class="stp-group-label">Light</p>
          <div class="stp-swatches" role="group" aria-label="Light themes">
            <For each={lightThemes()}>
              {(t) => (
                <Swatch
                  t={t}
                  active={s().theme === t.id}
                  onSelect={() => set("theme", t.id)}
                  onEdit={() => openEdit(t)}
                />
              )}
            </For>
            <button
              class="stp-swatch stp-add"
              title="Create custom theme"
              aria-label="Create custom theme"
              onClick={openCreate}
            >
              <Icon icon={Plus} size={16} labelFromParent />
            </button>
          </div>

          <p class="stp-group-label">Dark</p>
          <div class="stp-swatches" role="group" aria-label="Dark themes">
            <For each={darkThemes()}>
              {(t) => (
                <Swatch
                  t={t}
                  active={s().theme === t.id}
                  onSelect={() => set("theme", t.id)}
                  onEdit={() => openEdit(t)}
                />
              )}
            </For>
            <button
              class="stp-swatch stp-add"
              title="Create custom theme"
              aria-label="Create custom theme"
              onClick={openCreate}
            >
              <Icon icon={Plus} size={16} labelFromParent />
            </button>
          </div>
        </section>

        <section class="stp-section">
          <h3>Font</h3>
          <button
            class="stp-specimen"
            onClick={() => openSpecimen(props.onclose)}
          >
            Open type specimen
          </button>
          <p class="stp-hint">A sample chapter for previewing your settings.</p>
          <label class="stp-toggle">
            <input
              type="checkbox"
              checked={s().preserveFonts}
              onChange={(e) => set("preserveFonts", e.currentTarget.checked)}
            />
            Use the book's fonts
          </label>

          {/* The font choice is moot while the book's own fonts are in use: the
              iframe only applies fontFamily when preserveBookFonts is off, so
              the picker, per-style role overrides, and rescan are disabled to
              match. */}
          <select
            class="stp-font-select"
            id="reading-font"
            name="reading-font"
            value={effectiveFontFamily()}
            aria-label="Reading font"
            disabled={s().preserveFonts}
            onChange={(e) => set("fontFamily", e.currentTarget.value)}
          >
            <optgroup label="Built-in">
              <For each={READER_FONTS}>
                {(f) => <option value={f.id}>{f.label}</option>}
              </For>
            </optgroup>
            {/* Always mounted, emptied by <For>: on 2.0.0-beta.29 a
                conditional element child of a <select> is never removed once
                the <select> has another element child (probe-verified for
                <Show>, a ternary and a gated <For>, on either side of the
                sibling). Gating it here strands the stale group after a rescan
                and its dead user:<dir> options stay selectable, so the empty
                state lives in the label instead. */}
            <optgroup
              label={
                userFamilies().length > 0 ? "Your fonts" : "Your fonts (none)"
              }
            >
              <For each={userFamilies()}>
                {(f) => <option value={f.id}>{f.label}</option>}
              </For>
            </optgroup>
          </select>

          <Show when={selectedUserFamily()}>
            {(fam) => (
              <div
                class={["stp-roles", { "stp-row-disabled": s().preserveFonts }]}
              >
                <p class="stp-roles-hint">
                  Pick which file to use for each style.
                </p>
                <For each={fam().variable ? AXIS_ROLES : ROLES}>
                  {(role) => (
                    <label class="stp-role-row">
                      <span class="stp-role-label">{role.label}</span>
                      <select
                        class="stp-role-select"
                        id={`font-role-${role.key}`}
                        name={`font-role-${role.key}`}
                        value={roleValue(role.key)}
                        disabled={s().preserveFonts}
                        onChange={(e) =>
                          setRole(role.key, e.currentTarget.value)
                        }
                      >
                        <option value="">
                          {fam().detected[role.key]
                            ? `Auto (${fam().detected[role.key]})`
                            : "Auto"}
                        </option>
                        <For each={fam().files}>
                          {(file) => <option value={file}>{file}</option>}
                        </For>
                      </select>
                    </label>
                  )}
                </For>
              </div>
            )}
          </Show>

          <button
            class="stp-rescan"
            onClick={() => void rescan()}
            disabled={rescanning() || s().preserveFonts}
          >
            {rescanning() ? "Scanning…" : "Rescan fonts folder"}
          </button>
        </section>

        <section class="stp-section">
          <h3>Text</h3>
          <div class="stp-row">
            <div class="stp-row-head">
              <span class="stp-label">Font size</span>
            </div>
            <div class="stp-slider">
              <input
                type="range"
                min="14"
                max="50"
                step="1"
                value={s().fontSize}
                aria-label="Font size"
                onInput={(e) => set("fontSize", +e.currentTarget.value)}
              />
              <span class="stp-val">{s().fontSize}px</span>
            </div>
          </div>

          <AutoRow
            label="Line height"
            value={s().lineHeight}
            min={1.2}
            max={2.4}
            step={0.05}
            fallback={1.6}
            unit=""
            apply={(v) => set("lineHeight", v)}
          />
          <AutoRow
            label="Paragraph spacing"
            value={s().paragraphSpacing}
            min={0}
            max={2}
            step={0.1}
            fallback={0.8}
            unit="em"
            apply={(v) => set("paragraphSpacing", v)}
          />
          <AutoRow
            label="Paragraph indent"
            value={s().textIndent}
            min={0}
            max={4}
            step={0.1}
            fallback={1.2}
            unit="em"
            apply={(v) => set("textIndent", v)}
          />
          <AutoRow
            label="Letter spacing"
            value={s().letterSpacing}
            min={-0.05}
            max={0.25}
            step={0.01}
            fallback={0}
            unit="em"
            apply={(v) => set("letterSpacing", v)}
          />
          <AutoRow
            label="Font weight"
            value={s().textWeight}
            min={100}
            max={900}
            step={100}
            fallback={400}
            unit=""
            apply={(v) => set("textWeight", v)}
            disabledReason={null}
            headNote={weightName}
          />

          <label class="stp-toggle">
            <input
              type="checkbox"
              checked={s().justify}
              onChange={(e) => set("justify", e.currentTarget.checked)}
            />
            Justify text
          </label>
          <label class="stp-toggle">
            <input
              type="checkbox"
              checked={s().hyphenation}
              onChange={(e) => set("hyphenation", e.currentTarget.checked)}
            />
            Hyphenation
          </label>
        </section>

        <section class="stp-section">
          <h3>Layout</h3>
          <AutoRow
            label="Side margin"
            value={s().marginSide}
            min={0}
            max={160}
            step={4}
            fallback={48}
            unit="px"
            apply={(v) => set("marginSide", v)}
          />
          {/* Top and bottom are independent server-side fields that this one
              row writes together. A preset or a hand-edited save can diverge
              them, so surface the bottom value instead of showing top as if it
              spoke for both. */}
          <AutoRow
            label="Vertical margin"
            value={s().marginTop}
            min={0}
            max={160}
            step={4}
            fallback={48}
            unit="px"
            apply={(v) => settings.update({ marginTop: v, marginBottom: v })}
            headNote={(v) => {
              const b = s().marginBottom;
              if (b === v) return "";
              return b === null ? "bottom auto" : `bottom ${b}px`;
            }}
          />
          <AutoRow
            label="Content width"
            value={s().contentWidth}
            min={40}
            max={100}
            step={5}
            fallback={70}
            unit="%"
            apply={(v) => set("contentWidth", v)}
            disabledReason={
              props.effectiveMode === "scroll" ? null : "Scroll mode only"
            }
          />
        </section>

        <section class="stp-section">
          <h3>Chapter titles</h3>
          <p class="stp-group-label">Title font</p>
          {/* Overrides the font for headings only. Like the body font it
              applies only when the book's own fonts aren't in use, so it's
              disabled while "Use the book's fonts" is on; "Auto" inherits the
              body reading font. */}
          <select
            class="stp-font-select"
            id="title-font"
            name="title-font"
            value={effectiveTitleFont()}
            aria-label="Chapter title font"
            disabled={s().preserveFonts}
            onChange={(e) =>
              set("chapterTitleFontFamily", e.currentTarget.value || null)
            }
          >
            <option value="">Auto (match body)</option>
            <optgroup label="Built-in">
              <For each={READER_FONTS}>
                {(f) => <option value={f.id}>{f.label}</option>}
              </For>
            </optgroup>
            {/* Always mounted for the same beta.29 reason as the body-font
                group above. */}
            <optgroup
              label={
                userFamilies().length > 0 ? "Your fonts" : "Your fonts (none)"
              }
            >
              <For each={userFamilies()}>
                {(f) => <option value={f.id}>{f.label}</option>}
              </For>
            </optgroup>
          </select>

          <div class="stp-row">
            <div class="stp-row-head">
              <span class="stp-label">Alignment</span>
            </div>
            <div
              class="stp-segmented stp-segmented-small"
              role="group"
              aria-label="Chapter title alignment"
            >
              <For each={TITLE_ALIGNS}>
                {(a) => (
                  <button
                    class={[{ active: s().chapterTitleAlign === a.id }]}
                    aria-pressed={
                      s().chapterTitleAlign === a.id ? "true" : "false"
                    }
                    onClick={() => set("chapterTitleAlign", a.id)}
                  >
                    {a.label}
                  </button>
                )}
              </For>
            </div>
          </div>

          <AutoRow
            label="Title size"
            value={s().chapterTitleSize}
            min={16}
            max={64}
            step={1}
            fallback={32}
            unit="px"
            apply={(v) => set("chapterTitleSize", v)}
            disabledReason={
              s().headerSizesEnabled ? "Per-heading sizes on" : null
            }
          />

          <details class="stp-per-header">
            <summary>Size each heading (H1–H6)</summary>
            <label class="stp-toggle">
              <input
                type="checkbox"
                checked={s().headerSizesEnabled}
                onChange={(e) =>
                  set("headerSizesEnabled", e.currentTarget.checked)
                }
              />
              Override each heading size
            </label>
            <For each={HEADERS}>
              {(h) => (
                <AutoRow
                  label={h.label}
                  value={s()[h.key]}
                  min={10}
                  max={100}
                  step={1}
                  fallback={32}
                  unit="px"
                  apply={(v) => set(h.key, v)}
                  disabledReason={
                    s().headerSizesEnabled ? null : "Turn on above to edit"
                  }
                />
              )}
            </For>
          </details>

          <AutoRow
            label="Title weight"
            value={s().headerWeight}
            min={100}
            max={900}
            step={100}
            fallback={700}
            unit=""
            apply={(v) => set("headerWeight", v)}
            disabledReason={null}
            headNote={weightName}
          />
          <AutoRow
            label="Title letter spacing"
            value={s().headingLetterSpacing}
            min={-0.05}
            max={0.25}
            step={0.01}
            fallback={0}
            unit="em"
            apply={(v) => set("headingLetterSpacing", v)}
          />
          <AutoRow
            label="Title spacing"
            value={s().chapterTitleSpacing}
            min={0}
            max={4}
            step={0.1}
            fallback={1}
            unit="em"
            apply={(v) => set("chapterTitleSpacing", v)}
          />
        </section>

        <section class="stp-section">
          <h3>Book styling</h3>
          <label class="stp-toggle">
            <input
              type="checkbox"
              checked={s().preserveStyles}
              onChange={(e) => set("preserveStyles", e.currentTarget.checked)}
            />
            Keep the book's own CSS
          </label>
        </section>
      </div>

      <footer class="stp-foot">
        <button
          class={["stp-reset", { armed: resetArmed() }]}
          onClick={resetToDefaults}
          aria-label="Reset all settings to defaults"
        >
          {resetArmed()
            ? "Click again to reset everything"
            : "Reset to defaults"}
        </button>
      </footer>

      {/* Remounted per open so the dialog can seed local state from props. */}
      <Show when={editor()}>
        {(ed) => (
          <CustomThemeDialog
            base={ed().base}
            edit={ed().edit}
            onclose={closeEditor}
          />
        )}
      </Show>
    </div>
  );
}
