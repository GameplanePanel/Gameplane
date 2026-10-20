package modsrc

import (
	"testing"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

const localesTestMetadata = "name: demo\ndisplayName: Demo\nversion: 1.0.0\n"

func TestFromFilesCollectsLocales(t *testing.T) {
	t.Parallel()
	b, err := FromFiles("sha256:x", map[string][]byte{
		FileMetadata:      []byte(localesTestMetadata),
		FileTemplate:      []byte("spec: {}\n"),
		"locales/fr.json": []byte(`{"a":{"defaultMessage":"x"}}`),
		"locales/en.json": []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Locales) != 1 || string(b.Locales["fr"]) != `{"a":{"defaultMessage":"x"}}` {
		t.Fatalf("Locales = %v", b.Locales)
	}
}

func TestFromFilesWithoutLocalesIsNil(t *testing.T) {
	t.Parallel()
	b, err := FromFiles("sha256:x", map[string][]byte{
		FileMetadata: []byte(localesTestMetadata),
		FileTemplate: []byte("spec: {}\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Locales != nil {
		t.Fatalf("Locales = %v, want nil", b.Locales)
	}
}

func TestFromFilesRecordsOversizedLocales(t *testing.T) {
	t.Parallel()
	b, err := FromFiles("sha256:x", map[string][]byte{
		FileMetadata:      []byte(localesTestMetadata),
		FileTemplate:      []byte("spec: {}\n"),
		"locales/fr.json": []byte(`{}`),
		"locales/de.json": oci.OversizedLocale,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.OversizedLocales) != 1 || b.OversizedLocales[0] != "de" {
		t.Fatalf("OversizedLocales = %v, want [de]", b.OversizedLocales)
	}
	if _, ok := b.Locales["de"]; ok || len(b.Locales) != 1 {
		t.Fatalf("Locales = %v, want fr only", b.Locales)
	}
}
