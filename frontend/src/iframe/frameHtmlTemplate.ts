// frameHtmlTemplate.ts — the reader iframe's document shell, as a pure function.
//
// Split out of buildFrameHtml.ts so it can be unit-tested: that module imports
// `virtual:frame-script`, which exists only once vite's frameScriptPlugin has
// run, and vitest.config.ts deliberately keeps that plugin out of the test run.
// Everything with a decision in it lives here; buildFrameHtml.ts is just the
// binding of the two build-time payloads.
//
// This shell owns four contracts the rest of src/iframe reads back:
//   - CSP. Scripts are nonce-only and connect-src is 'none'; asset sources stay
//     permissive on purpose (see iframe/AGENTS.md — real EPUBs break if they
//     are tightened). style-src is inline-only: every stylesheet in this
//     document is one of the <style> slots below, written via textContent.
//   - The <style> slots and their cascade order: base-css, initial-theme-css,
//     font-face-css, book-css, override-css. frame.ts writes the last three by
//     id and depends on override-css coming after base-css.
//   - The #paged-clip > #content > #content-inner skeleton, which frame.ts
//     resolves by id.
//   - The html.theme-<id> class, which frame.ts reads back to seed its
//     activeThemeClass.
//
// This module renders in the PARENT document (buildFrameHtml.ts, ChapterFrame)
// and is never part of the frame bundle, so it can read the shell's theme
// registry directly rather than keeping a second copy of the default.
import { DEFAULT_THEME_ID } from "~/lib/themes";

/** Caller-supplied options; the payloads are bound in buildFrameHtml.ts. */
export interface FrameSrcdocOptions {
  /**
   * Script nonce. Must survive [^a-zA-Z0-9-] stripping, which every arm of
   * createFrameNonce() below guarantees.
   */
  nonce: string;
  /** Theme id, e.g. "catppuccin", or a custom theme's id. */
  theme: string;
  /**
   * Resolved palette for a custom theme, which has no static html.theme-<id>
   * rule in frame.css; null for built-ins. Without it the first paint falls
   * through to frame.css's bare `html` rule — the light palette — so a custom
   * dark theme flashes white until the parent's first apply-settings lands.
   */
  themeVars?: string | null;
  /** Book language for the initial document; frame.ts re-sets it per chapter. */
  language?: string | null;
}

export interface FrameSrcdocInput extends FrameSrcdocOptions {
  frameCSS: string;
  frameScript: string;
}

// <style> and <script> are HTML raw-text elements: the parser ends them at the
// literal "</style" / "</script" whatever the CSS or JS context, so a payload
// containing one would close the tag early and spill the remainder into the
// body. A backslash before the slash is inert in both languages — an escaped
// "/" inside a string or regex literal, a no-op inside a comment — so this
// neutralizes the terminator without changing what the payload means.
const RAW_TEXT_CLOSE: Record<"style" | "script", RegExp> = {
  style: /<\/(?=style)/gi,
  script: /<\/(?=script)/gi,
};

function escapeRawText(payload: string, tag: "style" | "script"): string {
  return payload.replace(RAW_TEXT_CLOSE[tag], "<\\/");
}

/** Built-in ids are lowercase-kebab; custom ids are minted server-side. */
const THEME_ID = /^[a-z0-9-]{1,64}$/;

// Counter for the no-WebCrypto arm of createFrameNonce, so two frames built in
// the same millisecond cannot collide.
let nonceCounter = 0;

/**
 * Mints the script nonce for one frame document.
 *
 * crypto.randomUUID is SECURE-CONTEXT ONLY. Sayumi is documented as a
 * plain-HTTP LAN server, and over http://192.168.x.x the property is
 * undefined: calling it threw while the reader was building its srcdoc, the
 * Errored boundary swallowed it, and the book never rendered while the rest
 * of the app worked. Never reach for it unguarded here again.
 *
 * crypto.getRandomValues is NOT gated on a secure context, so the fallback
 * keeps the same unpredictability on exactly the deployment that lost it. The
 * final arm is for a runtime with no WebCrypto at all, where uniqueness is the
 * only property still available to promise -- and uniqueness is all the CSP
 * nonce needs to keep the engine running.
 *
 * Every arm emits [0-9a-z-] only, so renderFrameSrcdoc's strip is a no-op and
 * can never empty the nonce.
 */
export function createFrameNonce(
  source: Partial<Crypto> | undefined = globalThis.crypto,
): string {
  if (typeof source?.randomUUID === "function") return source.randomUUID();
  if (typeof source?.getRandomValues === "function") {
    const bytes = source.getRandomValues(new Uint8Array(16));
    let hex = "";
    for (const byte of bytes) hex += byte.toString(16).padStart(2, "0");
    return hex;
  }
  nonceCounter += 1;
  const noise = Math.random().toString(36).slice(2);
  return `f${Date.now().toString(36)}-${nonceCounter.toString(36)}-${noise}`;
}

export function renderFrameSrcdoc(input: FrameSrcdocInput): string {
  const nonce = input.nonce.replace(/[^a-zA-Z0-9-]/g, "");
  if (!nonce) {
    // Never emit `script-src 'nonce-'`: it is well-formed CSP that matches
    // nothing, so the engine would be blocked and the reader would come up
    // blank with no error anywhere.
    throw new Error("renderFrameSrcdoc: nonce has no usable characters");
  }

  // Validate rather than strip. Stripping turns "Sepia" into "epia", a
  // syntactically fine class that matches no rule, so the frame would render
  // unstyled instead of falling back to a real theme. The fallback is the
  // shell's default theme, so an illegal id cannot open the reader in a palette
  // the rest of the app never paints.
  const theme = THEME_ID.test(input.theme) ? input.theme : DEFAULT_THEME_ID;

  // Same sanitizer as frame.ts's load handler, so the initial lang and the
  // per-chapter one can never disagree about what is legal.
  const language = (input.language ?? "")
    .replace(/[^a-zA-Z0-9-]/g, "")
    .slice(0, 35);
  const langAttr = language ? ` lang="${language}"` : "";

  const vars = input.themeVars ? escapeRawText(input.themeVars, "style") : "";
  const initialThemeCSS = vars ? `html { ${vars} }` : "";

  return `<!DOCTYPE html>
<html${langAttr} class="theme-${theme}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Book content</title>
  <meta http-equiv="Content-Security-Policy"
        content="default-src 'none';
                 base-uri 'none';
                 form-action 'none';
                 object-src 'none';
                 style-src 'unsafe-inline';
                 font-src * data: blob:;
                 img-src * data: blob:;
                 media-src * data: blob:;
                 script-src 'nonce-${nonce}';
                 connect-src 'none';
                 frame-src 'none';">
  <style id="base-css">${escapeRawText(input.frameCSS, "style")}</style>
  <style id="initial-theme-css">${initialThemeCSS}</style>
  <style id="font-face-css"></style>
  <style id="book-css"></style>
  <style id="override-css"></style>
</head>
<body>
  <div id="paged-clip">
    <div id="content"><div id="content-inner"></div></div>
  </div>
  <script nonce="${nonce}">${escapeRawText(input.frameScript, "script")}</script>
</body>
</html>`;
}
