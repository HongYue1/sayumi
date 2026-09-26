// The reader's clock pill and its reading alarm.
//
// Per-DEVICE state, deliberately NOT part of lib/settings.ts, on cardSize.ts's
// reasoning: every field there round-trips through internal/api/setting.go and
// carries a storage column, so one more field is a schema change plus a
// migration. Nothing here describes the profile's reading style -- it
// describes the clock in front of you (a phone read in a 24-hour locale and a
// desktop read in a 12-hour one want different answers from the same profile),
// and an alarm can only ring on the device that armed it. One JSON key rather
// than four flags: the pill reads the whole group on every paint, so an atomic
// read/write keeps a half-applied state impossible.
//
// The four pure formatters below take their inputs as arguments and touch no
// storage, no Date.now() and no signals, so clock.test.ts can pin every
// boundary (midnight rollover, the hour/minute carry, 12-hour noon) without
// faking a clock.
import { createSignal } from "solid-js";

const KEY = "sayumi:clock";

/** Shape persisted under KEY. Every field is optional on read: a blob written
 *  by an older version must not lose the fields it does carry. */
interface ClockPrefs {
  /** Show the pill at all. On by default -- the feature is opt-OUT. */
  show: boolean;
  /** 24-hour clock instead of 12-hour with AM/PM. */
  hour24: boolean;
  /** Render the alarm as time-left ("50m") instead of its target time. */
  showRemaining: boolean;
  /** Epoch ms the alarm is due, or null when none is armed. */
  alarmAt: number | null;
}

const DEFAULTS: ClockPrefs = {
  show: true,
  hour24: false,
  showRemaining: false,
  alarmAt: null,
};

function read(): ClockPrefs {
  let raw: string | null;
  try {
    raw = localStorage.getItem(KEY);
  } catch {
    return { ...DEFAULTS };
  }
  if (raw === null || raw === "") return { ...DEFAULTS };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { ...DEFAULTS };
  }
  if (typeof parsed !== "object" || parsed === null) return { ...DEFAULTS };
  const o = parsed as Partial<Record<keyof ClockPrefs, unknown>>;
  return {
    show: typeof o.show === "boolean" ? o.show : DEFAULTS.show,
    hour24: typeof o.hour24 === "boolean" ? o.hour24 : DEFAULTS.hour24,
    showRemaining:
      typeof o.showRemaining === "boolean"
        ? o.showRemaining
        : DEFAULTS.showRemaining,
    // A stored alarm is honoured only while it is still in the future. A blob
    // written before a laptop slept for a week would otherwise ring the moment
    // the reader mounts, which reads as a bug rather than as an alarm.
    alarmAt:
      typeof o.alarmAt === "number" &&
      Number.isFinite(o.alarmAt) &&
      o.alarmAt > Date.now()
        ? o.alarmAt
        : null,
  };
}

const [prefs, setPrefs] = createSignal<ClockPrefs>(read());

/** Plain mirror of the signal's value, and the merge base for patch() below.
 *  A signal read is not guaranteed to observe a write made in the same tick,
 *  so merging onto prefs() would let two setters in one turn (switching to the
 *  24-hour clock and arming an alarm, say) drop the first write. */
let latest: ClockPrefs = prefs();

function write(next: ClockPrefs): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // Blocked or full storage must not break the pill: the signal is what the
    // reader renders and it is already correct for this tab.
  }
}

function patch(part: Partial<ClockPrefs>): void {
  const next = { ...latest, ...part };
  latest = next;
  setPrefs(next);
  write(next);
}

export const clock = {
  get show(): boolean {
    return prefs().show;
  },
  get hour24(): boolean {
    return prefs().hour24;
  },
  get showRemaining(): boolean {
    return prefs().showRemaining;
  },
  get alarmAt(): number | null {
    return prefs().alarmAt;
  },
  setShow(on: boolean): void {
    patch({ show: on });
  },
  setHour24(on: boolean): void {
    patch({ hour24: on });
  },
  setShowRemaining(on: boolean): void {
    patch({ showRemaining: on });
  },
  /** Arms the alarm for an epoch-ms instant, or clears it with null. */
  setAlarm(at: number | null): void {
    patch({ alarmAt: at });
  },
};

/**
 * Wall-clock time for the pill.
 *
 * Hand-rolled rather than toLocaleTimeString: the 12/24 choice here is an
 * explicit setting, and the locale formatter would also decide the separator,
 * the AM/PM casing, and whether a leading zero appears -- so the same setting
 * would render differently per browser locale. Minutes are always two digits;
 * the hour is padded only on the 24-hour clock, matching how a 12-hour clock
 * is written (1:25 PM, not 01:25 PM).
 */
export function formatClock(at: Date, hour24: boolean): string {
  const minutes = String(at.getMinutes()).padStart(2, "0");
  if (hour24) {
    return `${String(at.getHours()).padStart(2, "0")}:${minutes}`;
  }
  const h = at.getHours();
  // Midnight and noon are hour 0 and 12 on the 24-hour clock; both read as 12.
  const h12 = h % 12 === 0 ? 12 : h % 12;
  return `${h12}:${minutes} ${h < 12 ? "AM" : "PM"}`;
}

/**
 * Time left until the alarm, as "2h 5m" / "50m" / "1m".
 *
 * Rounded UP to the minute so a pill that says 1m never means "already due",
 * and so the countdown reads the way a person says it: with 50m 40s left the
 * answer is "51m" only for the first second, then 50m for the rest of that
 * minute. Anything at or below zero is "now" -- the caller clears a fired
 * alarm, so this is only the one frame between due and cleared.
 */
export function formatRemaining(ms: number): string {
  if (ms <= 0) return "now";
  const totalMinutes = Math.ceil(ms / 60000);
  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (hours === 0) return `${minutes}m`;
  if (minutes === 0) return `${hours}h`;
  return `${hours}h ${minutes}m`;
}

/**
 * The next instant a wall-clock "HH:MM" happens, as epoch ms.
 *
 * The alarm is set by time of day, so 06:00 typed at 23:40 means tomorrow
 * morning: a target that is not still ahead of `nowMs` rolls to the next day.
 * Day arithmetic goes through the Date constructor rather than adding 86.4e6,
 * so a DST boundary keeps the wall-clock time the user asked for. Returns null
 * for anything that is not a valid HH:MM, so a half-typed input arms nothing.
 */
export function alarmTargetFrom(nowMs: number, hhmm: string): number | null {
  const match = /^(\d{1,2}):(\d{2})$/.exec(hhmm.trim());
  if (match === null) return null;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (hours > 23 || minutes > 59) return null;
  const now = new Date(nowMs);
  const target = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
    hours,
    minutes,
    0,
    0,
  );
  if (target.getTime() <= nowMs) {
    target.setDate(target.getDate() + 1);
  }
  return target.getTime();
}

/** "HH:MM" for a time input, in the device's own timezone. */
export function hhmmFrom(at: Date): string {
  return `${String(at.getHours()).padStart(2, "0")}:${String(
    at.getMinutes(),
  ).padStart(2, "0")}`;
}

/**
 * A two-note chime when the alarm comes due.
 *
 * Synthesised, not a bundled audio file: the binary embeds its own assets and
 * an alarm is not worth a sound file in it. Wrapped in try/catch and
 * best-effort by design -- an autoplay-blocked or unsupported AudioContext
 * must not take the visual alert (toast plus the pill's flash) down with it.
 */
export function playAlarmChime(): void {
  try {
    const Ctor =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext })
        .webkitAudioContext;
    if (Ctor === undefined) return;
    const ctx = new Ctor();
    const gain = ctx.createGain();
    gain.connect(ctx.destination);
    gain.gain.setValueAtTime(0.0001, ctx.currentTime);
    // A short swell and decay per note: a square-edged gain step on a sine
    // clicks audibly on every device tested.
    gain.gain.exponentialRampToValueAtTime(0.18, ctx.currentTime + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + 0.9);
    const osc = ctx.createOscillator();
    osc.type = "sine";
    osc.frequency.setValueAtTime(880, ctx.currentTime);
    osc.frequency.setValueAtTime(1174, ctx.currentTime + 0.22);
    osc.connect(gain);
    osc.start();
    osc.stop(ctx.currentTime + 0.95);
    // Release the hardware once the tail has played; a context left open holds
    // an audio device for the rest of the session.
    osc.addEventListener("ended", () => void ctx.close().catch(() => {}), {
      once: true,
    });
  } catch {
    // See the doc comment: silence is an acceptable degradation here.
  }
}
