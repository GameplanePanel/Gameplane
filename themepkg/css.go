package themepkg

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxCSSBytes is the largest theme.css accepted (contract: 32 KiB).
const MaxCSSBytes = 32768

var (
	// cssImportRE matches an @import rule anywhere in the stylesheet.
	cssImportRE = regexp.MustCompile(`(?i)@import\b`)
	// cssURLRE captures the reference inside each url(...) so it can be
	// inspected; surrounding quotes are optional and excluded.
	cssURLRE = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]*)["']?\s*\)`)
	// cssExternalRefRE finds an absolute or protocol-relative reference
	// anywhere in the stylesheet, inside or outside url().
	cssExternalRefRE = regexp.MustCompile(`(?i)(https?:|["'(\s,]//)`)
	// urlControlStripper removes the characters URL parsing ignores.
	urlControlStripper = strings.NewReplacer("\t", "", "\n", "", "\r", "")
)

// cssLine returns the 1-based line that contains byte offset idx of css.
func cssLine(css string, idx int) int {
	return strings.Count(css[:idx], "\n") + 1
}

// SanitizeCSS applies the spec 016 FR-013 custom CSS rules to a stylesheet.
// It reports the first rule the stylesheet breaks and returns ok=true (with
// a zero Problem) when the CSS is acceptable. It rejects oversized payloads,
// @import rules, external url() references (absolute http(s) or
// protocol-relative, remote fonts included) while allowing inline data: URIs,
// unbalanced braces, anything smuggling HTML <style>/<script> delimiters, CSS
// escapes, and any other absolute or protocol-relative reference
// (image-set, src()).
func SanitizeCSS(css string) (Problem, bool) {
	if len(css) > MaxCSSBytes {
		return Problem{
			Rule:   RuleCSSSize,
			Detail: fmt.Sprintf("exceeds the maximum length of %d characters", MaxCSSBytes),
		}, false
	}
	if loc := cssImportRE.FindStringIndex(css); loc != nil {
		return Problem{
			Rule:   RuleCSSImport,
			Detail: "@import rules are not allowed",
			Line:   cssLine(css, loc[0]),
		}, false
	}
	for _, m := range cssURLRE.FindAllStringSubmatchIndex(css, -1) {
		ref := strings.TrimSpace(css[m[2]:m[3]])
		lower := strings.ToLower(ref)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(ref, "//") {
			return Problem{
				Rule:   RuleCSSURL,
				Detail: fmt.Sprintf("external url() reference '%s' is not allowed", ref),
				Line:   cssLine(css, m[0]),
			}, false
		}
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		return Problem{Rule: RuleCSSSyntax, Detail: "unbalanced braces"}, false
	}
	lower := strings.ToLower(css)
	for _, d := range []string{"<style", "</style", "<script", "</script"} {
		if idx := strings.Index(lower, d); idx >= 0 {
			return Problem{
				Rule:   RuleCSSRemote,
				Detail: fmt.Sprintf("HTML delimiter %q is not allowed", d),
				Line:   cssLine(lower, idx),
			}, false
		}
	}
	// CSS escapes (\69mport, u\72l(...)) would hide the rules above from a
	// plain text match, and image-set("https://...") or src("...") load a
	// resource without the url( token, so refuse both forms outright.
	if idx := strings.IndexByte(css, '\\'); idx >= 0 {
		return Problem{
			Rule:   RuleCSSRemote,
			Detail: "CSS escapes (\\) are not allowed",
			Line:   cssLine(css, idx),
		}, false
	}
	// URL parsing drops ASCII tab and newline, so "ht\ttps://" still
	// fetches: check the text with them removed as well.
	if loc := cssExternalRefRE.FindStringIndex(css); loc != nil {
		return Problem{
			Rule:   RuleCSSRemote,
			Detail: "external references are not allowed",
			Line:   cssLine(css, loc[0]),
		}, false
	}
	if cssExternalRefRE.MatchString(urlControlStripper.Replace(css)) {
		return Problem{
			Rule:   RuleCSSRemote,
			Detail: "external references are not allowed",
		}, false
	}
	return Problem{}, true
}

// CheckCSSComments reports the first comment opened with "/*" that has no
// closing "*/" after it. It is separate from SanitizeCSS so the spec 016
// behaviour of the API gate is unchanged. ok=true means every comment closes.
func CheckCSSComments(css string) (Problem, bool) {
	pos := 0
	for {
		i := strings.Index(css[pos:], "/*")
		if i < 0 {
			return Problem{}, true
		}
		start := pos + i
		j := strings.Index(css[start+2:], "*/")
		if j < 0 {
			return Problem{
				Rule:   RuleCSSSyntax,
				Detail: "unterminated comment",
				Line:   cssLine(css, start),
			}, false
		}
		pos = start + 2 + j + 2
	}
}
