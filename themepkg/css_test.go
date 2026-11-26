package themepkg

import (
	"strings"
	"testing"
)

// Custom CSS rules that reach past the plain-text checks are refused; the
// cases mirror the API FR-013 security tests.
func TestSanitizeCSS_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		css        string
		wantRule   string
		wantDetail string
		wantLine   int
	}{
		{
			name:       "backslash escape hides @import",
			css:        `.a{color:red}\69mport`,
			wantRule:   RuleCSSRemote,
			wantDetail: `CSS escapes (\) are not allowed`,
			wantLine:   1,
		},
		{
			name:       "hex escape in selector",
			css:        `\62ody{color:red}`,
			wantRule:   RuleCSSRemote,
			wantDetail: `CSS escapes (\) are not allowed`,
			wantLine:   1,
		},
		{
			name:       "image-set with quoted https url",
			css:        `.a{background:image-set("https://example.com/a.png" 1x)}`,
			wantRule:   RuleCSSRemote,
			wantDetail: "external references are not allowed",
			wantLine:   1,
		},
		{
			name:       "protocol-relative quoted reference",
			css:        `.a{b:"//example.com/x"}`,
			wantRule:   RuleCSSRemote,
			wantDetail: "external references are not allowed",
			wantLine:   1,
		},
		{
			name:       "tab split scheme is caught by stripped check",
			css:        ".a{b:ht\ttps://x}",
			wantRule:   RuleCSSRemote,
			wantDetail: "external references are not allowed",
			wantLine:   0,
		},
		{
			name:       "newline split scheme is caught by stripped check",
			css:        ".a{b:ht\ntps://x}",
			wantRule:   RuleCSSRemote,
			wantDetail: "external references are not allowed",
			wantLine:   0,
		},
		{
			name:       "external reference on a later line",
			css:        ".a{}\n.b{}\n.c{b:\"https://x\"}",
			wantRule:   RuleCSSRemote,
			wantDetail: "external references are not allowed",
			wantLine:   3,
		},
		{
			name:       "oversized",
			css:        strings.Repeat("a", MaxCSSBytes+1),
			wantRule:   RuleCSSSize,
			wantDetail: "exceeds the maximum length of 32768 characters",
			wantLine:   0,
		},
		{
			name:       "import lower case",
			css:        ".a{}\n@import url(x.css);",
			wantRule:   RuleCSSImport,
			wantDetail: "@import rules are not allowed",
			wantLine:   2,
		},
		{
			name:       "import upper case",
			css:        "@IMPORT 'x.css';",
			wantRule:   RuleCSSImport,
			wantDetail: "@import rules are not allowed",
			wantLine:   1,
		},
		{
			name:       "url http",
			css:        ".a{background:url(http://x.test/a.png)}",
			wantRule:   RuleCSSURL,
			wantDetail: "external url() reference 'http://x.test/a.png' is not allowed",
			wantLine:   1,
		},
		{
			name:       "url https quoted with spaces and upper case scheme",
			css:        ".a{background:url( \"HTTPS://x/a.png\" )}",
			wantRule:   RuleCSSURL,
			wantDetail: "external url() reference 'HTTPS://x/a.png' is not allowed",
			wantLine:   1,
		},
		{
			name:       "url protocol-relative",
			css:        ".a{}\n.b{background:url(//x.test/a.png)}",
			wantRule:   RuleCSSURL,
			wantDetail: "external url() reference '//x.test/a.png' is not allowed",
			wantLine:   2,
		},
		{
			name:       "unbalanced open brace",
			css:        ".a{color:red",
			wantRule:   RuleCSSSyntax,
			wantDetail: "unbalanced braces",
			wantLine:   0,
		},
		{
			name:       "unbalanced close brace",
			css:        ".a{}}",
			wantRule:   RuleCSSSyntax,
			wantDetail: "unbalanced braces",
			wantLine:   0,
		},
		{
			name:       "style open delimiter",
			css:        ".a{}\n<STYLE>",
			wantRule:   RuleCSSRemote,
			wantDetail: `HTML delimiter "<style" is not allowed`,
			wantLine:   2,
		},
		{
			name:       "style close delimiter",
			css:        ".a{}</style>",
			wantRule:   RuleCSSRemote,
			wantDetail: `HTML delimiter "</style" is not allowed`,
			wantLine:   1,
		},
		{
			name:       "script open delimiter",
			css:        ".a{}<script>",
			wantRule:   RuleCSSRemote,
			wantDetail: `HTML delimiter "<script" is not allowed`,
			wantLine:   1,
		},
		{
			name:       "script close delimiter",
			css:        ".a{}\n\n</SCRIPT>",
			wantRule:   RuleCSSRemote,
			wantDetail: `HTML delimiter "</script" is not allowed`,
			wantLine:   3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := SanitizeCSS(tc.css)
			if ok {
				t.Fatalf("SanitizeCSS(%q) accepted, want rule %s", tc.css, tc.wantRule)
			}
			if p.Rule != tc.wantRule || p.Detail != tc.wantDetail || p.Line != tc.wantLine {
				t.Fatalf("SanitizeCSS(%q) = %+v, want {%s %q line %d}", tc.css, p, tc.wantRule, tc.wantDetail, tc.wantLine)
			}
		})
	}
}

func TestSanitizeCSS_Accepts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		css  string
	}{
		{"empty", ""},
		{"plain rules", `.server-card{color:#222;border-radius:4px}`},
		{"data uri allowed", `.a{background:url(data:image/png;base64,AAAA)}`},
		{"data uri quoted", `.a{background:url("data:image/svg+xml,%3Csvg%3E")}`},
		{"comment only", `/* theme */ .a{}`},
		{"exactly max size", strings.Repeat("a", MaxCSSBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if p, ok := SanitizeCSS(tc.css); !ok {
				t.Fatalf("SanitizeCSS(%q) rejected: %+v", tc.css, p)
			}
		})
	}
}

func TestSanitizeCSS_FirstFailingRuleWins(t *testing.T) {
	t.Parallel()
	// Oversized input reports the size rule even when it also has an
	// @import, and an @import is reported before a url() reference.
	big := "@import x;" + strings.Repeat("a", MaxCSSBytes)
	if p, _ := SanitizeCSS(big); p.Rule != RuleCSSSize {
		t.Fatalf("oversized rule = %q, want %q", p.Rule, RuleCSSSize)
	}
	if p, _ := SanitizeCSS("@import x; .a{b:url(https://x)}"); p.Rule != RuleCSSImport {
		t.Fatalf("import rule = %q, want %q", p.Rule, RuleCSSImport)
	}
}

func TestCheckCSSComments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		css      string
		ok       bool
		wantLine int
	}{
		{"empty", "", true, 0},
		{"no comments", ".a{}", true, 0},
		{"closed comment", "/* ok */ .a{}", true, 0},
		{"two closed comments", "/* a */ .a{} /* b */", true, 0},
		{"multi-line closed comment", "/*\n\n*/ .a{}", true, 0},
		{"unterminated", "a{}\n/* open\n.b{}", false, 2},
		{"second opener unterminated", "/* a */ /* b", false, 1},
		{"slash star slash is open", "/*/", false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := CheckCSSComments(tc.css)
			if ok != tc.ok {
				t.Fatalf("CheckCSSComments(%q) ok = %v, want %v (%+v)", tc.css, ok, tc.ok, p)
			}
			if tc.ok {
				return
			}
			if p.Rule != RuleCSSSyntax || p.Detail != "unterminated comment" || p.Line != tc.wantLine {
				t.Fatalf("CheckCSSComments(%q) = %+v, want syntax unterminated comment line %d", tc.css, p, tc.wantLine)
			}
		})
	}
}
