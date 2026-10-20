package catalog

import (
	"fmt"
	"testing"
	"testing/fstest"

	"golang.org/x/text/language"

	"github.com/GameplanePanel/gameplane/i18n"
)

func testFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys["locales/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func metaJSON(tag, direction string) string {
	return fmt.Sprintf(`{"tag":%q,"nativeName":"N-%s","englishName":"E-%s","direction":%q}`, tag, tag, tag, direction)
}

func TestLookupFallbackChain(t *testing.T) {
	c, err := Load(testFS(map[string]string{
		"en/app.json":     `{"a":{"defaultMessage":"A"},"b":{"defaultMessage":"B"},"x":{"defaultMessage":"X"}}`,
		"en/meta.json":    metaJSON("en", "ltr"),
		"fr/app.json":     `{"a":{"defaultMessage":"Afr"},"x":{"defaultMessage":"Xfr"}}`,
		"fr/meta.json":    metaJSON("fr", "ltr"),
		"fr-CA/app.json":  `{"a":{"defaultMessage":"Afr-CA"}}`,
		"fr-CA/meta.json": metaJSON("fr-CA", "ltr"),
	}), "en")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		tag, id, want string
	}{
		{"fr-CA", "a", "Afr-CA"},
		{"fr-CA", "x", "Xfr"},
		{"fr-CA", "b", "B"},
		{"fr", "b", "B"},
		{"en-GB", "a", "A"},
	}
	for _, tc := range tests {
		got, ok := c.Lookup(language.MustParse(tc.tag), tc.id, nil)
		if !ok || got != tc.want {
			t.Errorf("Lookup(%s, %s) = %q, %v; want %q", tc.tag, tc.id, got, ok, tc.want)
		}
	}
}

func TestInvalidTranslationDroppedForReference(t *testing.T) {
	c, err := Load(testFS(map[string]string{
		"en/app.json":  `{"n":{"defaultMessage":"{count} files"}}`,
		"en/meta.json": metaJSON("en", "ltr"),
		"fr/app.json":  `{"n":{"defaultMessage":"{n} fichiers"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	}), "en")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(language.French, "n", map[string]any{"count": 2})
	if !ok || got != "2 files" {
		t.Errorf("Lookup = %q, %v; want %q, true", got, ok, "2 files")
	}
}

func TestLookupMissingID(t *testing.T) {
	c, err := Load(testFS(map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A"}}`,
	}), "en")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(language.English, "nope", nil); ok {
		t.Error("Lookup of an id the reference lacks reported ok")
	}
}

func TestListedThreshold(t *testing.T) {
	c, err := Load(testFS(map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A"},"b":{"defaultMessage":"B"}}`,
		"en/meta.json": metaJSON("en", "ltr"),
		"de/app.json":  `{"a":{"defaultMessage":"Ade"}}`,
		"de/meta.json": metaJSON("de", "ltr"),
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"},"b":{"defaultMessage":"Bfr"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
		"ar/app.json":  `{"a":{"defaultMessage":"Aar"},"b":{"defaultMessage":"Bar"}}`,
		"ar/meta.json": metaJSON("ar", "rtl"),
	}), "en")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.Tags()); got != "[en ar de fr]" {
		t.Errorf("Tags() = %s; want [en ar de fr]", got)
	}
	if got := fmt.Sprint(c.Listed()); got != "[en fr]" {
		t.Errorf("Listed() = %s; want [en fr]", got)
	}
	if c.IsListed("de") {
		t.Error("50% pack reported as listed")
	}
	if !c.IsListed("en") {
		t.Error("reference reported as not listed")
	}
	if comp, ok := c.Completeness("de"); !ok || comp != 50 {
		t.Errorf("Completeness(de) = %v, %v; want 50, true", comp, ok)
	}
	if _, ok := c.Completeness("zz"); ok {
		t.Error("Completeness of an unknown tag reported ok")
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(fstest.MapFS{}, "en"); err == nil {
		t.Error("Load without a locales directory returned no error")
	}
	if _, err := Load(testFS(map[string]string{
		"fr/app.json": `{"a":{"defaultMessage":"Afr"}}`,
	}), "en"); err == nil {
		t.Error("Load without the reference returned no error")
	}
	if _, err := Load(testFS(map[string]string{
		"en/app.json": `{"a":`,
	}), "en"); err == nil {
		t.Error("Load with a malformed reference returned no error")
	}
}

func TestBrokenPacksSkipped(t *testing.T) {
	c, err := Load(testFS(map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A"}}`,
		"en/meta.json": metaJSON("en", "ltr"),
		"de/app.json":  `{"a":`,
		"de/meta.json": metaJSON("de", "ltr"),
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"}}`,
		"fr/meta.json": `{"tag":"fr","nativeName":"x"`,
		"es/app.json":  `{"a":{"defaultMessage":"Aes"}}`,
		"es/meta.json": metaJSON("zz", "ltr"),
		"it/meta.json": metaJSON("it", "ltr"),
		"pt/app.json":  `{"a":{"defaultMessage":"Apt"}}`,
		"pt/meta.json": metaJSON("pt", "ltr"),
	}), "en")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.Tags()); got != "[en pt]" {
		t.Errorf("Tags() = %s; want [en pt]", got)
	}
	if !c.IsListed("pt") {
		t.Error("valid pack pt not listed")
	}
}

func TestEmptyCatalogLookup(t *testing.T) {
	c := newCatalog("en")
	if _, ok := c.Lookup(language.English, "a", nil); ok {
		t.Error("empty catalog returned a message")
	}
	if tags := c.Tags(); len(tags) != 0 {
		t.Errorf("empty catalog Tags() = %v", tags)
	}
}

func TestDefaultLoadsEmbeddedPacks(t *testing.T) {
	if _, err := Load(i18n.Locales, "en"); err != nil {
		t.Fatalf("Load(i18n.Locales) error: %v", err)
	}
	tags := Tags()
	if len(tags) == 0 || tags[0] != "en" {
		t.Fatalf("Default().Tags() = %v; want reference first", tags)
	}
	if !IsListed("en") {
		t.Error("reference not listed in the default catalog")
	}
	if got := Listed(); len(got) == 0 || got[0] != "en" {
		t.Errorf("Listed() = %v; want reference first", got)
	}
	if _, ok := Lookup(language.English, "missing.id", nil); ok {
		t.Error("Lookup of a missing id reported ok")
	}
}
