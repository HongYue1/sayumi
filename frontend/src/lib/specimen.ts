// Built-in typography specimen.
//
// The reader doubles as a live preview: navigating to /read/<SPECIMEN_BOOK_ID>
// renders this sample chapter entirely client-side (no book, progress, or
// bookmark server calls). It exercises every element the reader styles so a
// user can open Settings and tune their reading typography against rich text.
//
// The HTML is a plain semantic fragment with NO author CSS: it is injected into
// the iframe's #content-inner and styled solely by the reader's own frame.css +
// settings-derived overrides, so what the user sees is exactly what their
// chosen font, size, line-height, spacing, justification, heading sizes/weights
// and theme produce. The only inline styles are font-variant demos (small-caps,
// old-style vs lining numerals) that have no dedicated reader control but are
// worth seeing when judging a typeface.

import type { BookDetail, ChapterData } from "~/api/client";

/** Sentinel bookId the reader recognizes as the built-in specimen. */
export const SPECIMEN_BOOK_ID = "__specimen__";

// A tiny inline SVG (data URI) so the figure demo needs no network/resource
// base. CSP allows img-src data:.
const SAMPLE_IMAGE =
  "data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='320'%20height='150'%3E%3Crect%20width='320'%20height='150'%20rx='8'%20fill='%23cbd5e1'/%3E%3Ccircle%20cx='84'%20cy='75'%20r='38'%20fill='%2394a3b8'/%3E%3Crect%20x='146'%20y='50'%20width='132'%20height='15'%20rx='4'%20fill='%2394a3b8'/%3E%3Crect%20x='146'%20y='80'%20width='104'%20height='12'%20rx='4'%20fill='%23b4c0cf'/%3E%3C/svg%3E";

const SPECIMEN_HTML = `
<h1>Typography specimen</h1>
<p>This sample chapter gathers every kind of formatting the reader can render, so you can adjust your font, size, line height, spacing, justification, margins, and theme and see the effect immediately. Adjust any control in <strong>Settings</strong> and watch the text below reflow in real time&mdash;there is no need to open one of your own books to find the right balance.</p>
<p>The paragraph that follows is deliberately long, with a few uncommonly long words such as <em>incomprehensibilities</em> and <em>counterrevolutionary</em>, so you can judge how justification and hyphenation handle tight lines and how your chosen line height feels across a full block of running prose rather than a single short sentence.</p>

<h2>Heading levels</h2>
<p>The six heading levels below show the heading scale, weight, and spacing relative to body text.</p>
<h1>Heading one</h1>
<h2>Heading two</h2>
<h3>Heading three</h3>
<h4>Heading four</h4>
<h5>Heading five</h5>
<h6>Heading six</h6>

<h2>Inline formatting</h2>
<p>Body text can be <em>italic</em>, <strong>bold</strong>, <strong><em>bold italic</em></strong>, <u>underlined</u>, or <s>struck through</s>. It can carry <code>inline code</code>, a <a href="#">hyperlink</a>, H<sub>2</sub>O subscripts and E=mc<sup>2</sup> superscripts, and <span style="font-variant: small-caps">small capitals</span> for headers or abbreviations.</p>

<h2>Block quotation</h2>
<blockquote>
<p>Typography is the craft of endowing human language with a durable visual form, and thus with an independent existence.</p>
<p>&mdash; Robert Bringhurst</p>
</blockquote>

<h2>Lists</h2>
<p>An unordered list, with one nested level:</p>
<ul>
<li>First item</li>
<li>Second item
<ul>
<li>Nested item</li>
<li>Another nested item</li>
</ul>
</li>
<li>Third item</li>
</ul>
<p>An ordered list, with one nested level:</p>
<ol>
<li>Prepare the manuscript</li>
<li>Set the body text
<ol>
<li>Choose a typeface</li>
<li>Tune the measure and leading</li>
</ol>
</li>
<li>Print the proof</li>
</ol>

<h2>Code block</h2>
<pre><code>function greet(name) {
  // a short sample of monospaced text
  return \`Hello, \${name}!\`;
}</code></pre>

<h2>Table</h2>
<table>
<thead>
<tr><th>Typeface</th><th>Classification</th><th>Year</th></tr>
</thead>
<tbody>
<tr><td>Garamond</td><td>Old-style serif</td><td>1530</td></tr>
<tr><td>Baskerville</td><td>Transitional serif</td><td>1757</td></tr>
<tr><td>Helvetica</td><td>Grotesque sans</td><td>1957</td></tr>
</tbody>
</table>

<h2>Figure</h2>
<figure>
<img alt="A simple placeholder illustration" src="${SAMPLE_IMAGE}" />
<figcaption>Figure 1. A caption set in the reader's caption style.</figcaption>
</figure>

<hr />

<h2>Type details</h2>
<p>Ligatures: office, waffle, fluffier, affluent. Numerals, old-style <span style="font-variant-numeric: oldstyle-nums">0123456789</span> versus lining <span style="font-variant-numeric: lining-nums">0123456789</span>. Punctuation: &ldquo;curly quotes,&rdquo; an em&mdash;dash, and an ellipsis&hellip; Accents: na&iuml;ve caf&eacute; r&eacute;sum&eacute;, &OElig;uvre, Dvo&rcaron;&aacute;k.</p>
`;

/** A synthetic BookDetail for the specimen (single chapter, no server record). */
export function specimenBookDetail(): BookDetail {
  return {
    id: SPECIMEN_BOOK_ID,
    title: "Typography specimen",
    author: "Sayumi",
    language: "en",
    publisher: "",
    description: "",
    pubDate: "",
    hasCover: false,
    direction: "ltr",
    chapterCount: 1,
    progress: 0,
    spine: [
      {
        href: "specimen",
        id: "specimen",
        mediaType: "application/xhtml+xml",
        linear: true,
      },
    ],
    toc: [{ title: "Typography specimen", href: "specimen", depth: 0 }],
  };
}

/** The specimen's single chapter, rendered with reader typography (no book CSS). */
export function specimenChapter(): ChapterData {
  return {
    chapterIndex: 0,
    html: SPECIMEN_HTML,
    css: "",
    fontFaceCSS: "",
    direction: "ltr",
    writingMode: "horizontal-tb",
  };
}
