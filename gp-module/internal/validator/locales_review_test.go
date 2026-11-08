package validator

import (
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

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
