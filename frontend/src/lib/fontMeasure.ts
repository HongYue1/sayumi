// Ink-measured x-height ratios for user fonts.
//
// The server reads each face's x-height out of its OS/2 table, but that table
// can lie: Ovo and Rosarivo claim ~0.17 while inking ~0.46-0.51, and older
// faces predate the field entirely. The browser, handed the loaded bytes, can
// measure what actually renders instead: 'x' set at 100px, read back off a
// canvas. Each ratio overrides the server metrics wherever present (see
// normalizeToReference), so every family matches once measured and no table
// can blow text to 300% again.

import {
  userFontUrl,
  type FontRoleMap,
  type UserFontFamily,
} from "~/api/client";
import { userFamilyDir } from "~/lib/fontRegistry";
import { REFERENCE_FACE } from "~/lib/readerFontFaces";

// The probe renders at 100px so one canvas pixel is one percent of the em.
// Only ratios leave this module, so the value is arbitrary — just large
// enough that rounding cannot move the result.
const MEASURE_PX = 100;

let probeSerial = 0;

// Reference ink height, measured once: the file every ratio is taken against
// is embedded, so it cannot change under a session the way user fonts can.
let referencePromise: Promise<number | null> | null = null;

function referenceUrl(): string {
  return `${window.location.origin}/fonts/${REFERENCE_FACE}`;
}

// True x-height in px of the face at url, or null when it cannot be measured.
// A throwaway FontFace keeps the probe out of the page's own font list, and
// every failure — no FontFace or canvas in this runtime, a 404, an unloadable
// file — degrades to null so the caller falls back to the server metrics.
async function xHeightPx(url: string): Promise<number | null> {
  try {
    if (typeof FontFace === "undefined") return null;
    const probe = `__sayumi_measure_${probeSerial++}`;
    const face = new FontFace(probe, `url("${url.replace(/"/g, "%22")}")`);
    await face.load();
    if (typeof document === "undefined" || !document.fonts) return null;
    document.fonts.add(face);
    try {
      const canvas = document.createElement("canvas");
      const ctx = canvas.getContext("2d");
      if (!ctx) return null;
      ctx.font = `${MEASURE_PX}px "${probe}"`;
      const ascent = ctx.measureText("x").actualBoundingBoxAscent;
      if (!Number.isFinite(ascent) || ascent <= 0) return null;
      return ascent;
    } finally {
      document.fonts.delete(face);
    }
  } catch {
    return null;
  }
}

function referenceXHeight(): Promise<number | null> {
  if (!referencePromise) {
    referencePromise = xHeightPx(referenceUrl());
  }
  return referencePromise;
}

// The regular file actually rendered for this family: the user's role pick
// when set, else the backend's detection. Measuring any other file would
// normalize against a face the reader never shows.
function regularFile(
  fam: UserFontFamily,
  roles: Record<string, FontRoleMap> | undefined,
): string {
  return roles?.[fam.id]?.regular ?? fam.detected.regular;
}

/**
 * Ink-measured size ratios (reference x-height over face x-height) for the
 * given families, keyed by family id. Unmeasurable families are absent, and a
 * null reference yields no ratios at all: both leave the server metrics in
 * charge rather than sizing from a guess.
 */
export async function measureFamilyAdjusts(
  families: UserFontFamily[],
  roles: Record<string, FontRoleMap> | undefined,
): Promise<Record<string, number>> {
  const reference = await referenceXHeight();
  if (!reference) return {};
  const out: Record<string, number> = {};
  await Promise.all(
    families.map(async (fam) => {
      const file = regularFile(fam, roles);
      if (!file) return;
      const height = await xHeightPx(userFontUrl(userFamilyDir(fam.id), file));
      if (height) out[fam.id] = reference / height;
    }),
  );
  return out;
}
