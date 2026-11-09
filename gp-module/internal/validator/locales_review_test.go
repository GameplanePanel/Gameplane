package validator

import (
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// TestValidateLocalesReviewStaleHashIsWarning checks that a review hash that no longer matches the English source is a warning.
func TestValidateLocalesReviewStaleHashIsWarning(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json":            `{"template.displayName":{"defaultMessage":"Jeu"}}`,
		"locales/" + pack.ReviewFile: `{"fr":{"template.displayName":"000000000000"}}`,
	})
	got := validateLocales(files)
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly one", got)
	}
	f := got[0]
	if f.RuleID != RuleLocaleNeedsReview || f.Level != SeverityWarn || f.Field != "template.displayName" || f.File != "locales/fr.json" {
		t.Fatalf("finding = %+v", f)
	}
}

// TestValidateLocalesReviewFileIsNotATag checks that the review file is not treated as a language tag.
func TestValidateLocalesReviewFileIsNotATag(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json":            `{"template.displayName":{"defaultMessage":"Jeu"}}`,
		"locales/" + pack.ReviewFile: `{"fr":{"template.displayName":"` + pack.SourceHash("Demo Game") + `"}}`,
	})
	if got := validateLocales(files); len(got) != 0 {
		t.Fatalf("findings = %v, want none", got)
	}
}

// TestValidateLocalesMalformedReviewFile checks that a malformed review file is reported by the locale validator.
func TestValidateLocalesMalformedReviewFile(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json":            `{"template.displayName":{"defaultMessage":"Jeu"}}`,
		"locales/" + pack.ReviewFile: `[`,
	})
	got := localeRuleFindings(validateLocales(files))
	if len(got) != 1 {
		t.Fatalf("locale findings = %v, want exactly one", got)
	}
	if got[0].File != "locales/"+pack.ReviewFile {
		t.Errorf("finding file = %q; want locales/%s", got[0].File, pack.ReviewFile)
	}
}
