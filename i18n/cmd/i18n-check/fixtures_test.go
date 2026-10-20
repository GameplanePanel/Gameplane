package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// allCodes lists every finding code; each has a fixture under testdata/defective.
var allCodes = []string{
	"malformed-pack",
	"invalid-meta",
	"icu-syntax",
	"missing-placeholder",
	"extra-placeholder",
	"markup",
	"unknown-id",
	"missing-description",
	"extract-drift",
	"server-drift",
	"unused-id",
	"needs-review",
}

var codeRE = regexp.MustCompile(`\[([a-z-]+)\]`)

func TestDefectiveFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "defective")
	for _, code := range allCodes {
		if _, err := os.Stat(filepath.Join(root, code)); err != nil {
			t.Errorf("fixture for %s: %v", code, err)
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, name)
			var args []string
			raw, err := os.ReadFile(filepath.Join(dir, "args"))
			switch {
			case err == nil:
				for _, a := range strings.Fields(string(raw)) {
					args = append(args, strings.ReplaceAll(a, "{dir}", dir))
				}
			case !errors.Is(err, fs.ErrNotExist):
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := run(append(args, dir), &stdout, &stderr)

			got := map[string]struct{}{}
			for _, m := range codeRE.FindAllStringSubmatch(stdout.String(), -1) {
				got[m[1]] = struct{}{}
			}
			if len(got) != 1 {
				t.Fatalf("codes = %v; want exactly {%s}\nstdout:\n%s", keys(got), name, stdout.String())
			}
			if _, ok := got[name]; !ok {
				t.Errorf("codes = %v; want {%s}\nstdout:\n%s", keys(got), name, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("unexpected stderr: %s", stderr.String())
			}

			want := 1
			if name == "needs-review" {
				want = 0
			}
			if code != want {
				t.Errorf("exit = %d; want %d", code, want)
			}
		})
	}
}

func keys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}
