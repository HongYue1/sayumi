<script lang="ts">
  import { settings, DEFAULT_USER_SETTINGS } from "~/lib/settings.svelte";
  import { THEMES } from "~/lib/themes";
  import { READER_FONTS, getFontById } from "~/lib/fonts";
  import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry.svelte";
  import { toast } from "~/lib/toast.svelte";
  import type { UserSettings } from "~/api/client";
  import Icon from "~/lib/Icon.svelte";
  import { X } from "@lucide/svelte";

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();

  const s = $derived(settings.value);

  const lightThemes = THEMES.filter((t) => t.group === "light");
  const darkThemes = THEMES.filter((t) => t.group === "dark");

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

  // User font families and the currently-selected one (if a user font).
  const userFamilies = $derived(fontRegistry.families);
  const selectedUserFamily = $derived(
    isUserFamilyId(s.fontFamily) ? fontRegistry.get(s.fontFamily) : undefined,
  );

  // The id to *display* as selected. A user font can't be validated until the
  // registry loads; once it has, an id with no matching family (e.g. the user
  // removed that font) falls back to the default so the <select> shows a real
  // option instead of going blank. Storage is left untouched (non-destructive);
  // changing the selection writes the chosen id back via onchange.
  const effectiveFontFamily = $derived.by(() => {
    const id = s.fontFamily;
    if (isUserFamilyId(id)) {
      if (!fontRegistry.loaded) return id;
      return fontRegistry.get(id) ? id : DEFAULT_USER_SETTINGS.fontFamily;
    }
    return getFontById(id) ? id : DEFAULT_USER_SETTINGS.fontFamily;
  });

  const ROLES: { key: "regular" | "italic" | "bold"; label: string }[] = [
    { key: "regular", label: "Regular" },
    { key: "italic", label: "Italic" },
    { key: "bold", label: "Bold" },
  ];

  // The file currently chosen for a role: the explicit override, else the
  // backend's detected guess, else empty.
  function roleValue(role: "regular" | "italic" | "bold"): string {
    const fam = selectedUserFamily;
    if (!fam) return "";
    return s.fontRoles?.[fam.id]?.[role] ?? fam.detected[role] ?? "";
  }

  function setRole(role: "regular" | "italic" | "bold", file: string): void {
    const fam = selectedUserFamily;
    if (!fam) return;
    const next: Record<
      string,
      { regular?: string; italic?: string; bold?: string }
    > = {
      ...(s.fontRoles ?? {}),
    };
    const entry = { ...(next[fam.id] ?? {}) };
    if (file) entry[role] = file;
    else delete entry[role];
    if (!entry.regular && !entry.italic && !entry.bold) delete next[fam.id];
    else next[fam.id] = entry;
    set("fontRoles", next);
  }

  let rescanning = $state(false);
  async function rescan(): Promise<void> {
    if (rescanning) return;
    rescanning = true;
    try {
      if (!(await fontRegistry.rescan())) {
        toast.show("Couldn't rescan fonts. Please try again.");
      }
    } finally {
      rescanning = false;
    }
  }

  function set<K extends keyof UserSettings>(
    key: K,
    value: UserSettings[K],
  ): void {
    settings.update({ [key]: value } as Partial<UserSettings>);
  }

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
  function weightName(v: number): string {
    return WEIGHT_NAMES[v] ?? "";
  }
</script>

<!-- Numeric row with an "Auto" toggle for nullable settings. -->
{#snippet autoRow(
  label: string,
  value: number | null,
  min: number,
  max: number,
  step: number,
  fallback: number,
  unit: string,
  apply: (v: number | null) => void,
  disabledReason?: string | null,
  headNote?: (v: number) => string,
)}
  <div class="row" class:row-disabled={!!disabledReason}>
    <div class="row-head">
      <span class="label"
        >{label}{#if value !== null && headNote}<span class="head-note">
            · {headNote?.(value)}</span
          >{/if}</span
      >
      {#if disabledReason}
        <span class="hint">{disabledReason}</span>
      {:else}
        <label class="auto">
          <input
            type="checkbox"
            checked={value === null}
            aria-label={`Auto ${label}`}
            onchange={(e) => apply(e.currentTarget.checked ? null : fallback)}
          />
          Auto
        </label>
      {/if}
    </div>
    <div class="slider">
      <input
        type="range"
        {min}
        {max}
        {step}
        value={value ?? fallback}
        disabled={value === null || !!disabledReason}
        aria-label={label}
        oninput={(e) => apply(+e.currentTarget.value)}
      />
      <span class="val"
        >{disabledReason
          ? "\u2014"
          : value === null
            ? "Auto"
            : `${value}${unit}`}</span
      >
    </div>
  </div>
{/snippet}

<div class="settings">
  <header>
    <h2>Settings</h2>
    <button class="close" onclick={onclose} aria-label="Close settings"
      ><Icon icon={X} size={18} /></button
    >
  </header>

  <div class="body">
    <section>
      <h3>Reading mode</h3>
      <div class="segmented" role="group" aria-label="Reading mode">
        {#each MODES as m (m.id)}
          <button
            class:active={s.displayMode === m.id}
            aria-pressed={s.displayMode === m.id}
            onclick={() => set("displayMode", m.id)}
          >
            {m.label}
          </button>
        {/each}
      </div>
    </section>

    <section>
      <h3>Theme</h3>
      <p class="group-label">Light</p>
      <div class="swatches" role="group" aria-label="Light themes">
        {#each lightThemes as t (t.id)}
          <button
            class="swatch"
            class:active={s.theme === t.id}
            aria-pressed={s.theme === t.id}
            style:background={t.bg}
            style:color={t.fg}
            title={t.label}
            aria-label={t.label}
            onclick={() => set("theme", t.id)}
          >
            <span class="aa">Aa</span>
            <span class="dot" style:background={t.accent}></span>
          </button>
        {/each}
      </div>
      <p class="group-label">Dark</p>
      <div class="swatches" role="group" aria-label="Dark themes">
        {#each darkThemes as t (t.id)}
          <button
            class="swatch"
            class:active={s.theme === t.id}
            aria-pressed={s.theme === t.id}
            style:background={t.bg}
            style:color={t.fg}
            title={t.label}
            aria-label={t.label}
            onclick={() => set("theme", t.id)}
          >
            <span class="aa">Aa</span>
            <span class="dot" style:background={t.accent}></span>
          </button>
        {/each}
      </div>
    </section>

    <section>
      <h3>Font</h3>
      <label class="toggle">
        <input
          type="checkbox"
          checked={s.preserveFonts}
          onchange={(e) => set("preserveFonts", e.currentTarget.checked)}
        />
        Use the book's fonts
      </label>

      <!-- The font choice is moot while the book's own fonts are in use: the
           iframe only applies fontFamily when preserveBookFonts is off, so the
           picker, per-style role overrides, and rescan are disabled to match. -->
      <select
        class="font-select"
        id="reading-font"
        name="reading-font"
        value={effectiveFontFamily}
        aria-label="Reading font"
        disabled={s.preserveFonts}
        onchange={(e) => set("fontFamily", e.currentTarget.value)}
      >
        <optgroup label="Built-in">
          {#each READER_FONTS as f (f.id)}
            <option value={f.id}>{f.label}</option>
          {/each}
        </optgroup>
        {#if userFamilies.length}
          <optgroup label="Your fonts">
            {#each userFamilies as f (f.id)}
              <option value={f.id}>{f.label}</option>
            {/each}
          </optgroup>
        {/if}
      </select>

      {#if selectedUserFamily}
        <div class="roles" class:row-disabled={s.preserveFonts}>
          <p class="roles-hint">Pick which file to use for each style.</p>
          {#each ROLES as role (role.key)}
            <label class="role-row">
              <span class="role-label">{role.label}</span>
              <select
                class="role-select"
                id={`font-role-${role.key}`}
                name={`font-role-${role.key}`}
                value={roleValue(role.key)}
                disabled={s.preserveFonts}
                onchange={(e) => setRole(role.key, e.currentTarget.value)}
              >
                <option value="">Auto</option>
                {#each selectedUserFamily.files as file (file)}
                  <option value={file}>{file}</option>
                {/each}
              </select>
            </label>
          {/each}
        </div>
      {/if}

      <button
        class="rescan"
        onclick={rescan}
        disabled={rescanning || s.preserveFonts}
      >
        {rescanning ? "Scanning…" : "Rescan ./Fonts"}
      </button>
    </section>

    <section>
      <h3>Text</h3>
      <div class="row">
        <div class="row-head"><span class="label">Font size</span></div>
        <div class="slider">
          <input
            type="range"
            min="14"
            max="50"
            step="1"
            value={s.fontSize}
            aria-label="Font size"
            oninput={(e) => set("fontSize", +e.currentTarget.value)}
          />
          <span class="val">{s.fontSize}px</span>
        </div>
      </div>

      {@render autoRow(
        "Line height",
        s.lineHeight,
        1.2,
        2.4,
        0.05,
        1.6,
        "",
        (v) => set("lineHeight", v),
      )}
      {@render autoRow(
        "Paragraph spacing",
        s.paragraphSpacing,
        0,
        2,
        0.1,
        0.8,
        "em",
        (v) => set("paragraphSpacing", v),
      )}
      {@render autoRow(
        "Paragraph indent",
        s.textIndent,
        0,
        4,
        0.1,
        1.2,
        "em",
        (v) => set("textIndent", v),
      )}
      {@render autoRow(
        "Font weight",
        s.textWeight,
        100,
        900,
        100,
        400,
        "",
        (v) => set("textWeight", v),
        null,
        weightName,
      )}

      <label class="toggle">
        <input
          type="checkbox"
          checked={s.justify}
          onchange={(e) => set("justify", e.currentTarget.checked)}
        />
        Justify text
      </label>
      <label class="toggle">
        <input
          type="checkbox"
          checked={s.hyphenation}
          onchange={(e) => set("hyphenation", e.currentTarget.checked)}
        />
        Hyphenation
      </label>
    </section>

    <section>
      <h3>Layout</h3>
      {@render autoRow("Side margin", s.marginSide, 0, 160, 4, 48, "px", (v) =>
        set("marginSide", v),
      )}
      {@render autoRow(
        "Vertical margin",
        s.marginTop,
        0,
        160,
        4,
        48,
        "px",
        (v) => {
          settings.update({ marginTop: v, marginBottom: v });
        },
      )}
      {@render autoRow(
        "Content width",
        s.contentWidth,
        40,
        100,
        5,
        70,
        "%",
        (v) => set("contentWidth", v),
        s.displayMode === "scroll" ? null : "Scroll mode only",
      )}
    </section>

    <section>
      <h3>Chapter titles</h3>
      <div class="row">
        <div class="row-head"><span class="label">Alignment</span></div>
        <div
          class="segmented small"
          role="group"
          aria-label="Chapter title alignment"
        >
          {#each TITLE_ALIGNS as a (a.id ?? "auto")}
            <button
              class:active={s.chapterTitleAlign === a.id}
              aria-pressed={s.chapterTitleAlign === a.id}
              onclick={() => set("chapterTitleAlign", a.id)}
            >
              {a.label}
            </button>
          {/each}
        </div>
      </div>

      {@render autoRow(
        "Title size",
        s.chapterTitleSize,
        16,
        64,
        1,
        32,
        "px",
        (v) => set("chapterTitleSize", v),
        s.headerSizesEnabled ? "Per-heading sizes on" : null,
      )}

      <details class="per-header">
        <summary>Size each heading (H1–H6)</summary>
        <label class="toggle">
          <input
            type="checkbox"
            checked={s.headerSizesEnabled}
            onchange={(e) => set("headerSizesEnabled", e.currentTarget.checked)}
          />
          Override each heading size
        </label>
        {#each HEADERS as h (h.key)}
          {@render autoRow(
            h.label,
            s[h.key],
            10,
            100,
            1,
            32,
            "px",
            (v) => set(h.key, v),
            s.headerSizesEnabled ? null : "Turn on above to edit",
          )}
        {/each}
      </details>

      {@render autoRow(
        "Title weight",
        s.headerWeight,
        100,
        900,
        100,
        700,
        "",
        (v) => set("headerWeight", v),
        null,
        weightName,
      )}
      {@render autoRow(
        "Title spacing",
        s.chapterTitleSpacing,
        0,
        4,
        0.1,
        1,
        "em",
        (v) => set("chapterTitleSpacing", v),
      )}
    </section>

    <section>
      <h3>Book styling</h3>
      <label class="toggle">
        <input
          type="checkbox"
          checked={s.preserveStyles}
          onchange={(e) => set("preserveStyles", e.currentTarget.checked)}
        />
        Keep the book's own CSS
      </label>
    </section>
  </div>
</div>

<style>
  .settings {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    color: var(--fg);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--hairline);
    flex: 0 0 auto;
  }
  h2 {
    margin: 0;
    font-family: var(--font-display);
    font-size: 1.4rem;
    font-weight: 500;
    line-height: 1;
  }
  .close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    background: transparent;
    color: var(--muted);
    padding: 0.3rem;
    border-radius: var(--radius);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .close:hover {
    background: var(--surface-hover);
    color: var(--fg);
  }
  .close:active {
    transform: scale(0.94);
  }
  .body {
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: var(--sp-3);
    padding: var(--sp-4) var(--sp-4) var(--sp-8);
  }
  /* Each section reads as its own editorial card rather than a divider-
     separated list: a subtle raised surface, generous internal padding, and a
     display-font heading give the panel rhythm and clearer hierarchy. */
  section {
    padding: var(--sp-4);
    background: var(--surface);
    border: 1px solid var(--hairline);
    border-radius: var(--radius);
  }
  h3 {
    margin: 0 0 var(--sp-3);
    font-family: var(--font-display);
    font-size: 1rem;
    font-weight: 500;
    color: var(--fg);
  }

  .segmented {
    display: flex;
    gap: 0.3rem;
  }
  .segmented button {
    flex: 1;
    padding: 0.45rem 0.3rem;
    border: 1px solid var(--hairline-strong);
    border-radius: var(--radius);
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: var(--text-sm);
    cursor: pointer;
    transition:
      background var(--dur) var(--ease-out),
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .segmented button:hover:not(.active) {
    border-color: var(--accent);
  }
  .segmented button:active {
    transform: scale(0.97);
  }
  .segmented button.active {
    background: var(--accent);
    color: var(--accent-fg);
    border-color: var(--accent);
  }
  .segmented.small button {
    padding: 0.3rem 0.2rem;
    font-size: 0.78rem;
  }

  .group-label {
    margin: 0.4rem 0 0.3rem;
    font-size: 0.72rem;
    color: var(--muted);
  }
  .swatches {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(54px, 1fr));
    gap: var(--sp-2);
  }
  .swatch {
    position: relative;
    aspect-ratio: 1.3;
    border: 2px solid var(--hairline-strong);
    border-radius: var(--radius);
    cursor: pointer;
    display: grid;
    place-items: center;
    padding: 0;
    transition:
      border-color var(--dur) var(--ease-out),
      transform var(--dur-fast) var(--ease-out);
  }
  .swatch:hover {
    border-color: var(--accent);
  }
  .swatch:active {
    transform: scale(0.95);
  }
  .swatch.active {
    border-color: var(--accent);
    box-shadow: 0 0 0 2px var(--accent);
  }
  .swatch .aa {
    font-size: 0.8rem;
    font-weight: 600;
  }
  .swatch .dot {
    position: absolute;
    right: 4px;
    bottom: 4px;
    width: 7px;
    height: 7px;
    border-radius: 50%;
  }

  .font-select {
    width: 100%;
    padding: 0.5rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 0.45rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    margin-bottom: 0.6rem;
  }
  .font-select:disabled,
  .role-select:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .per-header {
    margin: 0.1rem 0 0.5rem;
  }
  .per-header > summary {
    cursor: pointer;
    padding: 0.25rem 0;
    font-size: 0.82rem;
    color: var(--muted);
    user-select: none;
  }
  .per-header > summary:hover {
    color: var(--fg);
  }
  .per-header[open] > summary {
    margin-bottom: 0.25rem;
  }

  .roles {
    margin: 0.2rem 0 0.6rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--hairline);
    border-radius: 0.45rem;
    background: var(--bg);
  }
  .roles-hint {
    margin: 0 0 0.5rem;
    font-size: 0.72rem;
    color: var(--muted);
  }
  .role-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.4rem;
  }
  .role-row:last-child {
    margin-bottom: 0;
  }
  .role-label {
    flex: 0 0 4.5rem;
    font-size: 0.8rem;
  }
  .role-select {
    flex: 1;
    min-width: 0;
    padding: 0.35rem 0.4rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 0.35rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    font-size: 0.78rem;
  }
  .rescan {
    width: 100%;
    padding: 0.4rem;
    margin-bottom: 0.6rem;
    border: 1px solid var(--hairline-strong);
    border-radius: 0.4rem;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: 0.78rem;
    cursor: pointer;
  }
  .rescan:hover:not(:disabled) {
    background: var(--surface-hover);
  }
  .rescan:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .row {
    margin: 0.6rem 0;
  }
  .row-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }
  .label {
    font-size: 0.88rem;
  }
  .auto {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.78rem;
    color: var(--muted);
  }
  .slider {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .slider input[type="range"] {
    flex: 1;
    min-width: 0;
    accent-color: var(--accent);
  }
  .val {
    min-width: 3rem;
    text-align: right;
    font-size: 0.78rem;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }
  .head-note {
    color: var(--muted);
    font-weight: 400;
  }
  .hint {
    font-size: 0.78rem;
    color: var(--muted);
    font-style: italic;
  }
  .row-disabled .label {
    color: var(--muted);
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0.5rem 0;
    font-size: 0.88rem;
    cursor: pointer;
  }
  .toggle input {
    accent-color: var(--accent);
  }
</style>
