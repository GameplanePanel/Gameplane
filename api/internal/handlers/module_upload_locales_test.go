package handlers

import (
	"strings"
	"testing"
)

func TestParseUploadedBundle_CarriesLocales(t *testing.T) {
	t.Parallel()
	files := validBundleFiles()
	files["factorio/locales/fr.json"] = `{"template.displayName":{"defaultMessage":"Usine"}}`
	files["factorio/locales/pt-BR.json"] = `{}`
	files["factorio/locales/en.json"] = `{}`
	files["factorio/locales/notes.txt"] = `ignored`
	files["factorio/locales/sub/x.json"] = `{}`

	got, _, err := parseUploadedBundle(bundleTarGz(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if string(got["locales.fr.json"]) != files["factorio/locales/fr.json"] || got["locales.pt-BR.json"] == nil {
		t.Fatalf("keys = %v", localeKeys(got))
	}
	for _, key := range []string{"locales.en.json", "locales.notes.json", "locales.x.json", "locales/fr.json"} {
		if _, ok := got[key]; ok {
			t.Errorf("unexpected key %q", key)
		}
	}
}

func TestParseUploadedBundle_LocalesInFlatArchive(t *testing.T) {
	t.Parallel()
	flat := map[string]string{}
	for name, body := range validBundleFiles() {
		flat[strings.TrimPrefix(name, "factorio/")] = body
	}
	flat["locales/fr.json"] = `{}`
	got, _, err := parseUploadedBundle(bundleTarGz(t, flat))
	if err != nil {
		t.Fatal(err)
	}
	if got["locales.fr.json"] == nil {
		t.Fatalf("keys = %v", localeKeys(got))
	}
}

func TestParseUploadedBundle_RejectsInvalidLocaleTag(t *testing.T) {
	t.Parallel()
	files := validBundleFiles()
	files["factorio/locales/xx_YY.json"] = `{}`
	if _, _, err := parseUploadedBundle(bundleTarGz(t, files)); err == nil {
		t.Fatal("invalid language tag accepted")
	}
}

func TestParseUploadedBundle_LocalesCountTowardLimit(t *testing.T) {
	t.Parallel()
	files := validBundleFiles()
	files["factorio/locales/fr.json"] = strings.Repeat("a", maxUploadBundleBytes)
	if _, _, err := parseUploadedBundle(bundleTarGz(t, files)); err == nil {
		t.Fatal("over-limit locale file accepted")
	}
}

func localeKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
