package modsrc

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

// TestReadLocaleFilesSkipsReviewFile checks that .review.json is neither read as a locale nor reported as oversized.
func TestReadLocaleFilesSkipsReviewFile(t *testing.T) {
	t.Parallel()
	fsys := moduleDirFS(map[string]map[string]string{"mc": validModuleFiles("mc", "1.0.0")})
	fsys["mc/locales/fr.json"] = &fstest.MapFile{Data: []byte(`{"a":1}`)}
	fsys["mc/locales/.review.json"] = &fstest.MapFile{Data: []byte(`{"fr":{"a":"000000000000"}}`)}

	b, err := bundleFromDir(fsys, "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Locales) != 1 || string(b.Locales["fr"]) != `{"a":1}` {
		t.Fatalf("Locales = %v, want fr only", b.Locales)
	}
	if _, ok := b.Locales[".review"]; ok {
		t.Fatal(`Locales contains ".review"`)
	}
	if len(b.OversizedLocales) != 0 {
		t.Fatalf("OversizedLocales = %v, want none", b.OversizedLocales)
	}
}

// TestReadLocaleFilesReviewFileDoesNotCountTowardBudget checks that a review file at the layer cap does not push a later language out of the total budget.
func TestReadLocaleFilesReviewFileDoesNotCountTowardBudget(t *testing.T) {
	t.Parallel()
	// ReadDir sorts ".review.json" before "de.json", so an uncounted review
	// file leaves room for the three fillers plus fr (4 * layer cap fits in
	// MaxLocaleTotalSize). Counting the review file would push fr over it.
	layer := strings.Repeat("x", oci.MaxLocaleLayerSize)
	fsys := moduleDirFS(map[string]map[string]string{"mc": validModuleFiles("mc", "1.0.0")})
	fsys["mc/locales/.review.json"] = &fstest.MapFile{Data: []byte(layer)}
	fsys["mc/locales/de.json"] = &fstest.MapFile{Data: []byte(layer)}
	fsys["mc/locales/en.json"] = &fstest.MapFile{Data: []byte(layer)}
	fsys["mc/locales/es.json"] = &fstest.MapFile{Data: []byte(layer)}
	fsys["mc/locales/fr.json"] = &fstest.MapFile{Data: []byte(layer)}

	b, err := bundleFromDir(fsys, "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Locales["fr"]; !ok {
		t.Fatalf("fr missing from Locales; Locales keys = %d, OversizedLocales = %v", len(b.Locales), b.OversizedLocales)
	}
	if len(b.OversizedLocales) != 0 {
		t.Fatalf("OversizedLocales = %v, want none", b.OversizedLocales)
	}
}
