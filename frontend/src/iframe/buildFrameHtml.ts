// buildFrameHtml.ts — build-time asset wiring for the reader iframe shell.
//
// Everything with a decision in it lives in frameHtmlTemplate.ts, which is pure
// and unit-tested. This module exists only to bind the two payloads that can't
// be imported from a test:
//   - frame.css as a raw string. A normal CSS import would inject the sheet
//     into the *parent* document; this one has to travel inside the srcdoc.
//   - the engine bundle from vite's frameScriptPlugin, which is a virtual
//     module and so is absent from the vitest resolver.
import frameCSS from "./frame.css?raw";
import frameScript from "virtual:frame-script";
import {
  renderFrameSrcdoc,
  type FrameSrcdocOptions,
} from "./frameHtmlTemplate";

export type { FrameSrcdocOptions };

export function buildFrameSrcdoc(options: FrameSrcdocOptions): string {
  return renderFrameSrcdoc({ ...options, frameCSS, frameScript });
}
