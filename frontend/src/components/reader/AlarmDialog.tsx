// The alarm going off, as a screen-blocking sheet rather than a toast.
//
// A toast was the wrong shape for this: it is the same transient cue the app
// uses for "Bookmark added", it faded on its own timer, and an alarm that
// expires unseen has failed at the one thing it was set to do. This sheet
// stays until it is turned off or snoozed.
//
// It is portaled to document.body for the reason CustomThemeDialog documents:
// rendered in place it lands inside the reader's furniture, and an ancestor
// with a backdrop-filter becomes the containing block for `position: fixed`,
// so a "whole screen" veil would cover only that corner.
//
// Escape is a capture-phase window listener, like the other reader overlays,
// so the key that dismisses the alarm does not also navigate the reader back.
//
// The ring is owned here, start to stop: the sheet's lifetime IS the alarm's
// audible lifetime, so there is no way to leave a loop playing behind a closed
// sheet. Snoozing re-arms with the same label -- an alarm called "Stop for
// dinner" is still that alarm ten minutes later.
import { For, onSettled, Show } from "solid-js";
import { Portal } from "@solidjs/web";
import Icon from "~/lib/Icon";
import { AlarmClock } from "~/lib/icons";
import { clock, formatClock, SNOOZE_MINUTES } from "~/lib/clock";
import { startAlarmSound, stopAlarmSound } from "~/lib/alarmSound";
import { toast } from "~/lib/toast";
import { trap } from "~/lib/focusTrap";

interface Props {
  /** The instant the alarm was set for, in epoch ms. */
  at: number;
  /** The alarm's name, or "" when it was armed without one. */
  label: string;
  ondismiss: () => void;
}

export default function AlarmDialog(props: Props) {
  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    props.ondismiss();
  }

  // Mounted only while ringing, so both the listener and the ring live exactly
  // as long as this component.
  onSettled(() => {
    window.addEventListener("keydown", onKeydown, true);
    startAlarmSound();
    return () => {
      window.removeEventListener("keydown", onKeydown, true);
      stopAlarmSound();
    };
  });

  function snooze(minutes: number): void {
    const next = Date.now() + minutes * 60_000;
    clock.setAlarm(next, props.label);
    toast.show(`Snoozed to ${formatClock(new Date(next), clock.hour24)}`, 3500);
    props.ondismiss();
  }

  return (
    <Portal>
      <div class="alarm-overlay" role="presentation">
        {/* Pointer-only dismissal of the veil, matching the app's other
            overlays -- but it turns the alarm off, because a veil that closed
            over a still-ringing alarm would be a lie. */}
        <button
          type="button"
          class="backdrop-dismiss alarm-backdrop"
          aria-label="Turn off alarm"
          tabindex="-1"
          onClick={props.ondismiss}
        />
        {/* role="alertdialog": this interrupts on purpose, and its message is
            the reason it opened, so it is announced with the sheet. */}
        <div
          class="alarm-sheet"
          role="alertdialog"
          tabindex="-1"
          aria-modal="true"
          aria-labelledby="alarm-eyebrow alarm-time"
          ref={trap()}
        >
          <div class="alarm-head">
            <span class="alarm-bell" aria-hidden="true">
              <Icon icon={AlarmClock} size={22} stroke={1.9} decorative />
            </span>
            <div class="alarm-head-text">
              <p class="eyebrow" id="alarm-eyebrow">
                Alarm
              </p>
              <p class="alarm-time tnum" id="alarm-time">
                {formatClock(new Date(props.at), clock.hour24)}
              </p>
            </div>
          </div>

          {/* Only rendered when the alarm was named: an empty line here would
              leave the sheet looking like it lost its message. */}
          <Show when={props.label !== ""}>
            <p class="alarm-note">{props.label}</p>
          </Show>

          <div class="alarm-actions">
            <button
              type="button"
              class="btn press alarm-stop"
              onClick={props.ondismiss}
            >
              Turn off
            </button>
            <div class="alarm-snooze" role="group" aria-label="Snooze">
              <For each={SNOOZE_MINUTES}>
                {(minutes) => (
                  <button
                    type="button"
                    class="alarm-snooze-btn press"
                    onClick={() => snooze(minutes)}
                  >
                    +{minutes}m
                  </button>
                )}
              </For>
            </div>
          </div>
        </div>
      </div>
    </Portal>
  );
}
