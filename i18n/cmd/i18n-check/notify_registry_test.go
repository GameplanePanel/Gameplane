package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

func TestSyncRegistryCarriesNotifyEntries(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"a":{"defaultMessage":"A","description":"d"},"server.a":{"defaultMessage":"Old","description":"x"},"notify.old":{"defaultMessage":"Stale","description":"x"}}`,
	})
	registry := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registry, []byte(`[{"id":"server.a","english":"A2","description":"d2"},{"id":"notify.b","english":"B","description":"Shown"}]`), 0o644); err != nil {
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
	if got := entries["server.a"]; got.DefaultMessage != "A2" {
		t.Errorf("server.a = %+v; want registry value", got)
	}
	if got := entries["notify.b"]; got.DefaultMessage != "B" || got.Description != "Shown" {
		t.Errorf("notify.b = %+v; want registry values", got)
	}
	if _, ok := entries["notify.old"]; ok {
		t.Error("notify.old survived the sync")
	}
	if _, ok := entries["a"]; !ok {
		t.Error("non-registry entry was dropped by the sync")
	}
}

func TestCheckRegistryReportsStaleNotifyEntry(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json": `{"notify.stale":{"defaultMessage":"S","description":"d"}}`,
	})
	registry := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registry, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--check-server", "--registry", registry, root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
}
