// The alarm going off, as a screen-blocking modal rather than a toast.
//
// A toast was the wrong shape for this: it is the same transient cue the app
// uses for "Bookmark added", it faded on its own timer, and an alarm that
// expires unseen has failed at the one thing it was set to do. This sheet
// stays until it is dismissed, so the alarm cannot be missed by looking away.
//
// It is portaled to document.body for the reason CustomThemeDialog documents:
// rendered in place it lands inside the reader's furniture, and an ancestor
// with a backdrop-filter becomes the containing block for `position: fixed`,
// so a "whole screen" veil would cover only that corner.
//
// Escape is a capture-phase window listener, like the other reader overlays,
// so the key that dismisses the alarm does not also navigate the reader back.
//
// The chime repeats while the sheet is up -- a single note is missable in a
// noisy room -- but only for CHIME_LIMIT notes. Past that the sheet alone
// carries the alarm: a reader who left the room should not come back to a
// laptop that has been beeping for an hour.
import { createEffect, onSettled } from "solid-js";
import { Portal } from "@solidjs/web";
import Icon from "~/lib/Icon";
import { AlarmClock } from "~/lib/icons";
import { clock, formatClock, playAlarmChime } from "~/lib/clock";
import { trap } from "~/lib/focusTrap";

interface Props {
  /** The instant the alarm was set for, in epoch ms. */
  at: number;
  ondismiss: () => void;
}

const CHIME_MS = 3500;
const CHIME_LIMIT = 12;

export default function AlarmDialog(props: Props) {
  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    props.ondismiss();
  }

  // Mounted only while ringing, so the listener's lifetime is the component's.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    return () => window.removeEventListener("keydown", onKeydown, true);
  });

  // The first note is played by whoever opened this sheet (the pill, on the
  // tick the alarm came due); this effect owns the repeats.
  createEffect(
    () => props.at,
    () => {
      let left = CHIME_LIMIT;
      const id = setInterval(() => {
        left -= 1;
        if (left <= 0) {
          clearInterval(id);
          return;
        }
        playAlarmChime();
      }, CHIME_MS);
      return () => clearInterval(id);
    },
  );

  const time = (): string => formatClock(new Date(props.at), clock.hour24);

  return (
    <Portal>
      <div class="alarm-overlay" role="presentation">
        {/* Pointer-only dismissal of the veil, matching the app's other
            overlays -- but it dismisses the alarm, because a veil that closed
            without stopping the alarm would be a lie. */}
        <button
          type="button"
          class="backdrop-dismiss alarm-backdrop"
          aria-label="Dismiss alarm"
          tabindex="-1"
          onClick={props.ondismiss}
        />
        {/* role="alertdialog": this interrupts on purpose, and its message is
            the reason it opened, so it is announced with the sheet. */}
        <div
          class="alarm-sheet paper"
          role="alertdialog"
          tabindex="-1"
          aria-modal="true"
          aria-labelledby="alarm-title"
          aria-describedby="alarm-time"
          ref={trap()}
        >
          <span class="alarm-bell" aria-hidden="true">
            <Icon icon={AlarmClock} size={30} stroke={1.8} decorative />
          </span>
          <h2 class="display alarm-title" id="alarm-title">
            Alarm
          </h2>
          <p class="alarm-time tnum" id="alarm-time">
            {time()}
          </p>
          <button
            type="button"
            class="btn press alarm-stop"
            onClick={props.ondismiss}
          >
            Turn off
          </button>
        </div>
      </div>
    </Portal>
  );
}
