// The profile-name rules, in one place on the client.
//
// The server is the authority (validateProfileName in internal/api/auth.go):
// a name becomes a directory name verbatim, so it has to survive a Windows
// file system as well as a URL. The client keeps a copy so the form can say
// which rule was broken instead of posting and reading back a 400 that names
// rules the input already satisfies. profileName.test.ts pins this copy
// against the Go source -- the enforcement the two hand-maintained copies in
// Login.tsx and ProfileDialog.tsx never had.

/** Longest accepted name: first character, up to 30 middle, last. */
export const PROFILE_NAME_MAX_LENGTH = 32;

export const PROFILE_NAME_PATTERN =
  /^[a-zA-Z0-9]([a-zA-Z0-9 _-]{0,30}[a-zA-Z0-9])?$/;

/** Path-hostile characters and parent-directory hops. */
export const PROFILE_NAME_ILLEGAL = /[/\\:*?"<>|]|\.\./;

// DOS device names, which Windows resolves in every directory: a profile
// called "nul" is reported as created and then fails every login, because the
// directory is really a device. Rejected on all platforms, because a library
// folder is meant to move between machines.
export const PROFILE_NAME_RESERVED: ReadonlySet<string> = new Set([
  "con",
  "prn",
  "aux",
  "nul",
  "com1",
  "com2",
  "com3",
  "com4",
  "com5",
  "com6",
  "com7",
  "com8",
  "com9",
  "lpt1",
  "lpt2",
  "lpt3",
  "lpt4",
  "lpt5",
  "lpt6",
  "lpt7",
  "lpt8",
  "lpt9",
]);

export const PROFILE_NAME_RULE =
  "Use 1\u201332 characters: letters, digits, spaces, dashes, or underscores, starting and ending with a letter or digit.";

export function isValidProfileName(name: string): boolean {
  return !PROFILE_NAME_ILLEGAL.test(name) && PROFILE_NAME_PATTERN.test(name);
}

/** null when the name is acceptable, otherwise the message to show. */
export function profileNameProblem(raw: string): string | null {
  const name = raw.trim();
  if (name === "") return "Enter a profile name.";
  if (!isValidProfileName(name)) return PROFILE_NAME_RULE;
  if (PROFILE_NAME_RESERVED.has(name.toLowerCase())) {
    return `${name} is a name Windows reserves for a device. Pick another.`;
  }
  return null;
}
