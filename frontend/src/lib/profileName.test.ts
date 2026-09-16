// The client's profile-name rules are a copy of the server's, and a copy with
// nothing pinning it drifts. This suite reads internal/api/auth.go and asserts
// the client agrees with it: the regex, the path-hostile characters, the DOS
// device names and the length cap. A rule changed on one side of the language
// boundary now fails here instead of in a 400 the form cannot explain.
//
// cwd is the frontend root under vitest, so a plain relative read works both
// in-sandbox and on the dev box (indexHtml.test.ts relies on the same).
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  isValidProfileName,
  PROFILE_NAME_MAX_LENGTH,
  PROFILE_NAME_PATTERN,
  PROFILE_NAME_RESERVED,
  profileNameProblem,
} from "~/lib/profileName";

const goSource = readFileSync("../internal/api/auth.go", "utf8");

function goPatternSource(): string {
  const match = /var validProfileName = regexp\.MustCompile\(\s*`([^`]+)`/.exec(
    goSource,
  );
  if (!match) throw new Error("validProfileName regex missing from auth.go");
  // Go escapes the hyphen inside the class; JavaScript does not need to.
  return match[1].replaceAll("\\-", "-");
}

// Returned as a string and iterated directly: decomposing it would be a
// spread over a string, which the lint rules refuse.
function goIllegalChars(): string {
  const match = /strings\.ContainsAny\(name, `([^`]+)`\)/.exec(goSource);
  if (!match) throw new Error("illegal-character set missing from auth.go");
  return match[1];
}

function goReservedNames(): string[] {
  const block =
    /var windowsReservedNames = map\[string\]bool\{([\s\S]*?)\n\}/.exec(
      goSource,
    );
  if (!block) throw new Error("windowsReservedNames missing from auth.go");
  return [...block[1].matchAll(/"([^"]+)":\s*true/g)].map((m) => m[1]);
}

describe("profile-name rules mirror internal/api/auth.go", () => {
  it("uses the server's regex verbatim", () => {
    expect(PROFILE_NAME_PATTERN.source).toBe(goPatternSource());
  });

  it("rejects every character the server calls path-hostile", () => {
    const illegal = goIllegalChars();
    expect(illegal.length).toBeGreaterThan(0);
    for (const ch of illegal) {
      expect(isValidProfileName(`a${ch}b`)).toBe(false);
    }
    expect(isValidProfileName("a..b")).toBe(false);
  });

  it("carries the same DOS device names", () => {
    const reserved = goReservedNames();
    expect(reserved.length).toBeGreaterThan(0);
    expect([...PROFILE_NAME_RESERVED].toSorted()).toEqual(reserved.toSorted());
    for (const name of reserved) {
      expect(profileNameProblem(name)).toContain("Windows reserves");
    }
    // A name that merely contains one is still fine, as it is server-side.
    expect(profileNameProblem("Conrad")).toBeNull();
  });

  it("caps the length where the server's quantifier does", () => {
    const quantifier = /\{0,(\d+)\}/.exec(goPatternSource());
    if (!quantifier)
      throw new Error("no length quantifier in the server regex");
    // First character + middle run + last character.
    expect(PROFILE_NAME_MAX_LENGTH).toBe(Number(quantifier[1]) + 2);
    expect(isValidProfileName("A".repeat(PROFILE_NAME_MAX_LENGTH))).toBe(true);
    expect(isValidProfileName("A".repeat(PROFILE_NAME_MAX_LENGTH + 1))).toBe(
      false,
    );
  });

  it("names the broken rule instead of reporting a bare failure", () => {
    expect(profileNameProblem("")).toContain("Enter a profile name");
    expect(profileNameProblem("   ")).toContain("Enter a profile name");
    expect(profileNameProblem("_bob")).toContain("starting and ending");
    expect(profileNameProblem("bob-")).toContain("starting and ending");
    // Trimmed before validation, exactly as the handlers do.
    expect(profileNameProblem("  Ada Lovelace  ")).toBeNull();
  });
});
