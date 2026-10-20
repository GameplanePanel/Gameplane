package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func i18nModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"module.yaml":   "name: demo\ndisplayName: Demo\n",
		"template.yaml": "spec:\n  displayName: Demo Game\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestI18nCommandUsage(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if got := i18nCommand(nil, &out, &errb); got != 2 {
		t.Errorf("no args exit = %d", got)
	}
	errb.Reset()
	if got := i18nCommand([]string{"--help"}, &out, &errb); got != 0 || !strings.Contains(errb.String(), "extract") {
		t.Errorf("help exit = %d: %s", got, errb.String())
	}
	if got := i18nCommand([]string{"nope"}, &out, &errb); got != 2 {
		t.Errorf("unknown subcommand exit = %d", got)
	}
}

func TestI18nExtractWritesDefaultFile(t *testing.T) {
	t.Parallel()
	dir := i18nModule(t)
	var out, errb bytes.Buffer
	if got := i18nCommand([]string{"extract", dir}, &out, &errb); got != 0 {
		t.Fatalf("exit = %d: %s", got, errb.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "locales", "en.json"))
	if err != nil || !strings.Contains(string(data), "template.displayName") {
		t.Fatalf("en.json: %v %s", err, data)
	}
}

func TestI18nExtractOutputFlagForms(t *testing.T) {
	t.Parallel()
	dir := i18nModule(t)
	for _, args := range [][]string{
		{"extract", "--output", filepath.Join(dir, "a.json"), dir},
		{"extract", dir, "--output=" + filepath.Join(dir, "b.json")},
	} {
		var out, errb bytes.Buffer
		if got := i18nCommand(args, &out, &errb); got != 0 {
			t.Fatalf("%v exit = %d: %s", args, got, errb.String())
		}
	}
	for _, name := range []string{"a.json", "b.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
}

func TestI18nExtractErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"extract", "--output"}, 2},
		{[]string{"extract", "--bogus"}, 2},
		{[]string{"extract", "a", "b"}, 2},
		{[]string{"extract", filepath.Join(t.TempDir(), "empty")}, 1},
		{[]string{"extract", "-h"}, 0},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		if got := i18nCommand(c.args, &out, &errb); got != c.want {
			t.Errorf("%v exit = %d, want %d (%s)", c.args, got, c.want, errb.String())
		}
	}
}
