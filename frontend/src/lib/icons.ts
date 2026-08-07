/*
 * Inline Lucide icon geometry - the glyphs this UI actually uses.
 *
 * Originally taken from @lucide/svelte v1.28.0 (ISC licence); that package is
 * no longer a dependency. lucide-solid is not an option on Solid 2: 3,520 of
 * its dist files import the removed "solid-js/web", and its Icon/context
 * modules use splitProps, which 2.0 replaced with the inverted, rest-only
 * omit. Inlining the geometry drops the last runtime dependency and ships less
 * than the tree-shaken package did.
 *
 * Two upstream renames are folded in so the call sites keep reading in English:
 * CircleHelp is circle-question-mark and UploadCloud is cloud-upload upstream.
 *
 * Adding an icon: copy the iconNode array off lucide.dev and export it below in
 * PascalCase. Values MUST stay inert geometry - no quotes, angle brackets or
 * ampersands - because Icon.tsx serialises them straight into innerHTML with
 * no escaping at all. Nothing upstream asserts that; icons.test.ts does. It is
 * a real failure mode, not a theoretical one: a d of
 * M0 0" data-injected="yes  renders as a path carrying an extra attribute.
 *
 * Everything here is stroke-only and inherits the shell fill of none, EXCEPT
 * Tag, whose dot carries fill currentColor and deliberately overrides it.
 */

/** A Lucide glyph: the SVG children of a 24x24 viewBox, upstream tuple shape. */
export type IconNode = ReadonlyArray<
  readonly [tag: string, attrs: Readonly<Record<string, string>>]
>;

/** lucide `arrow-left` */
export const ArrowLeft: IconNode = [
  ["path", { d: "m12 19-7-7 7-7" }],
  ["path", { d: "M19 12H5" }],
];

/** lucide `arrow-up-down` */
export const ArrowUpDown: IconNode = [
  ["path", { d: "m21 16-4 4-4-4" }],
  ["path", { d: "M17 20V4" }],
  ["path", { d: "m3 8 4-4 4 4" }],
  ["path", { d: "M7 4v16" }],
];

/** lucide `bookmark` */
export const Bookmark: IconNode = [
  [
    "path",
    {
      d: "M17 3a2 2 0 0 1 2 2v15a1 1 0 0 1-1.496.868l-4.512-2.578a2 2 0 0 0-1.984 0l-4.512 2.578A1 1 0 0 1 5 20V5a2 2 0 0 1 2-2z",
    },
  ],
];

/** lucide `bookmark-check` */
export const BookmarkCheck: IconNode = [
  [
    "path",
    {
      d: "M17 3a2 2 0 0 1 2 2v15a1 1 0 0 1-1.496.868l-4.512-2.578a2 2 0 0 0-1.984 0l-4.512 2.578A1 1 0 0 1 5 20V5a2 2 0 0 1 2-2z",
    },
  ],
  ["path", { d: "m9 10 2 2 4-4" }],
];

/** lucide `book-marked` */
export const BookMarked: IconNode = [
  ["path", { d: "M10 2v8l3-3 3 3V2" }],
  [
    "path",
    {
      d: "M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H19a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H6.5a1 1 0 0 1 0-5H20",
    },
  ],
];

/** lucide `check` */
export const Check: IconNode = [["path", { d: "M20 6 9 17l-5-5" }]];

/** lucide `chevron-down` */
export const ChevronDown: IconNode = [["path", { d: "m6 9 6 6 6-6" }]];

/** lucide `circle-question-mark` */
export const CircleHelp: IconNode = [
  ["circle", { cx: "12", cy: "12", r: "10" }],
  ["path", { d: "M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" }],
  ["path", { d: "M12 17h.01" }],
];

/** lucide `copy` */
export const Copy: IconNode = [
  ["rect", { width: "14", height: "14", x: "8", y: "8", rx: "2", ry: "2" }],
  ["path", { d: "M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" }],
];

/** lucide `download` */
export const Download: IconNode = [
  ["path", { d: "M12 15V3" }],
  ["path", { d: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" }],
  ["path", { d: "m7 10 5 5 5-5" }],
];

/** lucide `ellipsis` */
export const Ellipsis: IconNode = [
  ["circle", { cx: "12", cy: "12", r: "1" }],
  ["circle", { cx: "19", cy: "12", r: "1" }],
  ["circle", { cx: "5", cy: "12", r: "1" }],
];

/** lucide `image-up` */
export const ImageUp: IconNode = [
  [
    "path",
    {
      d: "M10.3 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v10l-3.1-3.1a2 2 0 0 0-2.814.014L6 21",
    },
  ],
  ["path", { d: "m14 19.5 3-3 3 3" }],
  ["path", { d: "M17 22v-5.5" }],
  ["circle", { cx: "9", cy: "9", r: "2" }],
];

/** lucide `list` */
export const List: IconNode = [
  ["path", { d: "M3 5h.01" }],
  ["path", { d: "M3 12h.01" }],
  ["path", { d: "M3 19h.01" }],
  ["path", { d: "M8 5h13" }],
  ["path", { d: "M8 12h13" }],
  ["path", { d: "M8 19h13" }],
];

/** lucide `lock` */
export const Lock: IconNode = [
  ["rect", { width: "18", height: "11", x: "3", y: "11", rx: "2", ry: "2" }],
  ["path", { d: "M7 11V7a5 5 0 0 1 10 0v4" }],
];

/** lucide `log-out` */
export const LogOut: IconNode = [
  ["path", { d: "m16 17 5-5-5-5" }],
  ["path", { d: "M21 12H9" }],
  ["path", { d: "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" }],
];

/** lucide `pencil` */
export const Pencil: IconNode = [
  [
    "path",
    {
      d: "M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z",
    },
  ],
  ["path", { d: "m15 5 4 4" }],
];

/** lucide `plus` */
export const Plus: IconNode = [
  ["path", { d: "M5 12h14" }],
  ["path", { d: "M12 5v14" }],
];

/** lucide `refresh-cw` */
export const RefreshCw: IconNode = [
  ["path", { d: "M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" }],
  ["path", { d: "M21 3v5h-5" }],
  ["path", { d: "M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" }],
  ["path", { d: "M8 16H3v5" }],
];

/** lucide `search` */
export const Search: IconNode = [
  ["path", { d: "m21 21-4.34-4.34" }],
  ["circle", { cx: "11", cy: "11", r: "8" }],
];

/** lucide `settings` */
export const Settings: IconNode = [
  [
    "path",
    {
      d: "M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915",
    },
  ],
  ["circle", { cx: "12", cy: "12", r: "3" }],
];

/** lucide `share-2` */
export const Share2: IconNode = [
  ["circle", { cx: "18", cy: "5", r: "3" }],
  ["circle", { cx: "6", cy: "12", r: "3" }],
  ["circle", { cx: "18", cy: "19", r: "3" }],
  ["line", { x1: "8.59", x2: "15.42", y1: "13.51", y2: "17.49" }],
  ["line", { x1: "15.41", x2: "8.59", y1: "6.51", y2: "10.49" }],
];

/** lucide `tag` */
export const Tag: IconNode = [
  [
    "path",
    {
      d: "M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z",
    },
  ],
  ["circle", { cx: "7.5", cy: "7.5", r: ".5", fill: "currentColor" }],
];

/** lucide `trash-2` */
export const Trash2: IconNode = [
  ["path", { d: "M10 11v6" }],
  ["path", { d: "M14 11v6" }],
  ["path", { d: "M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" }],
  ["path", { d: "M3 6h18" }],
  ["path", { d: "M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" }],
];

/** lucide `triangle-alert` */
export const TriangleAlert: IconNode = [
  [
    "path",
    {
      d: "m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3",
    },
  ],
  ["path", { d: "M12 9v4" }],
  ["path", { d: "M12 17h.01" }],
];

/** lucide `cloud-upload` */
export const UploadCloud: IconNode = [
  ["path", { d: "M12 13v8" }],
  ["path", { d: "M4 14.899A7 7 0 1 1 15.71 8h1.79a4.5 4.5 0 0 1 2.5 8.242" }],
  ["path", { d: "m8 17 4-4 4 4" }],
];

/** lucide `user` */
export const User: IconNode = [
  ["path", { d: "M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" }],
  ["circle", { cx: "12", cy: "7", r: "4" }],
];

/** lucide `x` */
export const X: IconNode = [
  ["path", { d: "M18 6 6 18" }],
  ["path", { d: "m6 6 12 12" }],
];

/** lucide `wifi-off` */
export const WifiOff: IconNode = [
  ["line", { x1: "2", x2: "22", y1: "2", y2: "22" }],
  ["path", { d: "M8.5 16.5a5 5 0 0 1 7 0" }],
  ["path", { d: "M2 8.82a15 15 0 0 1 4.17-2.65" }],
  ["path", { d: "M10.66 5c4.01-.36 8.14.9 11.34 3.76" }],
  ["path", { d: "M16.85 11.25a10 10 0 0 1 2.22 1.68" }],
  ["path", { d: "M5 13a10 10 0 0 1 5.24-2.76" }],
  ["line", { x1: "12", x2: "12.01", y1: "20", y2: "20" }],
];
