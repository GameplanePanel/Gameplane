package modsrc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestExtractArchiveCarriesLocales(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := validModuleFiles("mc", "1.0.0")
	files["locales/fr.json"] = `{"a":1}`
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "mc/" + name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	fsys, err := extractArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundleFromDir(fsys, "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Locales["fr"]) != `{"a":1}` {
		t.Fatalf("Locales = %v", b.Locales)
	}
}
