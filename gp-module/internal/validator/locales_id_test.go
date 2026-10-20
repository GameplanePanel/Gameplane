package validator

import (
	"strings"
	"testing"
)

func localeIDRuleFindings(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.RuleID == RuleLocaleIDInvalid {
			out = append(out, f)
		}
	}
	return out
}

func TestValidateLocaleIDsSpacedConfigKey(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml":   []byte(localeModuleYAML),
		"template.yaml": []byte("spec:\n  displayName: Demo Game\n  configSchema:\n    - name: MAX PLAYERS\n      displayName: Max players\n"),
	}
	got := validateLocaleIDs(files)
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly one", got)
	}
	f := got[0]
	if f.RuleID != RuleLocaleIDInvalid || f.Level != SeverityError || f.File != "template.yaml" {
		t.Fatalf("finding = %+v", f)
	}
	const id = "config.MAX PLAYERS.displayName"
	if f.Field != id || !strings.Contains(f.Message, `"`+id+`"`) {
		t.Fatalf("finding does not name %q: %+v", id, f)
	}
	if f.Remediation == "" {
		t.Error("finding has no remediation")
	}
}

func TestValidateLocaleIDsCleanTemplate(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml":   []byte(localeModuleYAML),
		"template.yaml": []byte("spec:\n  displayName: Demo Game\n  configSchema:\n    - name: MAX_PLAYERS\n      displayName: Max players\n      enum: [easy, hard]\n"),
	}
	if got := validateLocaleIDs(files); len(got) != 0 {
		t.Fatalf("unexpected findings: %v", got)
	}
	if got := localeIDRuleFindings(validateLocales(files)); len(got) != 0 {
		t.Fatalf("unexpected locale-id-invalid findings: %v", got)
	}
}
