import frameCSS from "./frame.css?raw";
import frameScript from "virtual:frame-script";

export function buildFrameSrcdoc(nonce: string, initialTheme: string): string {
  const safeNonce = nonce.replace(/[^a-zA-Z0-9-]/g, "");
  const safeTheme = initialTheme.replace(/[^a-z0-9-]/g, "") || "light";

  return `<!DOCTYPE html>
<html class="theme-${safeTheme}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta http-equiv="Content-Security-Policy"
        content="default-src 'none';
                 base-uri 'none';
                 form-action 'none';
                 object-src 'none';
                 style-src 'unsafe-inline' *;
                 font-src * data: blob:;
                 img-src * data: blob:;
                 media-src *;
                 script-src 'nonce-${safeNonce}';
                 connect-src 'none';
                 frame-src 'none';">
  <style id="base-css">${frameCSS}</style>
  <style id="font-face-css"></style>
  <style id="book-css"></style>
  <style id="override-css"></style>
</head>
<body>
  <div id="paged-clip">
    <div id="content"><div id="content-inner"></div></div>
  </div>
  <script nonce="${safeNonce}">${frameScript}</script>
</body>
</html>`;
}
