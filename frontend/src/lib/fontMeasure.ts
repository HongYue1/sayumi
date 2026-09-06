// Calibrate rendered ink, not optional (sometimes incorrect) OS/2 x-height.
// A loaded face can silently borrow a missing 'x' from another font; different
// fallback stacks and several matching letters make that substitution visible.
import {
  userFontUrl,
  type FontRoleMap,
  type UserFontFamily,
} from "~/api/client";
import { userFamilyDir } from "~/lib/fontRegistry";
import {
  isSafeFontAdjust,
  regularFontFile,
  REFERENCE_FACE,
} from "~/lib/readerFontFaces";

// Fixed regular-face calibration preserves the designer's relative sizing of
// bold/italic and avoids reloading on every weight/size slider tick. This is a
// family-level Latin-text match, not equivalence across scripts or optical axes.
const MEASURE_PX = 100;
const LOAD_TIMEOUT_MS = 5000;
const LOWER = ["x", "z", "v", "w"] as const;
const CAPS = ["H", "I", "E", "F"] as const;
const FALLBACKS = ["serif", "monospace", "sans-serif"] as const;
type Ink = Partial<Record<string, number>>;
let probeSerial = 0;
let referencePromise: Promise<Ink | null> | null = null;
let referenceDocument: Document | null = null;

// Fresh registry objects invalidate rescans/reconnects without retaining old
// registries. URL keys distinguish role picks and server tokens; cache only
// successful calibrations and bound each family's number of retained files.
const measured = new WeakMap<UserFontFamily, Map<string, Ink>>();
const MAX_CACHED_FILES = 8;

function waitFor<T>(
  pending: Promise<T>,
  signal?: AbortSignal,
  timeout?: number,
): Promise<T | null> {
  if (signal?.aborted) {
    // Starting a native load can synchronously notify an aborting caller.
    // Its returned promise still needs a rejection observer.
    void pending.catch(() => {});
    return Promise.resolve(null);
  }
  return new Promise((resolve) => {
    let settled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (value: T | null) => {
      if (settled) return;
      settled = true;
      if (timer !== undefined) clearTimeout(timer);
      signal?.removeEventListener("abort", cancel);
      resolve(value);
    };
    const cancel = () => finish(null);
    signal?.addEventListener("abort", cancel, { once: true });
    if (timeout !== undefined) timer = setTimeout(cancel, timeout);
    // Observe late rejections too. FontFace.load has no abort API; a timed-out
    // face is never registered, even if its native load subsequently finishes.
    void pending.then(finish, cancel);
  });
}

function rasterAscent(ctx: CanvasRenderingContext2D, glyph: string): number {
  const side = MEASURE_PX * 3;
  const baseline = MEASURE_PX * 2;
  ctx.clearRect(0, 0, side, side);
  ctx.fillStyle = "#000";
  ctx.fillText(glyph, MEASURE_PX / 2, baseline);
  const pixels = ctx.getImageData(0, 0, side, side).data;
  let top = side;
  for (let y = 0; y < side; y++) {
    for (let x = 0; x < side; x++) {
      if (pixels[(y * side + x) * 4 + 3] === 0) continue;
      // Clipping or an opaque privacy substitute is not a usable ink bound.
      if (x === 0 || y === 0 || x === side - 1 || y === side - 1)
        return Number.NaN;
      if (y < top) top = y;
    }
  }
  return top === side ? Number.NaN : baseline - top;
}

function glyphAscent(
  ctx: CanvasRenderingContext2D,
  family: string,
  glyph: string,
): number | undefined {
  const font = (fallback: string, probe = false) =>
    `400 ${MEASURE_PX}px ${probe ? `"${family}", ` : ""}${fallback}`;
  // Distinct control advances reveal fallback substitution: the actual face
  // must keep the SAME advance in both stacks. Coincident generic controls
  // are inconclusive, so try a third generic rather than assume coverage.
  const controls = FALLBACKS.map((fallback) => {
    ctx.font = font(fallback);
    return ctx.measureText(glyph).width;
  });
  const second = controls.findIndex(
    (width, i) =>
      i > 0 && Number.isFinite(width) && Math.abs(width - controls[0]) > 0.01,
  );
  if (!Number.isFinite(controls[0]) || second < 0) return undefined;
  ctx.font = font(FALLBACKS[0], true);
  const first = ctx.measureText(glyph);
  ctx.font = font(FALLBACKS[second], true);
  const other = ctx.measureText(glyph);
  if (
    !Number.isFinite(first.width) ||
    first.width <= 0 ||
    !Number.isFinite(other.width) ||
    Math.abs(first.width - other.width) > 0.01
  )
    return undefined;

  let ascent = first.actualBoundingBoxAscent;
  if (
    !Number.isFinite(ascent) ||
    ascent <= 0 ||
    !Number.isFinite(other.actualBoundingBoxAscent) ||
    other.actualBoundingBoxAscent <= 0
  ) {
    // Extended TextMetrics may be unavailable while rasterization still works.
    // Fixed dimensions bound allocation/work; readback denial is caught below.
    ascent = rasterAscent(ctx, glyph);
  } else if (Math.abs(ascent - other.actualBoundingBoxAscent) > 0.01) {
    return undefined;
  }
  // Keep supported-but-unusable distinct from missing. Otherwise an extreme
  // lowercase face could be incorrectly "rescued" by its normal-looking caps.
  return Number.isFinite(ascent) &&
    ascent >= MEASURE_PX * 0.15 &&
    ascent <= MEASURE_PX * 1.2
    ? ascent
    : Number.NaN;
}

async function readInk(
  doc: Document,
  url: string,
  variable: boolean,
  signal?: AbortSignal,
): Promise<Ink | null> {
  const fonts = doc.fonts;
  try {
    const family = `__sayumi_measure_${probeSerial++}`;
    const face = new FontFace(family, `url("${url.replace(/"/g, "%22")}")`, {
      // Match the reading face's declared axis, selecting regular weight 400
      // rather than inadvertently measuring a variable font's default weight.
      weight: variable ? "100 900" : "400",
      style: "normal",
    });
    if (
      !(await waitFor(face.load(), signal, LOAD_TIMEOUT_MS)) ||
      signal?.aborted
    )
      return null;
    fonts.add(face);
    try {
      const canvas = doc.createElement("canvas");
      canvas.width = canvas.height = MEASURE_PX * 3;
      const ctx = canvas.getContext("2d");
      if (!ctx) return null;
      ctx.textBaseline = "alphabetic";
      ctx.textAlign = "left";
      const ink: Ink = {};
      for (const glyph of [...LOWER, ...CAPS]) {
        const height = glyphAscent(ctx, family, glyph);
        if (height !== undefined) ink[glyph] = height;
      }
      return Object.values(ink).some(Number.isFinite) ? ink : null;
    } finally {
      fonts.delete(face);
    }
  } catch {
    return null;
  }
}

function referenceInk(doc: Document): Promise<Ink | null> {
  if (!referencePromise || referenceDocument !== doc) {
    referenceDocument = doc;
    const pending = readInk(
      doc,
      `${window.location.origin}/fonts/${REFERENCE_FACE}`,
      true,
    );
    referencePromise = pending;
    void pending.then((ink) => {
      // Share in-flight work, but don't cache a transient offline/404/canvas
      // failure for the whole session. One caller's abort doesn't cancel peers.
      if (!ink && referencePromise === pending) referencePromise = null;
    });
  }
  return referencePromise;
}

function adjustment(reference: Ink, face: Ink): number | undefined {
  // Compare LIKE glyphs; never invent x-height with a cap-height multiplier.
  // Capitals are a fallback only when no lowercase probe is shared.
  const letters = LOWER.some((g) => g in reference && g in face) ? LOWER : CAPS;
  const ratios = letters
    .map((g) => (reference[g] ?? Number.NaN) / (face[g] ?? Number.NaN))
    .filter(isSafeFontAdjust)
    .sort((a, b) => a - b);
  if (ratios.length < 2) return undefined;
  const median = (values: number[]) =>
    (values[Math.floor((values.length - 1) / 2)] +
      values[Math.floor(values.length / 2)]) /
    2;
  const middle = median(ratios);
  const agreeing = ratios.filter(
    (ratio) => Math.abs(ratio / middle - 1) <= 0.1,
  );
  return agreeing.length >= 2 ? median(agreeing) : undefined;
}

/** Safe reference/face ink ratios; failures are absent and can be retried. */
export async function measureFamilyAdjusts(
  families: UserFontFamily[],
  roles: Record<string, FontRoleMap> | undefined,
  signal?: AbortSignal,
): Promise<Record<string, number>> {
  if (
    !families.length ||
    signal?.aborted ||
    typeof window === "undefined" ||
    typeof document === "undefined" ||
    typeof FontFace === "undefined" ||
    !document.fonts
  )
    return {};
  const doc = document;
  // Snapshot the file and URL token before awaiting the shared reference.
  // Callers may supply a live settings object or refresh the server session.
  const targets = families.flatMap((fam) => {
    const file = regularFontFile(fam, roles);
    return file
      ? [
          {
            fam,
            id: fam.id,
            url: userFontUrl(userFamilyDir(fam.id), file),
            variable: fam.variable,
          },
        ]
      : [];
  });
  if (!targets.length) return {};
  const reference = await waitFor(referenceInk(doc), signal);
  if (!reference || signal?.aborted) return {};
  const out: Record<string, number> = {};
  let next = 0;
  // Catalogue callers must not launch every native load/canvas probe at once.
  // The reader itself only asks for its active regular face.
  await Promise.all(
    Array.from({ length: Math.min(4, targets.length) }, async () => {
      while (next < targets.length) {
        if (signal?.aborted) break;
        const { fam, id, url, variable } = targets[next++];
        const key = `${variable}\n${url}`;
        const cache = measured.get(fam);
        const ink =
          cache?.get(key) ?? (await readInk(doc, url, variable, signal));
        if (!ink || signal?.aborted) continue;
        const ratio = adjustment(reference, ink);
        if (ratio === undefined) continue;
        const entries = cache ?? new Map<string, Ink>();
        entries.delete(key);
        entries.set(key, ink);
        if (entries.size > MAX_CACHED_FILES) {
          const oldest = entries.keys().next().value;
          if (oldest !== undefined) entries.delete(oldest);
        }
        measured.set(fam, entries);
        out[id] = ratio;
      }
    }),
  );
  return signal?.aborted ? {} : out;
}
