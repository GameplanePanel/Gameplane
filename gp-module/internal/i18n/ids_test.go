package i18n

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

func writeModule(t *testing.T, module, template string) string {
	t.Helper()
	dir := t.TempDir()
	if module != "" {
		if err := os.WriteFile(filepath.Join(dir, "module.yaml"), []byte(module), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if template != "" {
		if err := os.WriteFile(filepath.Join(dir, "template.yaml"), []byte(template), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	testModule   = "name: demo\ndisplayName: Demo\nsummary: Runs {Demo}\n"
	testTemplate = "spec:\n  displayName: Demo Game\n  configSchema:\n    - name: EULA\n      displayName: Accept EULA\n"
)

func TestExtract(t *testing.T) {
	t.Parallel()
	data, err := Extract(writeModule(t, testModule, testTemplate))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := pack.ParseEntries(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"module.displayName", "module.summary", "template.displayName", "config.EULA.displayName"} {
		e, ok := ref[id]
		if !ok || e.Description == "" {
			t.Errorf("missing %s or description (%+v)", id, e)
		}
	}
	if fs := pack.ValidateReference(ref, "en.json"); len(fs) != 0 {
		t.Errorf("reference invalid: %v", fs)
	}
}

func TestExtractErrors(t *testing.T) {
	t.Parallel()
	if _, err := Extract(writeModule(t, "", testTemplate)); err == nil || !strings.Contains(err.Error(), "module.yaml") {
		t.Errorf("missing module.yaml: %v", err)
	}
	if _, err := Extract(writeModule(t, testModule, "")); err == nil || !strings.Contains(err.Error(), "template.yaml") {
		t.Errorf("missing template.yaml: %v", err)
	}
	if _, err := Extract(writeModule(t, "name: [broken", testTemplate)); err == nil || !strings.Contains(err.Error(), "parse module.yaml") {
		t.Errorf("bad module.yaml: %v", err)
	}
}

func TestWriteExtractDefaultAndCustomOutput(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	got, err := WriteExtract(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "locales", "en.json") {
		t.Errorf("default output = %s", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Error(err)
	}
	custom := filepath.Join(t.TempDir(), "out", "ref.json")
	if _, err := WriteExtract(dir, custom); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Error(err)
	}
}

func TestWriteExtractPropagatesError(t *testing.T) {
	t.Parallel()
	if _, err := WriteExtract(t.TempDir(), ""); err == nil {
		t.Fatal("expected an error for an empty directory")
	}
}

func TestReadYAMLOptional(t *testing.T) {
	t.Parallel()
	got, err := readYAML(filepath.Join(t.TempDir(), "absent.yaml"), false)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}
