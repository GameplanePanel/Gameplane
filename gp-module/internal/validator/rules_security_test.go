package validator

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
)

// secTemplate builds a minimal template.yaml whose spec.security block is the
// given YAML fragment (already indented under "spec:").
func secTemplate(security string) string {
	return "apiVersion: gameplane.local/v1alpha1\n" +
		"kind: GameTemplate\n" +
		"metadata:\n  name: sec-demo\n" +
		"spec:\n" +
		"  game: sec-demo\n" +
		"  image: example/sec-demo@sha256:" + strings.Repeat("a", 64) + "\n" +
		"  displayName: Security Demo\n" +
		security
}

func parseSecTemplate(t *testing.T, src string) *yaml.Node {
	t.Helper()
	node, err := common.ParseYAMLNode([]byte(src))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return node
}

func securityFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if strings.HasPrefix(f.Field, "spec.security.") {
			out = append(out, f)
		}
	}
	return out
}

func TestValidateSecurityRisky_Cases(t *testing.T) {
	longReason := strings.Repeat("r", 200)
	tooLongReason := strings.Repeat("r", 201)

	tests := []struct {
		name      string
		security  string
		wantCount int
		wantField string
	}{
		{
			name:      "no security block",
			security:  "",
			wantCount: 0,
		},
		{
			name:      "no risky map",
			security:  "  security:\n    fsGroup: 1000\n",
			wantCount: 0,
		},
		{
			name:      "risky names each setting with a reason",
			security:  "  security:\n    runAsUser: 2000\n    risky:\n      runAsUser:\n        reason: Image only works as uid 2000.\n      fsGroup: {}\n",
			wantCount: 0,
		},
		{
			name:      "risky key that is not a setting",
			security:  "  security:\n    risky:\n      hostNetwork:\n        reason: nope\n",
			wantCount: 1,
			wantField: "spec.security.risky.hostNetwork",
		},
		{
			name:      "risky key with a null value is still a setting check",
			security:  "  security:\n    risky:\n      runAsGroupX:\n",
			wantCount: 1,
			wantField: "spec.security.risky.runAsGroupX",
		},
		{
			name:      "reason exactly 200 characters is accepted",
			security:  "  security:\n    risky:\n      runAsUser:\n        reason: " + longReason + "\n",
			wantCount: 0,
		},
		{
			name:      "reason of 201 characters is an error",
			security:  "  security:\n    risky:\n      runAsUser:\n        reason: " + tooLongReason + "\n",
			wantCount: 1,
			wantField: "spec.security.risky.runAsUser.reason",
		},
		{
			name:      "two bad members give two findings",
			security:  "  security:\n    risky:\n      bogus:\n        reason: " + tooLongReason + "\n",
			wantCount: 2,
			wantField: "spec.security.risky.bogus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := parseSecTemplate(t, secTemplate(tc.security))
			got := validateSecurityRisky(node)
			if len(got) != tc.wantCount {
				t.Fatalf("validateSecurityRisky findings = %d, want %d: %+v", len(got), tc.wantCount, got)
			}
			for _, f := range got {
				if f.Level != SeverityError {
					t.Errorf("level = %s, want ERROR", f.Level)
				}
				if f.RuleID != RuleTemplateSchemaViolation {
					t.Errorf("ruleID = %s, want %s", f.RuleID, RuleTemplateSchemaViolation)
				}
			}
			if tc.wantField != "" && (len(got) == 0 || got[0].Field != tc.wantField) {
				t.Errorf("first field = %v, want %s", got, tc.wantField)
			}
		})
	}
}

func TestValidateSecurityOwnershipCheck_Cases(t *testing.T) {
	tests := []struct {
		name      string
		security  string
		wantCount int
	}{
		{
			name:      "no ownership check",
			security:  "  security:\n    fsGroup: 1000\n",
			wantCount: 0,
		},
		{
			name:      "ownership check with fsGroup",
			security:  "  security:\n    fsGroup: 1000\n    volumeOwnershipCheck: EveryStart\n",
			wantCount: 0,
		},
		{
			name:      "ownership check without fsGroup warns",
			security:  "  security:\n    volumeOwnershipCheck: EveryStart\n",
			wantCount: 1,
		},
		{
			name:      "null fsGroup does not count as set",
			security:  "  security:\n    fsGroup:\n    volumeOwnershipCheck: OnlyWhenWrong\n",
			wantCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := parseSecTemplate(t, secTemplate(tc.security))
			got := validateSecurityOwnershipCheck(node)
			if len(got) != tc.wantCount {
				t.Fatalf("validateSecurityOwnershipCheck findings = %d, want %d: %+v", len(got), tc.wantCount, got)
			}
			for _, f := range got {
				if f.Level != SeverityWarn {
					t.Errorf("level = %s, want WARN", f.Level)
				}
				if f.Field != "spec.security.volumeOwnershipCheck" {
					t.Errorf("field = %s, want spec.security.volumeOwnershipCheck", f.Field)
				}
				if f.Line == 0 {
					t.Errorf("line = 0, want the line of volumeOwnershipCheck")
				}
			}
		})
	}
}

// TestValidateFiles_SecurityChecksAreWired proves the checks run from
// ValidateFiles, not only when the functions are called directly.
func TestValidateFiles_SecurityChecksAreWired(t *testing.T) {
	src := secTemplate("  security:\n" +
		"    volumeOwnershipCheck: EveryStart\n" +
		"    risky:\n" +
		"      hostNetwork:\n        reason: nope\n")

	report, err := ValidateFiles("sec-demo", map[string][]byte{"template.yaml": []byte(src)}, ValidateOptions{})
	if err != nil {
		t.Fatalf("ValidateFiles: %v", err)
	}

	var sawRisky, sawOwnership bool
	for _, f := range securityFindings(report.Findings) {
		switch f.Field {
		case "spec.security.risky.hostNetwork":
			sawRisky = f.Level == SeverityError
		case "spec.security.volumeOwnershipCheck":
			sawOwnership = f.Level == SeverityWarn
		}
	}
	if !sawRisky {
		t.Errorf("risky key error not reported by ValidateFiles: %+v", report.Findings)
	}
	if !sawOwnership {
		t.Errorf("ownership warning not reported by ValidateFiles: %+v", report.Findings)
	}
}
