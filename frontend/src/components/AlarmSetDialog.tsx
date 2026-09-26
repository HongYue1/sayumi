// "Set an alarm": the sheet behind the chrome's alarm button and the A key.
//
// Arming used to live in the settings panel, three sections down, which is the
// wrong place for something a reader does mid-chapter ("stop at half past").
// The settings panel keeps the clock PREFERENCES -- show it, 12/24, count down
// -- and now opens this sheet for the alarm itself.
//
// Structurally a sibling of AboutDialog, and for the reasons documented there:
// ref={trap()} owns focus-on-mount and the restore to whatever opened it, and
// the Escape listener is window-capture so one Esc closes this sheet without
// also navigating the reader back.
//
// The time field is a DRAFT, committed by the Set button. <input type="time">
// reports every keystroke, so arming on input would fire an alarm for 01:00
// while "01:30" was still being typed.
import { createEffect, createSignal, Show } from "solid-js";
import Icon from "~/lib/Icon";
import { AlarmClock, X } from "~/lib/icons";
import {
  alarmTargetFrom,
  clock,
  formatClock,
  formatRemaining,
  hhmmFrom,
  MAX_ALARM_LABEL,
} from "~/lib/clock";
import { preloadAlarmSound } from "~/lib/alarmSound";
import { trap } from "~/lib/focusTrap";
import { toast } from "~/lib/toast";
import { ui } from "~/lib/ui";

/** Seeded an hour out when nothing is armed: a reading session's alarm is
 *  "about an hour from now" far more often than it is 00:00. */
const DEFAULT_OFFSET_MS = 3_600_000;
/** The confirmation carries a time to read back and compare against the one
 *  you meant, which does not fit in a 2s glance. */
const ALARM_TOAST_MS = 4500;

function close(): void {
  ui.closeOverlays();
}

export default function AlarmSetDialog() {
  const [time, setTime] = createSignal("");
  const [label, setLabel] = createSignal("");

  function onKeydown(e: KeyboardEvent): void {
    if (e.key !== "Escape" || e.isComposing) return;
    e.preventDefault();
    e.stopImmediatePropagation();
    close();
  }

  // One effect for both jobs the open state owns: the Escape listener, and
  // seeding the draft from whatever is armed right now. Seeding on open rather
  // than at mount matters -- this sheet is mounted for the whole session, so a
  // once-only seed would show a stale time every time after the first.
  createEffect(
    () => ui.alarm,
    (open) => {
      if (!open) return undefined;
      const armed = clock.alarmAt;
      setTime(hhmmFrom(new Date(armed ?? Date.now() + DEFAULT_OFFSET_MS)));
      setLabel(clock.alarmLabel);
      window.addEventListener("keydown", onKeydown, true);
      return () => window.removeEventListener("keydown", onKeydown, true);
    },
  );

  function submit(e: Event): void {
    e.preventDefault();
    const at = alarmTargetFrom(Date.now(), time());
    if (at === null) {
      toast.show("Enter a time as HH:MM");
      return;
    }
    clock.setAlarm(at, label());
    // Warmed here, inside the click that armed it: this is the one moment with
    // a user activation to spend, so the ring is decoded and allowed to play
    // by the time it is needed.
    preloadAlarmSound();
    // The target, not the typed text: a time already past today arms for
    // tomorrow, and the reader should be told which one they got.
    const when = formatClock(new Date(at), clock.hour24);
    const left = formatRemaining(at - Date.now());
    toast.show(`Alarm set for ${when} \u00b7 in ${left}`, ALARM_TOAST_MS);
    close();
  }

  function clear(): void {
    clock.setAlarm(null);
    toast.show("Alarm cleared");
    close();
  }

  return (
    <Show when={ui.alarm}>
      <div class="about-overlay" role="presentation">
        <button
          type="button"
          class="backdrop-dismiss"
          aria-label="Close"
          tabindex="-1"
          onClick={close}
        />
        <div
          class="alarm-set-sheet"
          role="dialog"
          tabindex="-1"
          aria-modal="true"
          aria-labelledby="alarm-set-eyebrow alarm-set-title"
          ref={trap()}
        >
          <button
            type="button"
            class="icon-btn press alarm-set-close"
            aria-label="Close"
            onClick={close}
          >
            <Icon icon={X} size={18} labelFromParent />
          </button>

          <div class="alarm-set-head">
            <span class="alarm-set-bell" aria-hidden="true">
              <Icon icon={AlarmClock} size={18} stroke={1.9} decorative />
            </span>
            <div>
              <p class="eyebrow" id="alarm-set-eyebrow">
                Reader
              </p>
              <h2 class="display alarm-set-title" id="alarm-set-title">
                Set an alarm
              </h2>
            </div>
          </div>

          {/* A real form, so Enter in either field arms the alarm. */}
          <form class="alarm-set-form" onSubmit={submit}>
            <label class="alarm-set-field">
              <span>Time</span>
              <input
                type="time"
                class="alarm-set-time tnum"
                value={time()}
                onInput={(e) => setTime(e.currentTarget.value)}
              />
            </label>
            <label class="alarm-set-field">
              <span>Name (optional)</span>
              <input
                type="text"
                class="alarm-set-name"
                placeholder="Stop for dinner"
                maxlength={MAX_ALARM_LABEL}
                value={label()}
                onInput={(e) => setLabel(e.currentTarget.value)}
              />
            </label>

            {/* Live state for the armed alarm, so the sheet says what the pill
                says. role="status" is safe here (unlike on the pill itself):
                this text changes only when the alarm is armed or cleared. */}
            <p class="alarm-set-state" role="status">
              {clock.alarmAt === null
                ? "No alarm set."
                : `Armed for ${formatClock(
                    new Date(clock.alarmAt),
                    clock.hour24,
                  )}.`}
            </p>

            <div class="alarm-set-actions">
              <Show when={clock.alarmAt !== null}>
                <button type="button" class="btn-ghost press" onClick={clear}>
                  Clear
                </button>
              </Show>
              <button type="submit" class="btn press">
                {clock.alarmAt === null ? "Set alarm" : "Update alarm"}
              </button>
            </div>
          </form>
        </div>
      </div>
    </Show>
  );
}
