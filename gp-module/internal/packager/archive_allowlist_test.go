package packager

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestCreateArchiveFromDirLeavesOutNonStandardFiles checks that the .tar.gz
// export ships only the standard module files and locales, as the OCI push
// does: a dotfile with a secret, a notes file and an earlier bundle in the
// module directory are not archived.
func TestCreateArchiveFromDirLeavesOutNonStandardFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeModuleBase(t, dir)
	for name, body := range map[string]string{
		"icon.png":     "png-bytes",
		".env":         "REGISTRY_TOKEN=secret",
		".npmrc":       "//registry/:_authToken=secret",
		"notes.txt":    "scratch",
		"demo.tar.gz":  "older bundle",
		"module.yaml~": "editor backup",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLocale(t, dir, "fr.json", `{"a":1}`)

	data, _, err := CreateArchiveFromDir(dir, PackageLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got := archiveNames(t, data)
	want := []string{"module.yaml", "template.yaml", "README.md", "icon.png", "locales/fr.json"}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want exactly %v", got, want)
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Fatalf("entries = %v, missing %s", got, name)
		}
	}
}

// TestCreateArchiveFromDirOutputInsideModuleDir checks that an --output file
// written inside the module directory is not nested into the next archive.
func TestCreateArchiveFromDirOutputInsideModuleDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeModuleBase(t, dir)
	out := filepath.Join(dir, "demo.tar.gz")
	if _, err := ExportArchiveToFile(dir, out, PackageLimits{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportArchiveToFile(dir, out, PackageLimits{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range archiveNames(t, data) {
		if name == "demo.tar.gz" {
			t.Fatal("previous bundle nested into the new archive")
		}
	}
}
