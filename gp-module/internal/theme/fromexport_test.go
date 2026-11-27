package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/themepkg"
)

func TestFromExportDarkCustomColors(t *testing.T) {
	root := t.TempDir()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(sampleExport))
	dir := filepath.Join(root, "deep-ocean")

	res, err := FromExport(exportPath, dir, "deep-ocean", "1.2.0")
	if err != nil {
		t.Fatalf("FromExport: %v", err)
	}
	if res.Mode != "dark" || !res.WroteCSS || len(res.Notes) != 0 {
		t.Fatalf("result = %+v, want dark mode, CSS written, no notes", res)
	}
	m, probs := themepkg.DecodeManifest(readFile(t, filepath.Join(dir, "theme.yaml")))
	requireOnlyMetadataProblems(t, probs)
	if m.Version != "1.2.0" || m.DisplayName != "Deep Ocean" {
		t.Fatalf("identity = %q %q", m.Version, m.DisplayName)
	}
	if m.Tokens.Light != nil {
		t.Fatalf("a dark surface must not write light tokens")
	}
	want, err := themepkg.Derive("#38BDF8", "#0B1220")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !tokensEqual(m.Tokens.Dark, want) {
		t.Fatalf("dark tokens differ from themepkg.Derive output")
	}
	if got := string(readFile(t, filepath.Join(dir, "theme.css"))); got != ".card { border-radius: 12px; }" {
		t.Fatalf("theme.css = %q", got)
	}
}

func TestFromExportLightSurfaceWritesLightMode(t *testing.T) {
	root := t.TempDir()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(`{"format":"gameplane-theme","version":1,"preferences":{"themeType":"custom_colors","presetId":"pink","appearanceMode":"light","customColors":{"accent":"#db2777","surface":"#fff7fb"},"customCssEnabled":false,"customCss":null}}`))
	dir := filepath.Join(root, "soft")

	res, err := FromExport(exportPath, dir, "soft", "")
	if err != nil {
		t.Fatalf("FromExport: %v", err)
	}
	if res.Mode != "light" || res.WroteCSS {
		t.Fatalf("result = %+v, want light mode and no CSS", res)
	}
	m, _ := themepkg.DecodeManifest(readFile(t, filepath.Join(dir, "theme.yaml")))
	if m.Version != DefaultVersion {
		t.Fatalf("empty version should default to %s, got %q", DefaultVersion, m.Version)
	}
	if m.Tokens.Dark != nil || m.Tokens.Light == nil {
		t.Fatalf("expected light tokens only, got %v", m.Modes())
	}
}

func TestFromExportDisabledCSSIsNoted(t *testing.T) {
	root := t.TempDir()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(strings.Replace(sampleExport, `"customCssEnabled": true`, `"customCssEnabled": false`, 1)))
	dir := filepath.Join(root, "quiet")

	res, err := FromExport(exportPath, dir, "quiet", "0.1.0")
	if err != nil {
		t.Fatalf("FromExport: %v", err)
	}
	if res.WroteCSS || len(res.Notes) != 1 {
		t.Fatalf("result = %+v, want no CSS and one note", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "theme.css")); !os.IsNotExist(err) {
		t.Fatalf("theme.css exists although the overlay is disabled (stat err = %v)", err)
	}
}

func TestFromExportRefusesBadExports(t *testing.T) {
	cases := map[string]string{
		"not json":        `{"format":`,
		"wrong format":    `{"format":"other","version":1,"preferences":{}}`,
		"newer version":   `{"format":"gameplane-theme","version":2,"preferences":{}}`,
		"preset export":   `{"format":"gameplane-theme","version":1,"preferences":{"themeType":"preset","presetId":"pink","appearanceMode":"dark","customCssEnabled":false}}`,
		"no colors":       `{"format":"gameplane-theme","version":1,"preferences":{"themeType":"custom_colors","presetId":"pink","appearanceMode":"dark","customCssEnabled":false}}`,
		"bad accent":      `{"format":"gameplane-theme","version":1,"preferences":{"themeType":"custom_colors","presetId":"pink","appearanceMode":"dark","customColors":{"accent":"blue","surface":"#0b1220"},"customCssEnabled":false}}`,
		"bad surface hex": `{"format":"gameplane-theme","version":1,"preferences":{"themeType":"custom_colors","presetId":"pink","appearanceMode":"dark","customColors":{"accent":"#38bdf8","surface":"#0b12zz"},"customCssEnabled":false}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			exportPath := filepath.Join(root, "export.json")
			writeFile(t, exportPath, []byte(body))
			if _, err := FromExport(exportPath, filepath.Join(root, "out"), "out", ""); err == nil {
				t.Fatalf("FromExport(%s) succeeded, want error", name)
			}
		})
	}
}

func TestFromExportRequiresName(t *testing.T) {
	root := t.TempDir()
	exportPath := filepath.Join(root, "export.json")
	writeFile(t, exportPath, []byte(sampleExport))
	if _, err := FromExport(exportPath, filepath.Join(root, "x"), "", ""); err == nil {
		t.Fatalf("FromExport without a name succeeded, want error")
	}
}

func TestSurfaceIsDark(t *testing.T) {
	cases := map[string]bool{
		"#0b1220": true,
		"#121114": true,
		"#f8fafc": false,
		"#ffffff": false,
		"#000000": true,
	}
	for hex, want := range cases {
		got, err := surfaceIsDark(hex)
		if err != nil {
			t.Fatalf("surfaceIsDark(%s): %v", hex, err)
		}
		if got != want {
			t.Errorf("surfaceIsDark(%s) = %v, want %v", hex, got, want)
		}
	}
	if _, err := surfaceIsDark("#12345"); err == nil {
		t.Fatalf("surfaceIsDark accepted a short hex value")
	}
}
