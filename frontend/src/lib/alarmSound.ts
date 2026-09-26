// The alarm's ring: the bundled alarm.mp3, looped until it is turned off.
//
// This replaced a synthesised two-note WebAudio chime. A synth tone is cheap
// to ship but it does not sound like an alarm -- it sounds like a UI blip, and
// a reader deep in a chapter does not read "stop what you are doing" from it.
// The file is 11s of mono 96kbps in frontend/public, so the binary carries
// ~130KB for it and the browser caches it with every other static asset.
//
// One module-level element, not one per ring: a fresh <audio> per alarm leaks
// a decoder per fire, and the element also has to be reachable to stop the
// loop from the dialog's dismissal.
//
// Everything here is best-effort. play() rejects when the tab has no user
// activation to spend, and the sheet is the alert that must not depend on
// audio: a muted ring still leaves a modal on screen. Nothing throws.
const SRC = "/alarm.mp3";

let element: HTMLAudioElement | null = null;

function audio(): HTMLAudioElement | null {
  if (typeof Audio === "undefined") return null;
  if (element === null) {
    try {
      element = new Audio(SRC);
      // Loops rather than repeating on a timer: gapless, and the element is
      // the single thing to stop.
      element.loop = true;
      element.preload = "auto";
    } catch {
      return null;
    }
  }
  return element;
}

/** Starts (or restarts) the ring. Safe to call while it is already playing. */
export function startAlarmSound(): void {
  const el = audio();
  if (el === null) return;
  try {
    el.currentTime = 0;
    void el.play().catch(() => {});
  } catch {
    // See the header: silence is an acceptable degradation.
  }
}

/** Stops the ring and rewinds it, so the next alarm starts from the top. */
export function stopAlarmSound(): void {
  if (element === null) return;
  try {
    element.pause();
    element.currentTime = 0;
  } catch {
    // Ignored for the same reason as above.
  }
}

/** Warms the decoder while the reader is idle, so the first alarm of a session
 *  does not spend its first second fetching. */
export function preloadAlarmSound(): void {
  const el = audio();
  try {
    el?.load();
  } catch {
    // Ignored.
  }
}
