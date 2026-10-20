package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestI18nStampCommand(t *testing.T) {
	t.Parallel()
	dir := i18nModule(t)
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o750); err != nil {
		t.Fatal(err)
	}
	fr := filepath.Join(dir, "locales", "fr.json")
	if err := os.WriteFile(fr, []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := i18nCommand([]string{"stamp", dir}, &out, &errb); got != 0 || !strings.Contains(out.String(), "stamped") {
		t.Fatalf("exit = %d out=%q err=%q", got, out.String(), errb.String())
	}
	if data, _ := os.ReadFile(fr); !strings.Contains(string(data), "sourceHash") {
		t.Errorf("fr.json = %s", data)
	}
	out.Reset()
	if got := i18nCommand([]string{"stamp", dir}, &out, &errb); got != 0 || !strings.Contains(out.String(), "nothing to stamp") {
		t.Fatalf("second run exit = %d out=%q", got, out.String())
	}
}

func TestI18nStampUsageErrors(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if got := i18nCommand([]string{"stamp", "--nope"}, &out, &errb); got != 2 {
		t.Errorf("unknown option exit = %d", got)
	}
	if got := i18nCommand([]string{"stamp", "a", "b"}, &out, &errb); got != 2 {
		t.Errorf("two paths exit = %d", got)
	}
	if got := i18nCommand([]string{"stamp", filepath.Join(t.TempDir(), "missing")}, &out, &errb); got != 1 {
		t.Errorf("missing module exit = %d", got)
	}
}
