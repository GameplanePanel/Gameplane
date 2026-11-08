package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

func TestStaleReviewHashNeedsReview(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A","description":"d"}}`,
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	writeTree(t, root, map[string]string{"fr/" + pack.ReviewFile: `{"a":"000000000000"}`})
	var stdout, stderr bytes.Buffer
	if got := run([]string{root}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d; want 0 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[needs-review]") {
		t.Errorf("output missing needs-review:\n%s", stdout.String())
	}
}

func TestStampMovesInlineSourceHashToReviewFile(t *testing.T) {
	root := t.TempDir()
	want := pack.SourceHash("A")
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A","description":"d"}}`,
		"fr/app.json":  `{"a":{"defaultMessage":"Afr","sourceHash":"` + want + `"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--stamp", root}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d; want 0 (stdout: %s, stderr: %s)", got, stdout.String(), stderr.String())
	}
	if strings.Contains(readFile(t, filepath.Join(root, "fr", "app.json")), "sourceHash") {
		t.Error("fr/app.json still carries sourceHash after --stamp")
	}
	hashes, err := pack.ParseReview([]byte(readFile(t, filepath.Join(root, "fr", pack.ReviewFile))))
	if err != nil {
		t.Fatal(err)
	}
	if hashes["a"] != want {
		t.Errorf("review hash = %q; want %q", hashes["a"], want)
	}
}

func TestMalformedReviewFileReported(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"en/app.json":  `{"a":{"defaultMessage":"A","description":"d"}}`,
		"fr/app.json":  `{"a":{"defaultMessage":"Afr"}}`,
		"fr/meta.json": metaJSON("fr", "ltr"),
	})
	writeTree(t, root, map[string]string{"fr/" + pack.ReviewFile: `[`})
	var stdout, stderr bytes.Buffer
	if got := run([]string{root}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit = %d; want 1 (stdout: %s)", got, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "[malformed-pack]") || !strings.Contains(out, "fr/"+pack.ReviewFile) {
		t.Errorf("output missing malformed-pack finding for fr/%s:\n%s", pack.ReviewFile, out)
	}
}

func TestModuleReviewClearsNoTagFinding(t *testing.T) {
	files := map[string]string{
		"en.json": derivedReference(),
		"fr.json": `{"template.displayName":{"defaultMessage":"Modèle"}}`,
	}
	files[pack.ReviewFile] = `{"fr":{"template.displayName":"000000000000"}}`
	code, out := runModuleCheck(t, files, derivedTemplate, "summary: Sum\n")
	if code != 0 {
		t.Fatalf("exit = %d; want 0 (info only): %s", code, out)
	}
	if !strings.Contains(out, "[needs-review]") {
		t.Errorf("output missing needs-review:\n%s", out)
	}
	if strings.Contains(out, "not a valid language tag") {
		t.Errorf("review file reported as a translation:\n%s", out)
	}
}

func TestModuleMalformedReviewReported(t *testing.T) {
	files := map[string]string{"en.json": derivedReference()}
	files[pack.ReviewFile] = `{"not a tag":{}}`
	code, out := runModuleCheck(t, files, derivedTemplate, "summary: Sum\n")
	if code != 1 || !strings.Contains(out, "[malformed-pack]") || !strings.Contains(out, pack.ReviewFile) {
		t.Fatalf("exit = %d; want 1 with malformed-pack for %s: %s", code, pack.ReviewFile, out)
	}
}
