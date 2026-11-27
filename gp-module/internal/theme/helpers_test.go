package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// themepkgTestdata is the shared theme fixture tree. The theme CLI and the
// themepkg module are checked out side by side, so the fixtures are read from
// there rather than copied.
const themepkgTestdata = "../../../themepkg/testdata"

// sampleExport is a spec 016 gameplane-theme v1 export with custom colors on a
// dark surface and an enabled custom CSS overlay.
const sampleExport = `{
  "format": "gameplane-theme",
  "version": 1,
  "preferences": {
    "themeType": "custom_colors",
    "presetId": "legacy",
    "appearanceMode": "dark",
    "customColors": {"accent": "#38BDF8", "surface": "#0B1220"},
    "customCssEnabled": true,
    "customCss": ".card { border-radius: 12px; }"
  }
}`

// writeFile writes content to path, creating parent folders.
func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFile returns the content of path, failing the test on error.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// tokensEqual reports whether two token maps hold the same name/value pairs.
func tokensEqual(a, b themepkg.Tokens) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// buildValidPackage writes a package that passes every rule: it comes from a
// sample export, gets its author and license filled in and a dark preview copied
// from the shared fixtures. It returns the package folder.
func buildValidPackage(t *testing.T, root string) string {
	t.Helper()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(sampleExport))
	dir := filepath.Join(root, "deep-ocean")
	if _, err := FromExport(exportPath, dir, "deep-ocean", "1.2.0"); err != nil {
		t.Fatalf("FromExport: %v", err)
	}
	manifestPath := filepath.Join(dir, "theme.yaml")
	filled := strings.Replace(string(readFile(t, manifestPath)), `author: ""`, `author: "Jane Doe"`, 1)
	filled = strings.Replace(filled, `license: ""`, "license: MIT", 1)
	if !strings.Contains(filled, `author: "Jane Doe"`) || !strings.Contains(filled, "license: MIT") {
		t.Fatalf("theme.yaml from FromExport lacks the author or license line:\n%s", filled)
	}
	writeFile(t, manifestPath, []byte(filled))
	preview := readFile(t, filepath.Join(themepkgTestdata, "good", "previews", "dark.png"))
	writeFile(t, filepath.Join(dir, "previews", "dark.png"), preview)
	return dir
}
