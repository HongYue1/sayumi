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
    id: "literata",
    label: "Literata",
    family: "'Literata', Georgia, serif",
    category: "serif",
  },
  {
    id: "atkinson-next",
    label: "Atkinson Hyperlegible Next",
    family: "'Atkinson Hyperlegible Next', Helvetica, Arial, sans-serif",
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
