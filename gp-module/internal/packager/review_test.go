package packager

import (
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// TestReadLocaleFilesSkipsReviewFile checks that the packager skips the review file when reading locale files.
func TestReadLocaleFilesSkipsReviewFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLocale(t, dir, pack.ReviewFile, `{"fr":{"template.displayName":"000000000000"}}`)
	writeLocale(t, dir, "fr.json", `{"template.displayName":{"defaultMessage":"Jeu"}}`)

	got, err := ReadLocaleFiles(dir)
	if err != nil {
		t.Fatalf("ReadLocaleFiles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("keys = %v; want only locales/fr.json", got)
	}
	if _, ok := got["locales/fr.json"]; !ok {
		t.Errorf("keys = %v; want locales/fr.json", got)
	}
}
