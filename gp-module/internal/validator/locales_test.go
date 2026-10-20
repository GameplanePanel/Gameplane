package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const localeModuleYAML = "name: demo\ndisplayName: Demo\nsummary: A demo\nversion: 0.1.0\n"
const localeTemplateYAML = "spec:\n  displayName: Demo Game\n"

func localeFiles(extra map[string]string) map[string][]byte {
	files := map[string][]byte{
		"module.yaml":   []byte(localeModuleYAML),
		"template.yaml": []byte(localeTemplateYAML),
	}
	for k, v := range extra {
		files[k] = []byte(v)
	}
	return files
}

func localeRuleFindings(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.RuleID == RuleLocaleInvalid {
			out = append(out, f)
		}
	}
	return out
}

func TestValidateLocalesNoneIsUnchanged(t *testing.T) {
	t.Parallel()
	if got := validateLocales(localeFiles(nil)); len(got) != 0 {
		t.Fatalf("findings without locales: %v", got)
	}
}

func TestValidateLocalesValid(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json":    `{"module.displayName":{"defaultMessage":"Démo"},"template.displayName":{"defaultMessage":"Jeu"}}`,
		"locales/pt-BR.json": `{"module.summary":{"defaultMessage":"Uma demo"}}`,
		"locales/en.json":    `{"module.displayName":{"defaultMessage":"Demo","description":"d"}}`,
	})
	if got := validateLocales(files); len(got) != 0 {
		t.Fatalf("unexpected findings: %v", got)
	}
}

func TestValidateLocalesBadPlaceholderAndUnknownID(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/fr.json": `{"module.displayName":{"defaultMessage":"{x}"},"nope":{"defaultMessage":"y"}}`,
	})
	got := validateLocales(files)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %v", got)
	}
	var text strings.Builder
	for _, f := range got {
		if f.Level != SeverityError || f.RuleID != RuleLocaleInvalid || f.File != "locales/fr.json" || f.Remediation == "" {
			t.Errorf("bad finding %+v", f)
		}
		text.WriteString(f.Field + " " + f.Message + "\n")
	}
	for _, want := range []string{"module.displayName", "extra-placeholder", "nope", "unknown-id"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("findings do not mention %q:\n%s", want, text.String())
		}
	}
}

func TestValidateLocalesBadTagAndMalformed(t *testing.T) {
	t.Parallel()
	files := localeFiles(map[string]string{
		"locales/FR_fr.json":      `{}`,
		"locales/de.json":         `{not json`,
		"locales/nested/x/y.json": `{}`,
	})
	got := validateLocales(files)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %v", got)
	}
	if !strings.Contains(got[0].Message, "not a valid language tag") && !strings.Contains(got[1].Message, "not a valid language tag") {
		t.Errorf("missing tag finding: %v", got)
	}
	if !strings.Contains(got[0].Message+got[1].Message, "malformed") {
		t.Errorf("missing malformed finding: %v", got)
	}
}

func TestValidateLocalesFallsBackToEnglishFile(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml":     []byte("name: demo\n"),
		"template.yaml":   []byte("spec: {}\n"),
		"locales/en.json": []byte(`{"custom":{"defaultMessage":"Hi {name}","description":"d"}}`),
		"locales/fr.json": []byte(`{"custom":{"defaultMessage":"Salut"}}`),
	}
	got := validateLocales(files)
	if len(got) != 1 || !strings.Contains(got[0].Message, "missing-placeholder") {
		t.Fatalf("got %v", got)
	}
}

func TestValidateLocalesNoReferenceSkipsContentChecks(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml":     []byte("name: [broken"),
		"template.yaml":   []byte("spec: [broken"),
		"locales/fr.json": []byte(`{"a":{"defaultMessage":"x"}}`),
	}
	if got := validateLocales(files); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestValidateDirectoryReadsLocales(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("module.yaml", localeModuleYAML)
	write("template.yaml", localeTemplateYAML)
	write("README.md", "# demo\n")
	write("locales/fr.json", `{"module.displayName":{"defaultMessage":"{x}"}}`)
	write("locales/notes.txt", "ignored")
	rep, err := ValidateDirectory(dir, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := localeRuleFindings(rep.Findings)
	if len(got) != 1 || got[0].File != "locales/fr.json" {
		t.Fatalf("locale findings = %v", got)
	}
}
