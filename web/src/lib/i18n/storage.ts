// Per-browser language preference (spec 019, FR-005). The value is
// `{choice, applied}`: `choice` is what the viewer picked on this browser,
// `applied` is the language last rendered (read pre-React by the boot snippet
// in index.html). Storage can be unavailable (private mode, blocked site data),
// so every access is guarded and failures degrade to "no preference".

export const LANGUAGE_STORAGE_KEY = "gameplane-language";

export interface LanguageStorage {
  choice: string | null;
  applied: string | null;
}

const EMPTY: LanguageStorage = { choice: null, applied: null };

export function readLanguageStorage(): LanguageStorage {
  try {
    const raw = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
    if (!raw) return { ...EMPTY };
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return { ...EMPTY };
    const record = parsed as Partial<Record<keyof LanguageStorage, unknown>>;
    return {
      choice: typeof record.choice === "string" ? record.choice : null,
      applied: typeof record.applied === "string" ? record.applied : null,
    };
  } catch {
    return { ...EMPTY };
  }
}

export function writeLanguageStorage(patch: { choice?: string | null; applied?: string }): void {
  try {
    const current = readLanguageStorage();
    const next: LanguageStorage = {
      choice: patch.choice !== undefined ? patch.choice : current.choice,
      applied: patch.applied !== undefined ? patch.applied : current.applied,
    };
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, JSON.stringify(next));
  } catch {
    // Storage unavailable: the language still applies for this page view.
  }
}
