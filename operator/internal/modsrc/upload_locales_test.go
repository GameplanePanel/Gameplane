package modsrc

import (
	"context"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

func TestUploadFetcherPullCarriesLocales(t *testing.T) {
	t.Parallel()
	files := validModuleFiles("mc", "1.0.0")
	files["locales.fr.json"] = `{"a":1}`
	files["locales.pt-BR.json"] = `{"b":2}`
	files["locales.de.json"] = strings.Repeat("x", oci.MaxLocaleLayerSize+1)
	f := newUpload(uploadClient(uploadCM("bundle-mc", files)), "gameplane-system", nil)

	b, err := f.Pull(context.Background(), "mc", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Locales["fr"]) != `{"a":1}` || string(b.Locales["pt-BR"]) != `{"b":2}` || len(b.Locales) != 2 {
		t.Fatalf("Locales = %v", b.Locales)
	}
	if len(b.OversizedLocales) != 1 || b.OversizedLocales[0] != "de" {
		t.Fatalf("OversizedLocales = %v", b.OversizedLocales)
	}
}

func TestUploadBundleFilesKeepsOriginal(t *testing.T) {
	t.Parallel()
	in := map[string][]byte{FileMetadata: []byte("m"), "locales.fr.json": []byte("x"), "locales..json": []byte("y")}
	out := uploadBundleFiles(in)
	if _, ok := out["locales/fr.json"]; !ok {
		t.Errorf("locales/fr.json missing: %v", out)
	}
	if _, ok := in["locales/fr.json"]; ok {
		t.Error("input map modified")
	}
	if _, ok := out["locales..json"]; !ok {
		t.Error("empty-tag key should be left untouched")
	}
}
