package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const derivedTemplate = `apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
spec:
  displayName: Template
  configSchema:
    - name: MAX_PLAYERS
      displayName: Max players
`

func runModuleCheck(t *testing.T, files map[string]string, template, moduleYAML string) (int, string) {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, files)
	modDir := t.TempDir()
	tpl := filepath.Join(modDir, "template.yaml")
	if err := os.WriteFile(tpl, []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	if moduleYAML != "" {
		if err := os.WriteFile(filepath.Join(modDir, "module.yaml"), []byte(moduleYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--module", tpl, root}, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func derivedReference() string {
	return `{
  "template.displayName": {"defaultMessage": "Template", "description": "d"},
  "config.MAX_PLAYERS.displayName": {"defaultMessage": "Max players", "description": "d"},
  "module.summary": {"defaultMessage": "Sum", "description": "d"}
}`
}

func TestModuleDerivedReferenceMatches(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
		"fr.json": `{"template.displayName":{"defaultMessage":"Modèle"}}`,
	}, derivedTemplate, "displayName: Mod\nsummary: Sum\n")
	// module.displayName is derived too, so the reference is missing it.
	if code != 1 || !strings.Contains(out, "module.displayName") {
		t.Fatalf("exit = %d, want 1 naming module.displayName: %s", code, out)
	}
}

func TestModuleDerivedReferenceClean(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
		"fr.json": `{"template.displayName":{"defaultMessage":"Modèle"}}`,
	}, derivedTemplate, "summary: Sum\n")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleUnknownReferenceID(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": strings.Replace(derivedReference(), "}\n}", "},\n  \"stale.id\": {\"defaultMessage\": \"x\", \"description\": \"d\"}\n}", 1),
	}, derivedTemplate, "summary: Sum\n")
	if code != 1 || !strings.Contains(out, "stale.id") || !strings.Contains(out, "unknown-id") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleChangedReferenceText(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": strings.Replace(derivedReference(), `"Template"`, `"Changed"`, 1),
	}, derivedTemplate, "summary: Sum\n")
	if code != 1 || !strings.Contains(out, "extract-drift") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleTranslationUsesDerivedReference(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
		"fr.json": `{"template.displayName":{"defaultMessage":"{extra}"},"nope":{"defaultMessage":"x"}}`,
	}, derivedTemplate, "summary: Sum\n")
	if code != 1 || !strings.Contains(out, "extra-placeholder") || !strings.Contains(out, "unknown-id") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleInvalidTemplateYAML(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
	}, "spec: [unterminated\n", "")
	if code != 2 || !strings.Contains(out, "parse template") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleInvalidModuleYAML(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
	}, derivedTemplate, "summary: [unterminated\n")
	if code != 2 || !strings.Contains(out, "parse module.yaml") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleReferenceWithIDsButTemplateDerivesNone(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": `{"template.displayName": {"defaultMessage": "Template", "description": "d"}}`,
	}, "spec: {}\n", "")
	if code != 1 || !strings.Contains(out, "unknown-id") || !strings.Contains(out, "template.displayName") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleEmptyReferenceAndEmptyTemplate(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{"en.json": `{}`}, "spec: {}\n", "")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
}

func TestModuleInvalidTranslationFileNames(t *testing.T) {
	for _, name := range []string{"xx_YY.json", "Fr.json"} {
		code, out := runModuleCheck(t, map[string]string{
			"en.json": derivedReference(),
			name:      `{}`,
		}, derivedTemplate, "summary: Sum\n")
		if code != 1 || !strings.Contains(out, name) || !strings.Contains(out, "malformed-pack") {
			t.Fatalf("%s: exit = %d: %s", name, code, out)
		}
	}
}

func TestModuleTranslationUsingIDOnlyInReferenceIsUnknown(t *testing.T) {
	code, out := runModuleCheck(t, map[string]string{
		"en.json": derivedReference(),
		"fr.json": `{"only.in.en":{"defaultMessage":"x"}}`,
	}, derivedTemplate, "summary: Sum\n")
	if code != 1 || !strings.Contains(out, "unknown-id") || !strings.Contains(out, "only.in.en") {
		t.Fatalf("exit = %d: %s", code, out)
	}
}
