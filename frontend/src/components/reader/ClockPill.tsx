// The reader's clock pill: wall-clock time, plus the armed alarm beside it.
//
// Placement is the caller's mode, not a preference: in paged modes the book's
// own page pill owns the bottom-right corner (#page-indicator in
// iframe/frame.css) and the book-position chip owns the left, so the clock
// takes the middle. Scroll mode has no page pill, so it takes the right corner
// instead -- closer to the thumb, and off the centre line of the text.
//
// It rides with the bottom furniture rather than the top bar: the whole point
// of a clock while reading is that a glance answers it, and a pill that hides
// with the chrome would need the chrome brought back to be read.
//
// Deliberately not a live region. The position chip next door documents the
// same decision: a role="status" here would make screen readers interrupt
// every minute, forever. The label is on the element for anyone who navigates
// to it.
//
// Solid 2.0 notes:
//   - The 1s tick is a compute/apply createEffect, so the interval exists only
//     while something needs it and tears down with the route.
//   - The rendered strings are memos, so a tick that does not change the
//     minute (59 of every 60) writes nothing to the DOM.
import { createEffect, createMemo, createSignal, Show } from "solid-js";
import Icon from "~/lib/Icon";
import { AlarmClock } from "~/lib/icons";
import {
  clock,
  formatClock,
  formatRemaining,
  playAlarmChime,
} from "~/lib/clock";
import { toast } from "~/lib/toast";

interface Props {
  /** True in single-page and two-page modes (where the page pill exists). */
  paged: boolean;
}

const TICK_MS = 1000;
/** How long the pill stays flagged after the alarm comes due. The toast is the
 *  announcement; this is the trace left on screen for a reader who looked away
 *  while it faded. */
const FLASH_MS = 8000;

export default function ClockPill(props: Props) {
  const [nowMs, setNowMs] = createSignal(Date.now());
  const [rang, setRang] = createSignal(false);
  let flashTimer: ReturnType<typeof setTimeout> | undefined;

  // An armed alarm has to be watched even with the pill switched off: the
  // alarm is a separate promise from the clock, and turning the clock off is
  // not "cancel my alarm".
  const ticking = createMemo(() => clock.show || clock.alarmAt !== null);

  function ring(at: number): void {
    // Cleared first: a one-shot alarm, and clearing before the toast means a
    // slow toast render cannot let the next tick ring it twice.
    clock.setAlarm(null);
    setRang(true);
    toast.show(`Alarm \u2014 ${formatClock(new Date(at), clock.hour24)}`);
    playAlarmChime();
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => setRang(false), FLASH_MS);
  }

  createEffect(
    () => ticking(),
    (on) => {
      if (!on) return undefined;
      const id = setInterval(() => {
        const now = Date.now();
        setNowMs(now);
        const at = clock.alarmAt;
        // >= rather than a window: a sleeping laptop wakes with the instant
        // long past, and a due alarm must still ring on the first tick after.
        if (at !== null && now >= at) ring(at);
      }, TICK_MS);
      return () => {
        clearInterval(id);
        clearTimeout(flashTimer);
      };
    },
  );

  const timeText = createMemo(() =>
    formatClock(new Date(nowMs()), clock.hour24),
  );
  const alarmText = createMemo(() => {
    const at = clock.alarmAt;
    if (at === null) return null;
    return clock.showRemaining
      ? formatRemaining(at - nowMs())
      : formatClock(new Date(at), clock.hour24);
  });
  const label = createMemo(() => {
    const alarm = alarmText();
    return alarm === null
      ? `Time ${timeText()}`
      : `Time ${timeText()}, alarm ${alarm}`;
  });

  return (
    <Show when={clock.show}>
      <div
        class={[
          "rdp-clock tnum",
          { "rdp-clock-mid": props.paged, "rdp-clock-rang": rang() },
        ]}
        aria-label={label()}
      >
        <span>{timeText()}</span>
        <Show when={alarmText()}>
          {(text) => (
            <>
              <span class="rdp-clock-sep" aria-hidden="true" />
              <span class="rdp-clock-alarm">
                <Icon icon={AlarmClock} size={12} stroke={2} decorative />
                {text()}
              </span>
            </>
          )}
        </Show>
      </div>
    </Show>
  );
}
