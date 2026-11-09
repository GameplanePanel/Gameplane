package i18n

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// TestStampMovesInlineHashToReview checks that Stamp moves an inline source hash into the review file.
func TestStampMovesInlineHashToReview(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	locales := filepath.Join(dir, "locales")
	if err := os.MkdirAll(locales, 0o750); err != nil {
		t.Fatal(err)
	}
	hash := pack.SourceHash("Demo Game")
	fr := filepath.Join(locales, "fr.json")
	body := `{"template.displayName":{"defaultMessage":"Jeu","sourceHash":"` + hash + `"}}`
	if err := os.WriteFile(fr, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	stamped, err := Stamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(stamped) != 2 {
		t.Fatalf("Stamp = %v; want fr.json and .review.json", stamped)
	}
	frData, err := os.ReadFile(fr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(frData), "sourceHash") {
		t.Errorf("fr.json still carries sourceHash: %s", frData)
	}
	reviewData, err := os.ReadFile(filepath.Join(locales, pack.ReviewFile))
	if err != nil {
		t.Fatal(err)
	}
	review, err := pack.ParseModuleReview(reviewData)
	if err != nil {
		t.Fatal(err)
	}
	if got := review["fr"]["template.displayName"]; got != hash {
		t.Errorf("review hash = %q; want %q", got, hash)
	}
}

// TestStampKeepsExistingReview checks that Stamp keeps an existing review entry instead of overwriting it.
func TestStampKeepsExistingReview(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	locales := filepath.Join(dir, "locales")
	if err := os.MkdirAll(locales, 0o750); err != nil {
		t.Fatal(err)
	}
	hash := pack.SourceHash("Demo Game")
	review := `{"fr":{"template.displayName":"` + hash + `"}}`
	if err := os.WriteFile(filepath.Join(locales, pack.ReviewFile), []byte(review), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locales, "fr.json"), []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stamped, err := Stamp(dir)
	if err != nil || len(stamped) != 0 {
		t.Fatalf("Stamp = %v, %v; want a no-op", stamped, err)
	}
	after, err := os.ReadFile(filepath.Join(locales, pack.ReviewFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != review {
		t.Errorf("review file was rewritten: %q", after)
	}
}

// TestStampMalformedReviewIsError checks that Stamp reports an error when the review file is malformed.
func TestStampMalformedReviewIsError(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	locales := filepath.Join(dir, "locales")
	if err := os.MkdirAll(locales, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locales, pack.ReviewFile), []byte(`[`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Stamp(dir)
	if err == nil || !strings.Contains(err.Error(), pack.ReviewFile) {
		t.Fatalf("Stamp error = %v; want one naming %s", err, pack.ReviewFile)
	}
}

// TestStampWritesNothingWhenALocaleIsMalformed checks that a malformed locale
// stops Stamp before any locale file loses its inline hashes.
func TestStampWritesNothingWhenALocaleIsMalformed(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	locales := filepath.Join(dir, "locales")
	if err := os.MkdirAll(locales, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locales, "en.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	de := `{"template.displayName":{"defaultMessage":"Spiel","sourceHash":"000000000000"}}`
	if err := os.WriteFile(filepath.Join(locales, "de.json"), []byte(de), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locales, "fr.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Stamp(dir); err == nil {
		t.Fatal("Stamp succeeded with a malformed fr.json")
	}
	got, err := os.ReadFile(filepath.Join(locales, "de.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != de {
		t.Errorf("de.json = %q; want it unchanged", got)
	}
	if _, err := os.Stat(filepath.Join(locales, pack.ReviewFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("review file exists or stat failed: %v", err)
	}
}
