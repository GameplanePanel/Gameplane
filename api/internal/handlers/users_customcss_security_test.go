package handlers

import (
	"strings"
	"testing"
)

// Custom CSS rules that reach past the plain-text checks are refused.
func TestCustomCSS_RejectsEscapesAndExternalReferences(t *testing.T) {
	cases := []struct {
		name    string
		css     string
		wantMsg string
	}{
		{"backslash escape hides @import", `.a{color:red}\69mport`, "CSS escapes"},
		{"hex escape in selector", `\62ody{color:red}`, "CSS escapes"},
		{"image-set with quoted https url", `.a{background:image-set("https://example.com/a.png" 1x)}`, "external references"},
		{"protocol-relative quoted reference", `.a{b:"//example.com/x"}`, "external references"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := sanitizeCustomCSS(tc.css)
			if msg == "" || !strings.Contains(msg, tc.wantMsg) {
				t.Fatalf("sanitizeCustomCSS(%q) = %q, want message containing %q", tc.css, msg, tc.wantMsg)
			}
		})
	}
}

func TestCustomCSS_AcceptsPlainRules(t *testing.T) {
	if msg := sanitizeCustomCSS(`.server-card{color:#222;border-radius:4px}`); msg != "" {
		t.Fatalf("plain stylesheet rejected: %q", msg)
	}
}
