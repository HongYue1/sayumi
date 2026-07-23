import {
  reportReachable,
  reportUnreachable,
  isReachable,
} from "~/lib/reachability";
import { reportUnauthenticated } from "~/lib/sessionGate";

const BASE = "/api";

type HttpMethod =
  "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD" | "OPTIONS";

interface ApiErrorBody {
  error?: unknown;
  code?: unknown;
}

export class ApiError extends Error {
  readonly status?: number;
  readonly code?: string;

  constructor(
    message: string,
    status?: number,
    code?: string,
    cause?: unknown,
  ) {
    super(message, cause !== undefined ? { cause } : undefined);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

function buildHeaders(json = false): HeadersInit {
  const headers: Record<string, string> = {};
  if (json) headers["Content-Type"] = "application/json";
  return headers;
}

function isApiErrorBody(value: unknown): value is ApiErrorBody {
  return typeof value === "object" && value !== null;
}

function abortError(signal?: AbortSignal): Error {
  if (signal?.reason instanceof Error) return signal.reason;
  return new DOMException("The operation was aborted.", "AbortError");
}

function networkErrorMessage(error: unknown): string {
  if (error instanceof DOMException) {
    if (error.name === "TimeoutError") return "Request timed out";
    if (error.name === "AbortError")
      return error.message || "Request cancelled";
  }
  if (typeof navigator !== "undefined" && !navigator.onLine) {
    return "You appear to be offline";
  }
  return "Can't reach the server";
}

function pathSegment(value: string): string {
  return encodeURIComponent(value);
}

async function parseErrorResponse(
  res: Response,
  fallback: string,
): Promise<{ message: string; code?: string }> {
  try {
    const body: unknown = await res.json();
    if (isApiErrorBody(body)) {
      const message =
        typeof body.error === "string" && body.error.trim()
          ? body.error
          : fallback;
      const code =
        typeof body.code === "string" && body.code ? body.code : undefined;
      return { message, code };
    }
  } catch {
    // Non-JSON or malformed error body.
  }
  return { message: fallback };
}

async function parseSuccessResponse<T>(res: Response): Promise<T> {
  if (
    res.status === 204 ||
    res.status === 205 ||
    res.headers.get("Content-Length") === "0"
  ) {
    return undefined as T;
  }

  const contentType = (res.headers.get("Content-Type") ?? "").toLowerCase();

  // Non-JSON responses: tolerate a blank body as "no content", else it's an error.
  if (!contentType.includes("application/json")) {
    const rawBody = await res.text();
    if (!rawBody.trim()) return undefined as T;
    throw new ApiError(
      "Invalid server response",
      res.status,
      "invalid_response",
    );
  }

  // Parse the JSON body in a single native pass (res.json()) instead of first
  // materializing the full response text, which matters for large chapter
  // payloads. The backend returns 204 (handled above) for no-content rather than
  // an empty 200, so a parse failure here is a genuinely malformed body.
  try {
    return (await res.json()) as T;
  } catch (error) {
    throw new ApiError(
      "Invalid server response",
      res.status,
      "invalid_response",
      error,
    );
  }
}

// Per-attempt network timeout. Without it a connection that is accepted but
// never answered would hang forever and never reach the retry path.
const DEFAULT_TIMEOUT_MS = 20_000;

// Combines an optional caller signal with a timeout. AbortSignal.timeout aborts
// with a TimeoutError (not an AbortError), so request()'s catch routes it to the
// retryable network-error branch instead of treating it as a user cancellation.
function withTimeout(
  signal: AbortSignal | undefined,
  timeoutMs: number | undefined,
): AbortSignal | undefined {
  if (!timeoutMs || timeoutMs <= 0) return signal;
  const timeout = AbortSignal.timeout(timeoutMs);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

async function request<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  timeoutMs?: number,
): Promise<T> {
  const options: RequestInit = {
    method,
    headers: buildHeaders(),
    credentials: "same-origin",
    signal: withTimeout(signal, timeoutMs),
  };

  if (body != null) {
    if (body instanceof FormData) {
      options.body = body;
    } else {
      options.headers = buildHeaders(true);
      options.body = JSON.stringify(body);
    }
  }

  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, options);
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError")
      throw error;
    if (signal?.aborted) throw abortError(signal);
    // A rejected fetch to our own origin means the local server is unreachable
    // (e.g. it was quit). Flip the shared reachability signal so the offline
    // banner appears immediately, without waiting on navigator.onLine — which
    // stays true for a downed localhost server.
    reportUnreachable();
    throw new ApiError(
      networkErrorMessage(error),
      undefined,
      "network_error",
      error,
    );
  }

  // The server answered (even a 4xx/5xx proves it's reachable).
  reportReachable();

  if (!res.ok) {
    const fallback =
      res.status >= 500
        ? "Server error. Please try again."
        : `Request failed: ${res.status}`;
    const parsed = await parseErrorResponse(res, fallback);
    // A 401 "unauthenticated" on a request we made while believing we were
    // logged in means the server-side session is gone (commonly: the server
    // restarted and a non-remembered session wasn't restored, or the session
    // expired). Notify the session store so the app falls back to the login
    // screen instead of looking broken. Credential failures use other codes
    // ("invalid_credentials") and /auth/status returns 200, so neither trips
    // this.
    if (res.status === 401 && parsed.code === "unauthenticated") {
      reportUnauthenticated();
    }
    throw new ApiError(parsed.message, res.status, parsed.code);
  }

  return parseSuccessResponse<T>(res);
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) {
    return Promise.reject(abortError(signal));
  }

  const { promise, resolve, reject } = Promise.withResolvers<void>();

  const timer = globalThis.setTimeout(() => {
    signal?.removeEventListener("abort", onAbort);
    resolve();
  }, ms);

  function onAbort(): void {
    clearTimeout(timer);
    reject(abortError(signal));
  }

  signal?.addEventListener("abort", onAbort, { once: true });

  return promise;
}

export interface RequestWithRetryOptions {
  attempts?: number;
  signal?: AbortSignal;
  /** Per-attempt timeout in ms. Defaults to DEFAULT_TIMEOUT_MS; pass 0 to disable. */
  timeoutMs?: number;
}

export function requestWithRetry<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  attempts?: number,
  signal?: AbortSignal,
): Promise<T>;
export function requestWithRetry<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  options?: RequestWithRetryOptions,
): Promise<T>;
export async function requestWithRetry<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  attemptsOrOptions: number | RequestWithRetryOptions = 3,
  signal?: AbortSignal,
): Promise<T> {
  let maxAttempts: number;
  let sig: AbortSignal | undefined;
  let timeoutMs: number | undefined;

  if (typeof attemptsOrOptions === "object" && attemptsOrOptions !== null) {
    const raw = attemptsOrOptions.attempts ?? 3;
    maxAttempts = Number.isFinite(raw) ? Math.max(1, Math.trunc(raw)) : 3;
    sig = attemptsOrOptions.signal;
    timeoutMs = attemptsOrOptions.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  } else {
    maxAttempts = Number.isFinite(attemptsOrOptions)
      ? Math.max(1, Math.trunc(attemptsOrOptions))
      : 3;
    sig = signal;
    timeoutMs = DEFAULT_TIMEOUT_MS;
  }

  let lastError: unknown;

  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    // Snapshot reachability BEFORE the attempt. request() flips the shared
    // signal to unreachable on a network failure, so reading isReachable()
    // after the catch would always be false for the very network error we want
    // to retry — which silently killed idempotent network-error retries. Taken
    // beforehand, a server we already knew was down still short-circuits the
    // retry storm on later requests.
    const reachableBeforeAttempt = isReachable();
    try {
      return await request<T>(method, path, body, sig, timeoutMs);
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError")
        throw error;
      if (sig?.aborted) throw abortError(sig);

      // Only retry idempotent methods. Retrying a POST/PATCH that timed out
      // after the server already committed would duplicate the write.
      const idempotent =
        method === "GET" ||
        method === "HEAD" ||
        method === "PUT" ||
        method === "DELETE" ||
        method === "OPTIONS";
      // Once we already knew the server was unreachable (before this attempt),
      // stop the per-request retry storm (3 attempts x exponential backoff x
      // 20s timeout on every navigation). The reachability poll drives recovery
      // instead. A network error during THIS attempt still retries, because the
      // snapshot predates request()'s reportUnreachable().
      const isRetryable =
        idempotent &&
        reachableBeforeAttempt &&
        (!(error instanceof ApiError) ||
          error.status === undefined ||
          error.status >= 500);

      if (!isRetryable || attempt === maxAttempts - 1) {
        throw error;
      }

      lastError = error;
      await sleep(500 * 2 ** attempt, sig);
    }
  }

  throw lastError;
}

export interface AuthStatus {
  authenticated: boolean;
  profile: string;
}

export interface ProfileInfo {
  name: string;
  hasPin: boolean;
}

export function getAuthStatus(signal?: AbortSignal): Promise<AuthStatus> {
  return request<AuthStatus>("GET", "/auth/status", undefined, signal);
}

export function listProfiles(signal?: AbortSignal): Promise<ProfileInfo[]> {
  return request<ProfileInfo[]>("GET", "/auth/profiles", undefined, signal);
}

export function login(
  name: string,
  pin: string,
  remember: boolean,
  signal?: AbortSignal,
): Promise<{ profile: string }> {
  return request("POST", "/auth/login", { name, pin, remember }, signal);
}

export function logout(signal?: AbortSignal): Promise<void> {
  return request("POST", "/auth/logout", undefined, signal);
}

export function createProfile(
  name: string,
  pin: string,
  signal?: AbortSignal,
): Promise<{ name: string }> {
  return request("POST", "/auth/create", { name, pin }, signal);
}

export function cloneProfile(
  newName: string,
  pin: string,
  signal?: AbortSignal,
): Promise<{ name: string }> {
  return request("POST", "/auth/clone", { newName, pin }, signal);
}

export function deleteProfile(
  pin: string,
  signal?: AbortSignal,
): Promise<void> {
  return request("DELETE", "/auth/profile", { pin }, signal);
}

export interface BookMeta {
  id: string;
  title: string;
  author: string;
  language: string;
  publisher: string;
  description: string;
  pubDate: string;
  hasCover: boolean;
  direction: string;
  chapterCount: number;
  progress: number;
  flairId?: string;
  addedAt?: string;
  lastReadAt?: string;
  // Server's updated_at; appended to the cover URL as ?v=<updatedAt> so an
  // edited cover (same path) busts the immutable browser cache.
  updatedAt?: string;
}

export interface FlairDef {
  id: string;
  label: string;
  color: string;
}

export function getFlairs(signal?: AbortSignal): Promise<FlairDef[]> {
  return request<FlairDef[]>("GET", "/flairs", undefined, signal);
}

export function createFlair(
  data: { label: string; color: string },
  signal?: AbortSignal,
): Promise<FlairDef> {
  return request<FlairDef>("POST", "/flairs", data, signal);
}

export function deleteFlair(id: string, signal?: AbortSignal): Promise<void> {
  return request<void>(
    "DELETE",
    `/flairs/${pathSegment(id)}`,
    undefined,
    signal,
  );
}

/** Assigns a flair to a book, or clears it when flairId is null. */
export function setBookFlair(
  bookId: string,
  flairId: string | null,
  signal?: AbortSignal,
): Promise<void> {
  return request<void>(
    "PUT",
    `/books/${pathSegment(bookId)}/flair`,
    { flairId },
    signal,
  );
}

export interface BookDetail extends BookMeta {
  spine: SpineEntry[];
  toc: TocEntry[];
}

export interface SpineEntry {
  href: string;
  id: string;
  mediaType: string;
  linear: boolean;
}

export interface TocEntry {
  title: string;
  href: string;
  depth: number;
  children?: TocEntry[];
}

export interface UserSettings {
  fontSize: number;
  fontFamily: string;
  lineHeight: number | null;
  paragraphSpacing: number | null;
  textIndent: number | null;
  /** Extra letter-spacing (em) for body text; null leaves the browser default. */
  letterSpacing: number | null;
  contentWidth: number | null;
  displayMode: "scroll" | "paged" | "paged-two";
  marginTop: number | null;
  marginBottom: number | null;
  marginSide: number | null;
  preserveStyles: boolean;
  preserveFonts: boolean;
  justify: boolean;
  hyphenation: boolean;
  theme: string;
  chapterTitleAlign: "left" | "center" | "right" | null;
  chapterTitleSize: number | null;
  chapterTitleSpacing: number | null;
  /** Font family id for chapter titles (headings); null inherits the body font. */
  chapterTitleFontFamily: string | null;
  /** Extra letter-spacing (em) for headings; null leaves the browser default. */
  headingLetterSpacing: number | null;
  /** When true, per-heading sizes (h1Size..h6Size) apply and chapterTitleSize is ignored. */
  headerSizesEnabled: boolean;
  h1Size: number | null;
  h2Size: number | null;
  h3Size: number | null;
  h4Size: number | null;
  h5Size: number | null;
  h6Size: number | null;
  /** CSS font-weight (100-900) for all headings; null leaves them untouched. */
  headerWeight: number | null;
  /** CSS font-weight (100-900) for body text; null leaves it untouched. */
  textWeight: number | null;
  /**
   * Per-family override of which file fills each role, keyed by font family id.
   * Only meaningful for user (./Fonts/) families; embedded fonts ignore it.
   */
  fontRoles?: Record<string, FontRoleMap>;
}

export interface FontRoleMap {
  regular?: string;
  italic?: string;
  bold?: string;
  boldItalic?: string;
}

/** A user-supplied font family discovered under ./Fonts/<dir>/. */
export interface UserFontFamily {
  id: string; // "user:<dir>"
  label: string;
  category: "serif" | "sans-serif";
  files: string[];
  /**
   * A variable family: one upright file covers regular+bold and one italic
   * file covers italic+boldItalic via a weight range (no synthesized bold).
   */
  variable: boolean;
  detected: FontRoleMap & {
    regular: string;
    italic: string;
    bold: string;
    boldItalic: string;
  };
}

interface FontsResponse {
  user: UserFontFamily[];
  userToken: string;
}

let userFontToken = "";

function acceptFontsResponse(response: FontsResponse): UserFontFamily[] {
  userFontToken = response.userToken;
  return response.user ?? [];
}

export function getFonts(signal?: AbortSignal): Promise<UserFontFamily[]> {
  return request<FontsResponse>("GET", "/fonts", undefined, signal).then(
    acceptFontsResponse,
  );
}

export function rescanFonts(signal?: AbortSignal): Promise<UserFontFamily[]> {
  return request<FontsResponse>(
    "POST",
    "/fonts/rescan",
    undefined,
    signal,
  ).then(acceptFontsResponse);
}

/** Absolute URL for a user font file (used in @font-face src across the iframe boundary). */
export function userFontUrl(dir: string, file: string): string {
  return `${window.location.origin}/fonts/user/${encodeURIComponent(
    dir,
  )}/${encodeURIComponent(file)}?token=${encodeURIComponent(userFontToken)}`;
}

export interface ProgressData {
  chapter: number;
  percent: number;
  cfi?: string;
}

export function getBooks(signal?: AbortSignal): Promise<BookMeta[]> {
  return request<BookMeta[]>("GET", "/books", undefined, signal);
}

/** Re-scans the on-disk library folder for newly added EPUBs. Returns the count imported. */
export function rescanLibrary(
  signal?: AbortSignal,
): Promise<{ imported: number }> {
  return request<{ imported: number }>(
    "POST",
    "/library/rescan",
    undefined,
    signal,
  );
}

export function getBook(id: string, signal?: AbortSignal): Promise<BookDetail> {
  return request<BookDetail>(
    "GET",
    `/books/${pathSegment(id)}`,
    undefined,
    signal,
  );
}

export function deleteBook(id: string, signal?: AbortSignal): Promise<void> {
  return request<void>(
    "DELETE",
    `/books/${pathSegment(id)}`,
    undefined,
    signal,
  );
}

export function getToc(id: string, signal?: AbortSignal): Promise<TocEntry[]> {
  return request<TocEntry[]>(
    "GET",
    `/books/${pathSegment(id)}/toc`,
    undefined,
    signal,
  );
}

export function uploadBook(
  file: File,
  signal?: AbortSignal,
): Promise<BookMeta> {
  const form = new FormData();
  form.append("epub", file);
  return request<BookMeta>("POST", "/books/upload", form, signal);
}

// version (the book's updatedAt) is appended as ?v= so that editing a cover —
// which reuses the same /cover path — produces a new URL and bypasses the
// immutable browser cache; without it the stale cover would persist for a year.
export function getCoverUrl(id: string, version?: string): string {
  const base = `${BASE}/books/${pathSegment(id)}/cover`;
  return version ? `${base}?v=${encodeURIComponent(version)}` : base;
}

// getDownloadUrl points at the endpoint that streams the original .epub with a
// Content-Disposition: attachment header. A plain same-origin <a download>
// pointed here triggers a browser download and carries the session cookie, so
// no fetch/JS plumbing is needed.
export function getDownloadUrl(id: string): string {
  return `${BASE}/books/${pathSegment(id)}/file`;
}

// updateBookMeta edits a book's title/author. Only provided fields change
// (patch semantics); the server returns the refreshed BookMeta.
export function updateBookMeta(
  id: string,
  patch: { title?: string; author?: string },
  signal?: AbortSignal,
): Promise<BookMeta> {
  return request<BookMeta>("PATCH", `/books/${pathSegment(id)}`, patch, signal);
}

// uploadCover replaces a book's cover with an image file (JPEG/PNG/WebP); the
// server normalizes it to the same resized JPEG the importer produces and
// returns the refreshed BookMeta (with a bumped updatedAt for cache-busting).
export function uploadCover(
  id: string,
  file: File,
  signal?: AbortSignal,
): Promise<BookMeta> {
  const form = new FormData();
  form.append("cover", file);
  return request<BookMeta>(
    "PUT",
    `/books/${pathSegment(id)}/cover`,
    form,
    signal,
  );
}

// uploadToGofile uploads the book's .epub to gofile.io (anonymous) and returns
// the public download page URL. This is the app's only outbound network call.
export function uploadToGofile(
  id: string,
  signal?: AbortSignal,
): Promise<{ downloadPage: string }> {
  return request<{ downloadPage: string }>(
    "POST",
    `/books/${pathSegment(id)}/gofile`,
    undefined,
    signal,
    // gofile picks a server then streams the upload; allow well beyond the
    // default request timeout for a large book over a slow link.
    30 * 60 * 1000,
  );
}

export interface ChapterData {
  chapterIndex: number;
  html: string;
  css: string;
  fontFaceCSS: string;
  direction: string;
  writingMode: string;
  resourceBase?: string;
}

export function fetchChapter(
  bookId: string,
  index: number,
  attempts = 3,
  signal?: AbortSignal,
): Promise<ChapterData> {
  return requestWithRetry<ChapterData>(
    "GET",
    `/books/${pathSegment(bookId)}/chapters/${index}`,
    undefined,
    attempts,
    signal,
  );
}

export function getSettings(signal?: AbortSignal): Promise<UserSettings> {
  return request<UserSettings>("GET", "/settings", undefined, signal);
}

export function saveSettings(
  settings: UserSettings,
  signal?: AbortSignal,
): Promise<UserSettings> {
  return request<UserSettings>("PUT", "/settings", settings, signal);
}

/** A saved snapshot of the whole settings object (theme + fonts included). */
export interface SettingsPreset {
  id: string;
  name: string;
  settings: UserSettings;
  createdAt: string;
  updatedAt: string;
}

export function getPresets(signal?: AbortSignal): Promise<SettingsPreset[]> {
  return request<SettingsPreset[]>("GET", "/presets", undefined, signal);
}

export function createPreset(
  data: { name: string; settings: UserSettings },
  signal?: AbortSignal,
): Promise<SettingsPreset> {
  return request<SettingsPreset>("POST", "/presets", data, signal);
}

export function deletePreset(id: string, signal?: AbortSignal): Promise<void> {
  return request<void>(
    "DELETE",
    `/presets/${pathSegment(id)}`,
    undefined,
    signal,
  );
}

/** A user-created theme saved on the server. An empty accent means "auto":
 *  the client derives it from bg/fg (see lib/themes autoAccent). */
export interface CustomTheme {
  id: string;
  name: string;
  group: "light" | "dark";
  bg: string;
  fg: string;
  accent: string;
  createdAt: string;
  updatedAt: string;
}

/** Fields sent when creating or updating a custom theme. */
export interface CustomThemeInput {
  name: string;
  group: "light" | "dark";
  bg: string;
  fg: string;
  accent: string;
}

export function getCustomThemes(signal?: AbortSignal): Promise<CustomTheme[]> {
  return request<CustomTheme[]>("GET", "/themes", undefined, signal);
}

export function createCustomTheme(
  data: CustomThemeInput,
  signal?: AbortSignal,
): Promise<CustomTheme> {
  return request<CustomTheme>("POST", "/themes", data, signal);
}

export function updateCustomTheme(
  id: string,
  data: CustomThemeInput,
  signal?: AbortSignal,
): Promise<CustomTheme> {
  return request<CustomTheme>(
    "PUT",
    `/themes/${pathSegment(id)}`,
    data,
    signal,
  );
}

export function deleteCustomTheme(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  return request<void>(
    "DELETE",
    `/themes/${pathSegment(id)}`,
    undefined,
    signal,
  );
}

export function getProgress(
  bookId: string,
  signal?: AbortSignal,
): Promise<ProgressData> {
  return request<ProgressData>(
    "GET",
    `/books/${pathSegment(bookId)}/progress`,
    undefined,
    signal,
  );
}

export function saveProgress(
  bookId: string,
  data: ProgressData,
  signal?: AbortSignal,
): Promise<ProgressData> {
  return request<ProgressData>(
    "PUT",
    `/books/${pathSegment(bookId)}/progress`,
    data,
    signal,
  );
}

export function beaconProgress(bookId: string, data: ProgressData): void {
  void fetch(`${BASE}/books/${pathSegment(bookId)}/progress/beacon`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
    keepalive: true,
    credentials: "same-origin",
  }).catch(() => {
    // Best-effort keepalive on page hide.
  });
}

export interface SearchResult {
  chapterIndex: number;
  charOffset: number;
  matchLen: number;
  snippet: string;
  snippetStart: number;
  snippetLen: number;
}

export interface SearchResponse {
  results: SearchResult[];
  total?: number | null;
  hasMore: boolean;
  nextCursor?: string;
}

export function searchBook(
  bookId: string,
  query: string,
  cursor?: string,
  limit?: number,
  signal?: AbortSignal,
): Promise<SearchResponse> {
  const params = new URLSearchParams({ q: query });
  if (cursor) params.set("cursor", cursor);
  if (limit) params.set("limit", String(limit));
  return request<SearchResponse>(
    "GET",
    `/books/${pathSegment(bookId)}/search?${params}`,
    undefined,
    signal,
  );
}

export interface Bookmark {
  id: string;
  chapter: number;
  percent: number;
  cfi?: string;
  label: string;
  comment: string;
  createdAt: string;
}

export function getBookmarks(
  bookId: string,
  signal?: AbortSignal,
): Promise<Bookmark[]> {
  return request<Bookmark[]>(
    "GET",
    `/books/${pathSegment(bookId)}/bookmarks`,
    undefined,
    signal,
  );
}

export function createBookmark(
  bookId: string,
  data: {
    chapter: number;
    percent: number;
    cfi?: string;
    label?: string;
    comment?: string;
  },
  signal?: AbortSignal,
): Promise<Bookmark> {
  return request<Bookmark>(
    "POST",
    `/books/${pathSegment(bookId)}/bookmarks`,
    data,
    signal,
  );
}

export function updateBookmark(
  bookId: string,
  bookmarkId: string,
  data: { label: string; comment: string },
  signal?: AbortSignal,
): Promise<Bookmark> {
  return request<Bookmark>(
    "PATCH",
    `/books/${pathSegment(bookId)}/bookmarks/${pathSegment(bookmarkId)}`,
    data,
    signal,
  );
}

export function deleteBookmark(
  bookId: string,
  bookmarkId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request<void>(
    "DELETE",
    `/books/${pathSegment(bookId)}/bookmarks/${pathSegment(bookmarkId)}`,
    undefined,
    signal,
  );
}

export async function checkHealth(): Promise<boolean> {
  try {
    const res = await fetch(`${BASE}/health`, {
      method: "GET",
      credentials: "same-origin",
      signal: AbortSignal.timeout(5000),
    });
    if (res.ok) reportReachable();
    else reportUnreachable();
    return res.ok;
  } catch {
    reportUnreachable();
    return false;
  }
}
