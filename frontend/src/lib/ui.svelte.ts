// Tiny shared UI state for global overlays (command palette, shortcuts help)
// so any component can open them without prop-drilling or event plumbing.
class UIState {
  palette = $state(false);
  shortcuts = $state(false);

  togglePalette(): void {
    this.palette = !this.palette;
    // Opening the palette dismisses the shortcuts sheet so the two
    // focus-trapped overlays can't stack (mirrors openShortcuts() closing the
    // palette). Closing the palette leaves shortcuts untouched.
    if (this.palette) this.shortcuts = false;
  }
  openShortcuts(): void {
    this.palette = false;
    this.shortcuts = true;
  }
}

export const ui = new UIState();
