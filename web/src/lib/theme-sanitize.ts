// Client mirror of the FR-013 custom CSS sanitizer. The server gate in
// api/internal/handlers/users.go (sanitizeCustomCSS) is authoritative; this
// module replicates its rules, check ORDER, and message formats exactly so
// the editor can warn before save. Rules (in order):
//   1. size cap: 32,768 bytes (mirrors Go len(), i.e. UTF-8 byte length)
//   2. @import rules rejected
//   3. external url() references rejected (absolute http(s) case-insensitive
//      or protocol-relative //host); inline data: URIs and relative/bare
//      url() are allowed
//   4. unbalanced braces rejected
//   5. HTML <style>/<script> delimiters rejected (DOM-escape defense)
//   6. CSS escapes (any backslash) rejected: they could hide rules 2 and 3
//   7. any other absolute or protocol-relative reference rejected (image-set
//      strings, @font-face src, …), not only url(...)

export const MAX_CUSTOM_CSS_LEN = 32768;

export type SanitizeResult = { ok: true } | { ok: false; error: string };

const CSS_IMPORT_RE = /@import\b/i;
const CSS_URL_RE = /url\(\s*["']?([^"')]*)["']?\s*\)/gi;
const HTML_DELIMITERS = ["<style", "</style", "<script", "</script"];
// Mirrors cssExternalRefRE in api/internal/handlers/users.go.
const CSS_EXTERNAL_REF_RE = /(https?:|["'(\s,]\/\/)/i;

// utf8Len mirrors Go's len(string) for the size cap.
function utf8Len(s: string): number {
  return new TextEncoder().encode(s).length;
}

export function sanitizeCustomCss(css: string): SanitizeResult {
  if (utf8Len(css) > MAX_CUSTOM_CSS_LEN) {
    return {
      ok: false,
      error: `customCss rejected: exceeds the maximum length of ${MAX_CUSTOM_CSS_LEN} characters`,
    };
  }
  if (CSS_IMPORT_RE.test(css)) {
    return { ok: false, error: "customCss rejected: @import rules are not allowed" };
  }
  CSS_URL_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CSS_URL_RE.exec(css)) !== null) {
    const ref = (m[1] ?? "").trim();
    const lower = ref.toLowerCase();
    if (lower.startsWith("http://") || lower.startsWith("https://") || ref.startsWith("//")) {
      return {
        ok: false,
        error: `customCss rejected: external url() reference '${ref}' is not allowed`,
      };
    }
  }
  const opens = (css.match(/\{/g) ?? []).length;
  const closes = (css.match(/\}/g) ?? []).length;
  if (opens !== closes) {
    return { ok: false, error: "customCss rejected: unbalanced braces" };
  }
  const lower = css.toLowerCase();
  for (const d of HTML_DELIMITERS) {
    if (lower.includes(d)) {
      return { ok: false, error: `customCss rejected: HTML delimiter "${d}" is not allowed` };
    }
  }
  if (css.includes("\\")) {
    return { ok: false, error: "customCss rejected: CSS escapes (\\) are not allowed" };
  }
  // URL parsing drops ASCII tab and newline, so "ht\ttps://" still
  // fetches: check the text with them removed as well.
  if (CSS_EXTERNAL_REF_RE.test(css) || CSS_EXTERNAL_REF_RE.test(css.replace(/[\t\n\r]/g, ""))) {
    return { ok: false, error: "customCss rejected: external references are not allowed" };
  }
  return { ok: true };
}
