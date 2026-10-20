package modsrc

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

func TestReadFileCapped(t *testing.T) {
	fsys := fstest.MapFS{"f": &fstest.MapFile{Data: bytes.Repeat([]byte("x"), 10)}}
	got, err := readFileCapped(fsys, "f", 4)
	if err != nil || len(got) != 5 {
		t.Fatalf("readFileCapped = %d bytes, %v; want 5 bytes", len(got), err)
	}
	if _, err := readFileCapped(fsys, "missing", 4); err == nil {
		t.Fatal("missing file: want an error")
	}
	files := map[string][]byte{}
	big := fstest.MapFS{"m/locales/de.json": &fstest.MapFile{Data: bytes.Repeat([]byte("x"), oci.MaxLocaleLayerSize+1)}}
	readLocaleFiles(big, "m", files)
	if !oci.IsOversizedLocale(files["locales/de.json"]) {
		t.Fatalf("oversized file was not marked")
	}
}
