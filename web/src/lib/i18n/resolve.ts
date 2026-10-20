// Language resolution (spec 019, FR-004). The first candidate that matches a
// listed language wins; English is the terminal fallback.

export interface ResolveInput {
  userChoice?: string | null;
  browserChoice?: string | null;
  navigatorLanguages?: readonly string[];
  instanceDefault?: string | null;
  listed: readonly string[];
}

// matchListed maps a BCP 47 tag onto a listed language: an exact
// (case-insensitive) match first, then the base language before "-"
// (so `fr-CA` resolves to `fr`). Returns the listed spelling or null.
export function matchListed(tag: string, listed: readonly string[]): string | null {
  const wanted = tag.trim().toLowerCase();
  if (wanted === "") return null;
  const exact = listed.find((candidate) => candidate.toLowerCase() === wanted);
  if (exact !== undefined) return exact;
  const base = wanted.split("-")[0];
  if (base === undefined || base === wanted) return null;
  return listed.find((candidate) => candidate.toLowerCase() === base) ?? null;
}

// resolveLanguage applies the order: user choice, browser choice, each
// navigator language (regional, then base), instance default, then "en".
// Candidates that do not match a listed language are skipped.
export function resolveLanguage(input: ResolveInput): string {
  const candidates: (string | null | undefined)[] = [
    input.userChoice,
    input.browserChoice,
    ...(input.navigatorLanguages ?? []),
    input.instanceDefault,
  ];
  for (const candidate of candidates) {
    if (!candidate) continue;
    const matched = matchListed(candidate, input.listed);
    if (matched !== null) return matched;
  }
  return "en";
}
