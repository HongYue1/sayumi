export interface ReaderFont {
  id: string;
  label: string;
  family: string;
  category: "serif" | "sans-serif";
}

// Only the two fonts embedded in the binary are listed here. The rest of the
// catalogue ships as drop-in families in ./Fonts/ (see fonts-bundle/) and is
// surfaced dynamically through the user-font registry.
export const READER_FONTS: ReaderFont[] = [
  {
    id: "eb-garamond",
    label: "EB Garamond",
    family: "'EB Garamond', Garamond, serif",
    category: "serif",
  },
  {
    id: "atkinson",
    label: "Atkinson Hyperlegible",
    family: "'Atkinson Hyperlegible', Helvetica, Arial, sans-serif",
    category: "sans-serif",
  },
];

const FONT_MAP = new Map(READER_FONTS.map((f) => [f.id, f] as const));

export function getFontById(id: string): ReaderFont | undefined {
  return FONT_MAP.get(id);
}

export function getFontFamily(id: string): string {
  return FONT_MAP.get(id)?.family ?? READER_FONTS[0].family;
}
