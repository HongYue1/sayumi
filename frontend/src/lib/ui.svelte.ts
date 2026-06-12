// Tiny shared UI state for global overlays (command palette, shortcuts help)
// so any component can open them without prop-drilling or event plumbing.
class UIState {
  palette = $state(false);
  shortcuts = $state(false);

  togglePalette(): void {
    this.palette = !this.palette;
  }
  openShortcuts(): void {
    this.palette = false;
    this.shortcuts = true;
  }
}

export const ui = new UIState();
