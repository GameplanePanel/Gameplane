package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// hasRule reports whether any problem carries rule.
func hasRule(probs []themepkg.Problem, rule string) bool {
	for _, p := range probs {
		if p.Rule == rule {
			return true
		}
	}
	return false
}

// badFixtures pairs each themepkg bad fixture with the rule it must trip. Two
// of them are too large to commit, so their files are extended in the test, the
// same way themepkg's own tests build them.
var badFixtures = []struct {
	dir    string
	rule   string
	extend func(files map[string][]byte)
}{
	{dir: "schema", rule: themepkg.RuleSchema},
	{dir: "tokens-names", rule: themepkg.RuleTokensNames},
	{dir: "tokens-format", rule: themepkg.RuleTokensFormat},
	{dir: "css-size", rule: themepkg.RuleCSSSize, extend: func(f map[string][]byte) {
		f["theme.css"] = []byte(strings.Repeat("a{}\n", 8200))
	}},
	{dir: "css-import", rule: themepkg.RuleCSSImport},
	{dir: "css-url", rule: themepkg.RuleCSSURL},
	{dir: "css-remote", rule: themepkg.RuleCSSRemote},
	{dir: "css-syntax", rule: themepkg.RuleCSSSyntax},
	{dir: "preview-missing", rule: themepkg.RulePreviewMissing},
	{dir: "preview-format", rule: themepkg.RulePreviewFormat},
	{dir: "files-extra", rule: themepkg.RuleFilesExtra},
	{dir: "size-total", rule: themepkg.RuleSizeTotal, extend: func(f map[string][]byte) {
		f["padding.bin"] = make([]byte, themepkg.MaxPackageBytes)
	}},
}

func TestValidateGoodFixture(t *testing.T) {
	pkg, err := Inspect(filepath.Join(themepkgTestdata, "good"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !pkg.Valid() {
		t.Fatalf("good fixture reported problems: %v", pkg.Problems)
	}
	rep := pkg.Report()
	if !rep.Valid || rep.Name != "deep-ocean" || len(rep.Modes) != 1 || rep.Modes[0] != "dark" {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(rep.Human(), ": valid") {
		t.Fatalf("human report does not say valid:\n%s", rep.Human())
	}
}

func TestValidateBadFixturesTripTheirRule(t *testing.T) {
	for _, tt := range badFixtures {
		t.Run(tt.dir, func(t *testing.T) {
			files, err := Load(filepath.Join(themepkgTestdata, "bad", tt.dir))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if tt.extend != nil {
				tt.extend(files)
			}
			_, probs := themepkg.Validate(files)
			if !hasRule(probs, tt.rule) {
				t.Fatalf("expected rule %s, got %v", tt.rule, probs)
			}
		})
	}
}

func TestInspectBadFixtureIsInvalidWithJSON(t *testing.T) {
	pkg, err := Inspect(filepath.Join(themepkgTestdata, "bad", "schema"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if pkg.Valid() {
		t.Fatalf("schema fixture reported valid")
	}
	rep := pkg.Report()
	if rep.Valid || !strings.Contains(rep.Human(), ": invalid (") {
		t.Fatalf("report does not say invalid:\n%s", rep.Human())
	}
	data, err := rep.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode report JSON: %v", err)
	}
	if decoded.Valid || len(decoded.Problems) == 0 || decoded.Problems[0].Rule != themepkg.RuleSchema {
		t.Fatalf("decoded report = %+v", decoded)
	}
}

func TestReportJSONKeepsEmptyArrays(t *testing.T) {
	pkg, err := Inspect(filepath.Join(themepkgTestdata, "good"))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	data, err := pkg.Report().JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(data), `"problems": []`) || !strings.Contains(string(data), `"warnings": []`) {
		t.Fatalf("empty lists must encode as [] for CI consumers:\n%s", data)
	}
}

func TestReportWarnsOnLowContrastWithoutFailing(t *testing.T) {
	p := &Package{Dir: "x", Result: themepkg.Result{
		LowContrast: true,
		Manifest:    themepkg.Manifest{Name: "x", Version: "1.0.0"},
	}}
	rep := p.Report()
	if !rep.Valid || len(rep.Warnings) != 1 || rep.Warnings[0].Rule != "lowContrast" {
		t.Fatalf("report = %+v, want valid with one lowContrast warning", rep)
	}
}

func TestLoadRefusesSymbolicLinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside.css")
	writeFile(t, target, []byte("a{}"))
	if err := os.Symlink(target, filepath.Join(dir, "theme.css")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Load with a symbolic link: err = %v", err)
	}
}

func TestLoadRefusesMissingFolderAndFiles(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatalf("Load of a missing folder succeeded")
	}
	file := filepath.Join(t.TempDir(), "theme.yaml")
	writeFile(t, file, []byte("name: x\n"))
	if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("Load of a file: err = %v", err)
	}
}

func TestLoadKeysUseSlashPaths(t *testing.T) {
	dir := buildValidPackage(t, t.TempDir())
	files, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"theme.yaml", "theme.css", "previews/dark.png"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("Load is missing %s; keys = %v", want, keysOf(files))
		}
	}
}

// keysOf lists the keys of a package map for failure messages.
func keysOf(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	return out
}
