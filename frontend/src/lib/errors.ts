import { ApiError } from "~/api/client";

/**
 * The one policy for turning a caught error into text a user may see.
 *
 * Narrowed to ApiError deliberately: only messages the server authored are
 * fit to display. A bare Error reaching a call site is a frontend bug, and
 * showing its message would put an internal exception string ("Cannot read
 * properties of undefined") in front of the user in place of the caller's
 * fallback. Every call site passes a serviceable fallback, so narrowing drops
 * no server-authored message -- only ones that were never fit to show.
 * ShareDialog.test.ts and library.test.ts pin the non-ApiError path to the
 * fallback.
 *
 * The empty-message guard is defensive rather than reachable:
 * parseErrorResponse substitutes a fallback for a blank body and every other
 * ApiError is built from a string literal, so .message is never "" today.
 */
export function getErrorMessage(error: unknown, fallback: string): string {
  return error instanceof ApiError && error.message ? error.message : fallback;
}
