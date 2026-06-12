<script lang="ts">
  import { settings } from "~/lib/settings.svelte";
  import { THEMES } from "~/lib/themes";
  import { READER_FONTS } from "~/lib/fonts";
  import { fontRegistry, isUserFamilyId } from "~/lib/fontRegistry.svelte";
  import type { UserSettings } from "~/api/client";

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
  const TITLE_ALIGNS: { id: UserSettings["chapterTitleAlign"]; label: string }[] = [
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
    const next: Record<string, { regular?: string; italic?: string; bold?: string }> = {
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
      await fontRegistry.rescan();
    } finally {
      rescanning = false;
    }
  }

  function set<K extends keyof UserSettings>(key: K, value: UserSettings[K]): void {
    settings.update({ [key]: value } as Partial<UserSettings>);
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
)}
  <div class="row">
    <div class="row-head">
      <span class="label">{label}</span>
      <label class="auto">
        <input
          type="checkbox"
          checked={value === null}
          onchange={(e) => apply(e.currentTarget.checked ? null : fallback)}
        />
        Auto
      </label>
    </div>
    <div class="slider">
      <input
        type="range"
        {min}
        {max}
        {step}
        value={value ?? fallback}
        disabled={value === null}
        oninput={(e) => apply(+e.currentTarget.value)}
      />
      <span class="val">{value === null ? "Auto" : `${value}${unit}`}</span>
    </div>
  </div>
{/snippet}

<div class="settings">
  <header>
    <h2>Settings</h2>
    <button class="close" onclick={onclose} aria-label="Close settings">×</button>
  </header>

  <div class="body">
    <section>
      <h3>Reading mode</h3>
      <div class="segmented">
        {#each MODES as m (m.id)}
          <button class:active={s.displayMode === m.id} onclick={() => set("displayMode", m.id)}>
            {m.label}
          </button>
        {/each}
      </div>
    </section>

    <section>
      <h3>Theme</h3>
      <p class="group-label">Light</p>
      <div class="swatches">
        {#each lightThemes as t (t.id)}
          <button
            class="swatch"
            class:active={s.theme === t.id}
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
      <div class="swatches">
        {#each darkThemes as t (t.id)}
          <button
            class="swatch"
            class:active={s.theme === t.id}
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
      <select
        class="font-select"
        value={s.fontFamily}
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
        <div class="roles">
          <p class="roles-hint">Pick which file to use for each style.</p>
          {#each ROLES as role (role.key)}
            <label class="role-row">
              <span class="role-label">{role.label}</span>
              <select
                class="role-select"
                value={roleValue(role.key)}
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

      <button class="rescan" onclick={rescan} disabled={rescanning}>
        {rescanning ? "Scanning…" : "Rescan ./Fonts"}
      </button>

      <label class="toggle">
        <input
          type="checkbox"
          checked={s.preserveFonts}
          onchange={(e) => set("preserveFonts", e.currentTarget.checked)}
        />
        Use the book's fonts
      </label>
    </section>

    <section>
      <h3>Text</h3>
      <div class="row">
        <div class="row-head"><span class="label">Font size</span></div>
        <div class="slider">
          <input
            type="range"
            min="14"
            max="40"
            step="1"
            value={s.fontSize}
            oninput={(e) => set("fontSize", +e.currentTarget.value)}
          />
          <span class="val">{s.fontSize}px</span>
        </div>
      </div>

      {@render autoRow("Line height", s.lineHeight, 1.2, 2.4, 0.05, 1.6, "", (v) => set("lineHeight", v))}
      {@render autoRow("Paragraph spacing", s.paragraphSpacing, 0, 2, 0.1, 0.8, "em", (v) => set("paragraphSpacing", v))}
      {@render autoRow("Paragraph indent", s.textIndent, 0, 4, 0.1, 1.2, "em", (v) => set("textIndent", v))}

      <label class="toggle">
        <input type="checkbox" checked={s.justify} onchange={(e) => set("justify", e.currentTarget.checked)} />
        Justify text
      </label>
      <label class="toggle">
        <input type="checkbox" checked={s.hyphenation} onchange={(e) => set("hyphenation", e.currentTarget.checked)} />
        Hyphenation
      </label>
    </section>

    <section>
      <h3>Layout</h3>
      {@render autoRow("Side margin", s.marginSide, 0, 160, 4, 48, "px", (v) => set("marginSide", v))}
      {@render autoRow("Vertical margin", s.marginTop, 0, 160, 4, 48, "px", (v) => {
        set("marginTop", v);
        set("marginBottom", v);
      })}
      {@render autoRow("Content width", s.contentWidth, 480, 1200, 20, 720, "px", (v) => set("contentWidth", v))}
    </section>

    <section>
      <h3>Chapter titles</h3>
      <div class="row">
        <div class="row-head"><span class="label">Alignment</span></div>
        <div class="segmented small">
          {#each TITLE_ALIGNS as a (a.id ?? "auto")}
            <button
              class:active={s.chapterTitleAlign === a.id}
              onclick={() => set("chapterTitleAlign", a.id)}
            >
              {a.label}
            </button>
          {/each}
        </div>
      </div>

      {@render autoRow("Title size", s.chapterTitleSize, 16, 64, 1, 32, "px", (v) => set("chapterTitleSize", v))}
      {@render autoRow("Title spacing", s.chapterTitleSpacing, 0, 4, 0.1, 1, "em", (v) => set("chapterTitleSpacing", v))}
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
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 10%, transparent);
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
    border: none;
    background: transparent;
    color: var(--fg);
    font-size: 1.4rem;
    line-height: 1;
    cursor: pointer;
  }
  .body {
    overflow-y: auto;
    padding: 0.5rem 1rem 2rem;
  }
  section {
    padding: 0.85rem 0;
    border-bottom: 1px solid color-mix(in srgb, var(--fg) 8%, transparent);
  }
  section:last-child {
    border-bottom: none;
  }
  h3 {
    margin: 0 0 0.6rem;
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.14em;
    color: var(--muted, #6b6661);
  }

  .segmented {
    display: flex;
    gap: 0.3rem;
  }
  .segmented button {
    flex: 1;
    padding: 0.45rem 0.3rem;
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.45rem;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: 0.85rem;
    cursor: pointer;
  }
  .segmented button.active {
    background: var(--accent);
    color: #fff;
    border-color: var(--accent);
  }
  .segmented.small button {
    padding: 0.3rem 0.2rem;
    font-size: 0.78rem;
  }

  .group-label {
    margin: 0.4rem 0 0.3rem;
    font-size: 0.72rem;
    color: var(--muted, #6b6661);
  }
  .swatches {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(48px, 1fr));
    gap: 0.4rem;
  }
  .swatch {
    position: relative;
    aspect-ratio: 1.3;
    border: 2px solid color-mix(in srgb, var(--fg) 16%, transparent);
    border-radius: 0.4rem;
    cursor: pointer;
    display: grid;
    place-items: center;
    padding: 0;
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
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.45rem;
    background: var(--bg);
    color: var(--fg);
    font: inherit;
    margin-bottom: 0.6rem;
  }

  .roles {
    margin: 0.2rem 0 0.6rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid color-mix(in srgb, var(--fg) 10%, transparent);
    border-radius: 0.45rem;
    background: color-mix(in srgb, var(--fg) 3%, transparent);
  }
  .roles-hint {
    margin: 0 0 0.5rem;
    font-size: 0.72rem;
    color: var(--muted, #6b6661);
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
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
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
    border: 1px solid color-mix(in srgb, var(--fg) 14%, transparent);
    border-radius: 0.4rem;
    background: transparent;
    color: var(--fg);
    font: inherit;
    font-size: 0.78rem;
    cursor: pointer;
  }
  .rescan:hover:not(:disabled) {
    background: color-mix(in srgb, var(--fg) 6%, transparent);
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
    color: var(--muted, #6b6661);
  }
  .slider {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .slider input[type="range"] {
    flex: 1;
    accent-color: var(--accent);
  }
  .val {
    min-width: 3rem;
    text-align: right;
    font-size: 0.78rem;
    color: var(--muted, #6b6661);
    font-variant-numeric: tabular-nums;
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
