package packager

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeLocale(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "locales", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadLocaleFilesMissingDir(t *testing.T) {
	t.Parallel()
	got, err := ReadLocaleFiles(t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestReadLocaleFilesSkipsEnglishAndNonJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLocale(t, dir, "en.json", "{}")
	writeLocale(t, dir, "fr.json", `{"a":1}`)
	writeLocale(t, dir, "pt-BR.json", "{}")
	writeLocale(t, dir, "README.txt", "x")
	if err := os.MkdirAll(filepath.Join(dir, "locales", "sub.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLocaleFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got["locales/fr.json"]) != `{"a":1}` || got["locales/pt-BR.json"] == nil {
		t.Fatalf("got %v", got)
	}
}

func TestReadLocaleFilesRejectsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLocale(t, dir, "real.txt", "{}")
	if err := os.Symlink(filepath.Join(dir, "locales", "real.txt"), filepath.Join(dir, "locales", "fr.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadLocaleFiles(dir); err == nil {
		t.Fatal("expected an error for a symlinked locale file")
	}
}

func TestReadLocaleFilesRejectsBadTag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLocale(t, dir, "French.json", "{}")
	if _, err := ReadLocaleFiles(dir); err == nil {
		t.Fatal("expected an error for an invalid language tag")
	}
}

func TestLocaleLayerArgs(t *testing.T) {
	t.Parallel()
	if got := localeLayerArgs(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	got := localeLayerArgs(map[string][]byte{"locales/fr.json": nil, "locales/de.json": nil})
	want := []string{
		"locales/de.json:" + MediaLocale,
		"locales/fr.json:" + MediaLocale,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
