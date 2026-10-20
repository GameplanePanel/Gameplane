package packager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func archiveNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
}

func writeModuleBase(t *testing.T, dir string) {
	t.Helper()
	for name, body := range map[string]string{"module.yaml": "name: demo\n", "template.yaml": "spec: {}\n", "README.md": "# demo\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateArchiveFromDirCarriesLocales(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeModuleBase(t, dir)
	writeLocale(t, dir, "fr.json", `{"a":1}`)
	writeLocale(t, dir, "en.json", `{}`)
	writeLocale(t, dir, "de.json", `{}`)

	data, _, err := CreateArchiveFromDir(dir, PackageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	names := archiveNames(t, data)
	want := []string{"module.yaml", "template.yaml", "README.md", "locales/de.json", "locales/fr.json"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

func TestCreateArchiveFromDirRejectsInvalidLocaleTag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeModuleBase(t, dir)
	writeLocale(t, dir, "xx_YY.json", `{}`)
	if _, _, err := CreateArchiveFromDir(dir, PackageLimits{}); err == nil {
		t.Fatal("invalid language tag accepted")
	}
}
