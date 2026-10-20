package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// partialPack returns a French-style pack that translates the first translated
// ids of a reference built by appJSON.
func partialPack(t *testing.T, translated int) string {
	t.Helper()
	m := map[string]map[string]string{}
	for i := range translated {
		m[fmt.Sprintf("k%02d", i)] = map[string]string{"defaultMessage": fmt.Sprintf("f%d", i)}
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestManifestListingThreshold(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  appJSON(t, 10),
		"en/meta.json": metaJSON("en", "ltr"),
		"fr/app.json":  partialPack(t, 7), // 70%: below the listing threshold
		"fr/meta.json": metaJSON("fr", "ltr"),
		"de/app.json":  partialPack(t, 9), // 90%: listed
		"de/meta.json": metaJSON("de", "ltr"),
	})
	manifest := filepath.Join(t.TempDir(), "languages.json")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--manifest", manifest, root}, &stdout, &stderr); got != exitOK {
		t.Fatalf("exit = %d; want %d (stdout: %s)", got, exitOK, stdout.String())
	}
	var rows []manifestRow
	if err := json.Unmarshal([]byte(readFile(t, manifest)), &rows); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	completeness := map[string]int{}
	for _, r := range rows {
		listed[r.Tag] = r.Listed
		completeness[r.Tag] = r.Completeness
	}
	if completeness["fr"] != 70 || listed["fr"] {
		t.Errorf("fr: completeness %d listed %v; want 70 and not listed", completeness["fr"], listed["fr"])
	}
	if completeness["de"] != 90 || !listed["de"] {
		t.Errorf("de: completeness %d listed %v; want 90 and listed", completeness["de"], listed["de"])
	}
	if !listed["en"] {
		t.Error("en is not listed")
	}
}

func TestValidOutKeepsUsableEntries(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"Hi {name}"},"b":{"defaultMessage":"Plain"}}`,
		"en/meta.json": metaJSON("en", "ltr"),
		// "a" is usable; "b" adds a placeholder English does not have, and
		// "zz" is an id the reference no longer has.
		"fr/app.json":  `{"a":{"defaultMessage":"Salut {name}"},"b":{"defaultMessage":"Simple {name}"},"zz":{"defaultMessage":"Extra"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	// Findings make the exit status 1 here; only usage errors must not occur.
	if got := run([]string{"--valid-out", out, root}, &stdout, &stderr); got == exitUsage {
		t.Fatalf("exit = %d; want a non-usage status (stderr: %s)", got, stderr.String())
	}
	fr, err := pack.ParseEntries([]byte(readFile(t, filepath.Join(out, "fr", "app.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(fr); len(got) != 1 || got[0] != "a" {
		t.Errorf("fr ids = %v; want [a]", got)
	}
	en, err := pack.ParseEntries([]byte(readFile(t, filepath.Join(out, "en", "app.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedKeys(en); len(got) != 2 {
		t.Errorf("en ids = %v; want the whole reference", got)
	}
}

func TestValidOutIsUsageErrorWithModule(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--module", dir, "--valid-out", dir, dir}, &stdout, &stderr); got != exitUsage {
		t.Errorf("exit = %d; want %d", got, exitUsage)
	}
	if _, err := os.Stat(filepath.Join(dir, "fr")); err == nil {
		t.Error("module mode wrote packs")
	}
}

func sortedKeys(m map[string]pack.Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
