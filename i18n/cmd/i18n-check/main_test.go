package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func metaJSON(tag, direction string) string {
	return fmt.Sprintf(`{"tag":%q,"nativeName":"N-%s","englishName":"E-%s","direction":%q}`, tag, tag, tag, direction)
}

// appJSON returns n reference-style entries k00..k(n-1), each with a note.
func appJSON(t *testing.T, n int) string {
	t.Helper()
	m := map[string]map[string]string{}
	for i := range n {
		m[fmt.Sprintf("k%02d", i)] = map[string]string{
			"defaultMessage": fmt.Sprintf("v%d", i),
			"description":    "note",
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"en/app.json":   `{"a":{"defaultMessage":"A","description":"d"}}`,
		"template.yaml": "id: x\n",
	})
	tpl := filepath.Join(dir, "template.yaml")
	missing := filepath.Join(t.TempDir(), "missing")
	out := filepath.Join(t.TempDir(), "manifest.json")

	tests := []struct {
		name string
		args []string
	}{
		{"no locales dir", nil},
		{"two positional args", []string{dir, dir}},
		{"unknown flag", []string{"--bogus", dir}},
		{"sync without registry", []string{"--sync-server", dir}},
		{"check without registry", []string{"--check-server", dir}},
		{"unreadable reference", []string{missing}},
		{"unreadable registry", []string{"--check-server", "--registry", missing, dir}},
		{"unreadable used ids", []string{"--used-ids", missing, dir}},
		{"unreadable extracted", []string{"--extracted", missing, dir}},
		{"module with manifest", []string{"--module", tpl, "--manifest", out, dir}},
		{"module with stamp", []string{"--module", tpl, "--stamp", dir}},
		{"module with sync", []string{"--module", tpl, "--sync-server", "--registry", tpl, dir}},
		{"module missing template", []string{"--module", missing, dir}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := run(tc.args, &stdout, &stderr); got != 2 {
				t.Errorf("exit = %d; want 2 (stderr: %s)", got, stderr.String())
			}
			if stderr.Len() == 0 {
				t.Error("usage error printed nothing to stderr")
			}
		})
	}
}

func TestMalformedReferenceReported(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"en/app.json": `{"a":`})
	var stdout, stderr bytes.Buffer
	if got := run([]string{root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1", got)
	}
	if !strings.Contains(stdout.String(), "[malformed-pack]") {
		t.Errorf("stdout = %q; want a malformed-pack finding", stdout.String())
	}
}

func TestManifestShapeAndOrder(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A","description":"d"},"b":{"defaultMessage":"B","description":"d"}}`,
		"en/meta.json": metaJSON("en", "ltr"),
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"},"b":{"defaultMessage":"Bfr"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
		"de/app.json":  `{"a":{"defaultMessage":"Ade"}}`,
		"de/meta.json": metaJSON("de", "ltr"),
		"ar/app.json":  `{"a":{"defaultMessage":"Aar"},"b":{"defaultMessage":"Bar"}}`,
		"ar/meta.json": metaJSON("ar", "rtl"),
		"xx/app.json":  `{"a":{"defaultMessage":"Axx"}}`,
	})
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	var stdout, stderr bytes.Buffer
	// Exit 1 is expected: xx has no meta.json, which is an error-severity
	// invalid-meta finding. xx is still skipped from the manifest.
	if got := run([]string{"--manifest", manifest, root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 for xx without meta.json (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}

	data := readFile(t, manifest)
	if !strings.HasSuffix(data, "]\n") {
		t.Errorf("manifest does not end with a newline: %q", data)
	}
	var rows []manifestRow
	if err := json.Unmarshal([]byte(data), &rows); err != nil {
		t.Fatal(err)
	}
	want := []manifestRow{
		{Tag: "en", NativeName: "N-en", EnglishName: "E-en", Direction: "ltr", Completeness: 100, Listed: true},
		{Tag: "ar", NativeName: "N-ar", EnglishName: "E-ar", Direction: "rtl", Completeness: 100, Listed: false},
		{Tag: "de", NativeName: "N-de", EnglishName: "E-de", Direction: "ltr", Completeness: 50, Listed: false},
		{Tag: "fr", NativeName: "N-fr", EnglishName: "E-fr", Direction: "ltr", Completeness: 100, Listed: true},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("manifest rows =\n%+v\nwant\n%+v", rows, want)
	}
}

func TestSummaryShowsCompletenessChange(t *testing.T) {
	base := t.TempDir()
	writeTree(t, base, map[string]string{
		"en/app.json": appJSON(t, 20),
		"fr/app.json": appJSON(t, 8),
	})
	cur := t.TempDir()
	writeTree(t, cur, map[string]string{
		"en/app.json":  appJSON(t, 20),
		"en/meta.json": metaJSON("en", "ltr"),
		"fr/app.json":  appJSON(t, 17),
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	summary := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summary, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if got := run([]string{"--base", base, "--summary", summary, cur}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d; want 0 (stdout: %s)", got, stdout.String())
	}
	got := readFile(t, summary)
	for _, want := range []string{"existing\n", "### i18n-check", "0 error(s), 0 notice(s)", "| fr | 40% → 85% | yes |"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q:\n%s", want, got)
		}
	}
}

func TestStampWritesSourceHash(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A","description":"d"}}`,
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--stamp", root}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d; want 0 (stdout: %s)", got, stdout.String())
	}
	entries, err := pack.ParseEntries([]byte(readFile(t, filepath.Join(root, "fr", "app.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if want := pack.SourceHash("A"); entries["a"].SourceHash != want {
		t.Errorf("sourceHash = %q; want %q", entries["a"].SourceHash, want)
	}
	if strings.Contains(readFile(t, filepath.Join(root, "en", "app.json")), "sourceHash") {
		t.Error("reference was stamped")
	}
}

func TestSyncServerWritesRegistry(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"},"server.old":{"defaultMessage":"Old","description":"x"}}`,
	})
	registry := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registry, []byte(`[{"id":"server.new","english":"New","description":"Shown"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--sync-server", "--registry", registry, root}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d; want 0 (stderr: %s)", got, stderr.String())
	}
	entries, err := pack.ParseEntries([]byte(readFile(t, filepath.Join(root, "en", "app.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entries["server.old"]; ok {
		t.Error("server.old survived the sync")
	}
	if got := entries["server.new"]; got.DefaultMessage != "New" || got.Description != "Shown" {
		t.Errorf("server.new = %+v; want registry values", got)
	}
	if _, ok := entries["a"]; !ok {
		t.Error("non-server entry was dropped by the sync")
	}
}

func TestCheckServerRegistryAndReferenceOnlyIDs(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"},"server.only-ref":{"defaultMessage":"R","description":"d"}}`,
	})
	registry := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registry, []byte(`[{"id":"server.only-reg","english":"G","description":"d"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--check-server", "--registry", registry, root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
	for _, want := range []string{"server.only-ref: [server-drift]", "server.only-reg: [server-drift]"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestExtractedUnparseableReported(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"}}`,
	})
	extracted := filepath.Join(t.TempDir(), "extracted.json")
	if err := os.WriteFile(extracted, []byte(`{"a":`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--extracted", extracted, root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[extract-drift]") {
		t.Errorf("stdout = %q; want an extract-drift finding", stdout.String())
	}
}

func TestExtractedMissingAndExtraIDs(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"},"b":{"defaultMessage":"B","description":"d"}}`,
	})
	extracted := filepath.Join(t.TempDir(), "extracted.json")
	if err := os.WriteFile(extracted, []byte(`{"a":{"defaultMessage":"A","description":"d"},"c":{"defaultMessage":"C","description":"d"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--extracted", extracted, root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
	for _, want := range []string{": b: [extract-drift]", ": c: [extract-drift]"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), ": a: [extract-drift]") {
		t.Errorf("matching id a was reported:\n%s", stdout.String())
	}
}

func TestSummaryUnwritablePathIsUsageError(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"}}`,
	})
	summary := filepath.Join(t.TempDir(), "missing-dir", "summary.md")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--summary", summary, root}, &stdout, &stderr); got != 2 {
		t.Fatalf("exit = %d; want 2 (stdout: %s)", got, stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("unwritable summary printed nothing to stderr")
	}
}

func TestModulePacks(t *testing.T) {
	tests := []struct {
		name string
		fr   string
		want int
	}{
		{"clean", `{"template.displayName":{"defaultMessage":"Afr"}}`, 0},
		{"placeholder mismatch", `{"template.displayName":{"defaultMessage":"{x}"}}`, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, map[string]string{
				"en.json": `{"template.displayName":{"defaultMessage":"A","description":"d"}}`,
				"fr.json": tc.fr,
			})
			tpl := filepath.Join(t.TempDir(), "template.yaml")
			if err := os.WriteFile(tpl, []byte("spec:\n  displayName: A\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if got := run([]string{"--module", tpl, root}, &stdout, &stderr); got != tc.want {
				t.Errorf("exit = %d; want %d (stdout: %s)", got, tc.want, stdout.String())
			}
		})
	}
}
