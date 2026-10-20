package validator

import "testing"

func TestValidateLocalesStaleHashIsWarning(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json": `{"template.displayName":{"defaultMessage":"Jeu","sourceHash":"000000000000"}}`,
	})
	got := validateLocales(files)
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly one", got)
	}
	f := got[0]
	if f.RuleID != RuleLocaleNeedsReview || f.Level != SeverityWarn || f.Field != "template.displayName" || f.File != "locales/fr.json" {
		t.Fatalf("finding = %+v", f)
	}
	if len(localeRuleFindings(got)) != 0 {
		t.Error("a stale hash must not be a locale-invalid error")
	}
}
