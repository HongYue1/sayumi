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

/** Caller-supplied options; the payloads are bound in buildFrameHtml.ts. */
export interface FrameSrcdocOptions {
  /** Script nonce. Must survive [^a-zA-Z0-9-] stripping; crypto.randomUUID does. */
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
  // unstyled instead of falling back to a real theme.
  const theme = THEME_ID.test(input.theme) ? input.theme : "light";

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
