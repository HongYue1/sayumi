// Where a mouse has to be for the auto-hidden reader chrome to come back.
//
// Shared by the shell (Read's window listener) and the reader frame
// (frame.ts), because the frame covers the whole stage and the parent only
// sees the pointer while it is over shell elements. Both sides must agree on
// one rule, or moving from the frame onto the bar would flip behaviour.
//
// Movement anywhere else is reading, not a request for the controls. An
// earlier version revealed on any movement, then on accumulated travel; both
// fired on ordinary mouse use because a normal hand movement easily covers
// any travel threshold small enough to feel responsive. Aiming at the edge
// where the controls live is the only signal that means "show me the bar".
//
// The stage is `inset: 0` inside `.rdp`, so the frame viewport and the window
// share their top edge and clientY is comparable on both sides.
export const CHROME_REVEAL_ZONE_PX = 72;

export function inChromeRevealZone(clientY: number): boolean {
  return clientY >= 0 && clientY <= CHROME_REVEAL_ZONE_PX;
}
