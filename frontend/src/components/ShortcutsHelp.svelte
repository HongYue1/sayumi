<script lang="ts">
  import { ui } from "~/lib/ui.svelte";
  import Icon from "~/lib/Icon.svelte";
  import { X } from "@lucide/svelte";
  import { focusTrap } from "~/lib/focusTrap";

  const groups: { title: string; items: { keys: string[]; desc: string }[] }[] =
    [
      {
        title: "Global",
        items: [
          { keys: ["Ctrl / ⌘", "K"], desc: "Open command palette" },
          { keys: ["?"], desc: "Show this help" },
          { keys: ["Esc"], desc: "Close overlay / panel" },
        ],
      },
      {
        title: "Reader",
        items: [
          { keys: ["←"], desc: "Navigate left" },
          { keys: ["→"], desc: "Navigate right" },
          { keys: ["T"], desc: "Table of contents" },
          { keys: ["S"], desc: "Settings" },
          { keys: ["F"], desc: "Search in book" },
          { keys: ["B"], desc: "Toggle bookmark" },
          { keys: ["Shift", "B"], desc: "Bookmarks panel" },
        ],
      },
    ];

  function close(): void {
    ui.shortcuts = false;
  }
  function onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      // Consume the event so the reader's separate window key handler doesn't
      // also act on this Esc and navigate back to the library.
      e.stopImmediatePropagation();
      close();
    }
  }
</script>

<svelte:window onkeydown={ui.shortcuts ? onKeydown : undefined} />

{#if ui.shortcuts}
  <div class="overlay" role="presentation" onclick={close}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div
      class="sheet"
      role="dialog"
      tabindex="-1"
      aria-modal="true"
      aria-label="Keyboard shortcuts"
      onclick={(e) => e.stopPropagation()}
      {@attach focusTrap}
    >
      <header>
        <div class="head-text">
          <p class="eyebrow">Help</p>
          <h2 class="display">Keyboard shortcuts</h2>
        </div>
        <button
          class="icon-btn press close"
          aria-label="Close"
          onclick={close}
          {@attach (el) => (el as HTMLButtonElement).focus()}
          ><Icon icon={X} size={18} /></button
        >
      </header>
      <div class="groups">
        {#each groups as g (g.title)}
          <section>
            <h3 class="eyebrow">{g.title}</h3>
            <dl>
              {#each g.items as it (it.desc)}
                <div class="row">
                  <dt>
                    {#each it.keys as k (k)}<kbd class="kbd">{k}</kbd>{/each}
                  </dt>
                  <span class="leader" aria-hidden="true"></span>
                  <dd>{it.desc}</dd>
                </div>
              {/each}
            </dl>
          </section>
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  .overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: grid;
    place-items: center;
    padding: var(--sp-6);
    background: var(--veil);
    -webkit-backdrop-filter: blur(4px);
    backdrop-filter: blur(4px);
    animation: app-overlay-in var(--dur) var(--ease-out);
  }
  .sheet {
    width: min(34rem, 100%);
    max-height: calc(100dvh - var(--sp-6) - var(--sp-6));
    overflow-y: auto;
    background: var(--raised);
    border: 1px solid var(--hairline);
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-3);
    animation: app-sheet-in var(--dur-slow) var(--ease-out);
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--sp-3);
    padding: var(--sp-5) var(--sp-5) var(--sp-3);
    border-bottom: 1px solid var(--hairline);
  }
  .head-text {
    display: flex;
    flex-direction: column;
    gap: var(--sp-1);
  }
  .head-text .eyebrow {
    margin: 0;
  }
  h2 {
    margin: 0;
    font-size: var(--text-xl);
    font-weight: 540;
    line-height: var(--lh-tight);
  }
  .close {
    flex-shrink: 0;
  }
  .groups {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--sp-6);
    padding: var(--sp-5);
  }
  @media (max-width: 30rem) {
    .groups {
      grid-template-columns: 1fr;
    }
  }
  .eyebrow {
    margin: 0 0 var(--sp-3);
  }
  dl {
    margin: 0;
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    padding: 0.3rem 0;
  }
  dt {
    display: flex;
    gap: 0.25rem;
    flex-shrink: 0;
  }
  /* Printer's dotted leader between the keys and their description. */
  .leader {
    flex: 1;
    border-bottom: 1px dotted var(--hairline-strong);
    transform: translateY(0.3em);
    min-width: 1rem;
  }
  dd {
    margin: 0;
    color: var(--muted);
    font-size: var(--text-sm);
    text-align: right;
    flex-shrink: 0;
  }
</style>
