package validator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/gp-module/internal/scaffold"
)

func TestValidate_ValidScaffoldedModule(t *testing.T) {
	tempDir := t.TempDir()
	modDir := filepath.Join(tempDir, "my-game")

	opts := scaffold.Options{
		Name:        "my-game",
		DisplayName: "My Game",
		Archetype:   "steamcmd",
		OutputDir:   modDir,
		Overwrite:   false,
	}

	if _, err := scaffold.Scaffold(opts); err != nil {
		t.Fatalf("failed to scaffold: %v", err)
	}

	report, err := ValidateDirectory(modDir, ValidateOptions{})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if !report.Clean {
		t.Errorf("expected module to be clean, got findings: %+v", report.Findings)
	}
	if len(report.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(report.Findings))
	}
}

func TestValidate_MissingRequiredFiles(t *testing.T) {
	tempDir := t.TempDir()
	modDir := filepath.Join(tempDir, "empty-module")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		t.Fatal(err)
	}

	report, err := ValidateDirectory(modDir, ValidateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Clean {
		t.Errorf("expected module to fail validation")
	}

	missing := make(map[string]bool)
	for _, f := range report.Findings {
		if f.RuleID == RuleMissingRequiredFile {
			missing[f.File] = true
		}
	}

	if !missing["module.yaml"] || !missing["template.yaml"] || !missing["README.md"] {
		t.Errorf("expected missing module.yaml, template.yaml, and README.md, got %+v", missing)
	}
}

func TestValidate_InvalidModuleName(t *testing.T) {
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: INVALID_NAME
displayName: Bad Name
version: 1.0.0
game: bad
summary: Bad test module
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: bad
spec:
  image: "ghcr.io/example/game@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("invalid-dir", files, ValidateOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleInvalidModuleName {
			found = true
			if f.Line != 2 {
				t.Errorf("expected line 2 for invalid module name, got %d", f.Line)
			}
		}
	}
	if !found {
		t.Errorf("expected invalid-module-name error")
	}
}

func TestValidate_ImageUnpinned(t *testing.T) {
	// 1. Unpinned without comment
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "docker.io/mygame/server:latest"
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleImageUnpinned {
			found = true
			if f.Line != 6 {
				t.Errorf("expected line 6 for unpinned image, got %d", f.Line)
			}
		}
	}
	if !found {
		t.Errorf("expected image-unpinned error")
	}

	// 2. Unpinned with # gameplane:floating comment
	files["template.yaml"] = []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "docker.io/mygame/server:latest" # gameplane:floating
`)

	report, err = ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Findings {
		if f.RuleID == RuleImageUnpinned {
			t.Errorf("expected image-unpinned to be suppressed by gameplane:floating comment")
		}
	}
}

func TestValidate_UnrecognizedCategory(t *testing.T) {
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
categories:
  - Survival
  - SpaceOpera
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleUnrecognizedCategory {
			found = true
			if f.Level != SeverityWarn {
				t.Errorf("expected WARN level, got %s", f.Level)
			}
		}
	}
	if !found {
		t.Errorf("expected unrecognized-category warning")
	}
}

func TestValidate_PortChecks(t *testing.T) {
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  ports:
    - name: game
      containerPort: 70000
      protocol: TCP
    - name: query
      containerPort: 27015
      protocol: ICMP
    - name: dup1
      containerPort: 8080
      protocol: TCP
    - name: dup2
      containerPort: 8080
      protocol: TCP
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rulesFound := make(map[string]bool)
	for _, f := range report.Findings {
		rulesFound[f.RuleID] = true
	}

	if !rulesFound[RuleInvalidPortNumber] {
		t.Errorf("expected invalid-port-number error")
	}
	if !rulesFound[RuleInvalidPortProtocol] {
		t.Errorf("expected invalid-port-protocol error")
	}
	if !rulesFound[RuleDuplicatePortCollision] {
		t.Errorf("expected duplicate-port-collision error")
	}
}

func TestValidate_ConfigSchemaAndMemory(t *testing.T) {
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  configSchema:
    - name: SERVER_PASSWORD
      type: string
    - name: INVALID_TYPE_FIELD
      type: float
    - name: MEM_FIELD
      type: string
      autoFromMemoryLimit:
        percent: 150
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rulesFound := make(map[string]bool)
	for _, f := range report.Findings {
		rulesFound[f.RuleID] = true
	}

	if !rulesFound[RuleInsecureAuthField] {
		t.Errorf("missing expected rule %s", RuleInsecureAuthField)
	}
	if !rulesFound[RuleInvalidConfigType] {
		t.Errorf("expected invalid-config-type error")
	}
	if !rulesFound[RuleInvalidMemoryPercent] {
		t.Errorf("expected invalid-memory-percent error")
	}
}

func TestValidate_EnumFieldUsesEnumKey(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-enum-mod
displayName: Test Enum Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-enum-mod
spec:
  displayName: Test Enum Mod
  game: test
  version: 1.0.0
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  ports:
    - name: game
      containerPort: 25565
  configSchema:
    - name: DIFFICULTY
      type: enum
      enum: ["easy", "normal", "hard"]
      default: "normal"
    - name: BAD_ENUM
      type: enum
      options: ["a", "b"]
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-enum-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range report.Findings {
		if f.Field == "spec.configSchema[0].enum" {
			t.Errorf("CRD-correct 'enum:' field must not be flagged, got: %+v", f)
		}
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleInvalidConfigType && f.Field == "spec.configSchema[1].enum" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an enum field spelled with legacy 'options:' to be flagged as missing 'enum:'")
	}
}

func TestValidate_BooleanTypeRejected(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-bool-mod
displayName: Test Bool Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-bool-mod
spec:
  displayName: Test Bool Mod
  game: test
  version: 1.0.0
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  ports:
    - name: game
      containerPort: 25565
  configSchema:
    - name: HARDCORE
      type: boolean
      default: "true"
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-bool-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if report.Clean {
		t.Errorf("expected 'type: boolean' to be rejected since the CRD only accepts 'bool'")
	}
	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleInvalidConfigType && strings.Contains(f.Message, `"boolean"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid-config-type finding naming 'boolean', got: %+v", report.Findings)
	}
}

func TestValidate_MissingCRDRequiredFields(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-required-mod
displayName: Test Required Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-required-mod
spec:
  game: test
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  ports:
    - containerPort: 25565
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-required-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	wantFields := map[string]bool{
		"spec.displayName":   false,
		"spec.version":       false,
		"spec.ports[0].name": false,
	}
	for _, f := range report.Findings {
		if _, ok := wantFields[f.Field]; ok {
			wantFields[f.Field] = true
		}
	}
	for field, gotIt := range wantFields {
		if !gotIt {
			t.Errorf("expected a finding for missing required field %q", field)
		}
	}
}

func TestValidate_ReportJSON(t *testing.T) {
	report := ValidationReport{
		Timestamp:    "2026-08-27T12:00:00Z",
		Clean:        false,
		ErrorCount:   1,
		WarningCount: 1,
		Modules: []ModuleReport{
			{
				Name:  "test-mod",
				Path:  "/path/to/test-mod",
				Clean: false,
				Findings: []Finding{
					{
						Level:       SeverityError,
						RuleID:      RuleInvalidPortNumber,
						File:        "template.yaml",
						Line:        10,
						Field:       "spec.ports[0].containerPort",
						Message:     "port out of range",
						Remediation: "fix port",
					},
					{
						Level:       SeverityWarn,
						RuleID:      RuleUnrecognizedCategory,
						File:        "module.yaml",
						Line:        7,
						Field:       "categories",
						Message:     "unrecognized category",
						Remediation: "use canonical category",
					},
				},
			},
		},
	}

	out, err := report.ToJSON()
	if err != nil {
		t.Fatalf("failed to serialize JSON: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid json produced: %v", err)
	}

	if parsed["clean"] != false || parsed["errorCount"].(float64) != 1 {
		t.Errorf("unexpected parsed json values: %+v", parsed)
	}

	humanOut := report.FormatHuman()
	if !strings.Contains(humanOut, "test-mod") || !strings.Contains(humanOut, "ERROR") {
		t.Errorf("human output missing expected text:\n%s", humanOut)
	}
}

func TestValidate_RealModuleCS2(t *testing.T) {
	cs2Dir := filepath.Join("..", "..", "..", "modules", "cs2")
	if _, err := os.Stat(cs2Dir); os.IsNotExist(err) {
		t.Skip("modules/cs2 does not exist, skipping")
	}

	report, err := ValidateDirectory(cs2Dir, ValidateOptions{})
	if err != nil {
		t.Fatalf("failed to validate cs2: %v", err)
	}

	if !report.Clean {
		t.Errorf("expected cs2 to be clean, got findings: %+v", report.Findings)
	}
}

func TestValidate_DirectoryErrors(t *testing.T) {
	tempDir := t.TempDir()
	nonExistent := filepath.Join(tempDir, "does-not-exist")
	if _, err := ValidateDirectory(nonExistent, ValidateOptions{}); err == nil {
		t.Errorf("expected error for non-existent directory")
	}

	filePath := filepath.Join(tempDir, "regular-file")
	_ = os.WriteFile(filePath, []byte("hello"), 0o644)
	if _, err := ValidateDirectory(filePath, ValidateOptions{}); err == nil {
		t.Errorf("expected error when path is not a directory")
	}
}

func TestValidationReport(t *testing.T) {
	repEmpty := NewValidationReport(nil)
	if !repEmpty.Clean || repEmpty.ErrorCount != 0 || repEmpty.WarningCount != 0 {
		t.Errorf("expected empty report to be clean with 0 counts")
	}

	modReports := []ModuleReport{
		{
			Name:  "mod-ok",
			Clean: true,
		},
		{
			Name:  "mod-err",
			Clean: false,
			Findings: []Finding{
				{
					RuleID:      "TEST-001",
					Level:       SeverityError,
					File:        "module.yaml",
					Line:        10,
					Message:     "test error",
					Remediation: "fix it",
				},
				{
					RuleID:  "TEST-002",
					Level:   SeverityWarn,
					File:    "template.yaml",
					Message: "test warning",
				},
			},
		},
	}

	rep := NewValidationReport(modReports)
	if rep.Clean {
		t.Errorf("expected report to not be clean")
	}
	if rep.ErrorCount != 1 || rep.WarningCount != 1 {
		t.Errorf("expected 1 error and 1 warning, got %d, %d", rep.ErrorCount, rep.WarningCount)
	}

	human := rep.FormatHuman()
	if !strings.Contains(human, "OK (no findings)") || !strings.Contains(human, "TEST-001") || !strings.Contains(human, "Remediation: fix it") {
		t.Errorf("FormatHuman missing expected text:\n%s", human)
	}

	jsonBytes, err := rep.ToJSON()
	if err != nil || len(jsonBytes) == 0 {
		t.Errorf("ToJSON failed: %v", err)
	}
}

func TestValidate_TemplateApiVersion(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: test-mod\ndisplayName: Test Mod\nversion: 1.0.0\ngame: test\nsummary: Test\n"),
		"template.yaml": []byte("apiVersion: invalid/v1\nkind: GameTemplate\nmetadata:\n  name: test-mod\nspec:\n  game: test\n  image: \"ghcr.io/example/game@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\"\n"),
		"README.md":     []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleTemplateSchemaViolation && f.Field == "apiVersion" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected RuleTemplateSchemaViolation for invalid apiVersion, got: %+v", report.Findings)
	}
}

func TestFinding_JSONMarshalColumn(t *testing.T) {
	// Test that Column with value 0 is omitted from JSON (omitempty)
	finding0 := Finding{
		Level:       SeverityError,
		RuleID:      "test-rule",
		File:        "test.yaml",
		Line:        10,
		Column:      0,
		Message:     "test message",
		Remediation: "fix it",
	}

	jsonBytes, err := json.Marshal(finding0)
	if err != nil {
		t.Fatalf("failed to marshal Finding with Column=0: %v", err)
	}

	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if _, hasColumn := unmarshaled["column"]; hasColumn {
		t.Errorf("expected 'column' to be omitted from JSON when Column=0, but got: %s", string(jsonBytes))
	}

	// Test that Column with value > 0 is included in JSON
	finding5 := Finding{
		Level:       SeverityError,
		RuleID:      "test-rule",
		File:        "test.yaml",
		Line:        10,
		Column:      5,
		Message:     "test message",
		Remediation: "fix it",
	}

	jsonBytes, err = json.Marshal(finding5)
	if err != nil {
		t.Fatalf("failed to marshal Finding with Column=5: %v", err)
	}

	var unmarshaled2 map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &unmarshaled2); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if colVal, hasColumn := unmarshaled2["column"]; !hasColumn {
		t.Errorf("expected 'column' to be present in JSON when Column=5, but got: %s", string(jsonBytes))
	} else if colVal != float64(5) {
		t.Errorf("expected column value 5, got %v", colVal)
	}
}

func TestFormatHuman_LocationFormats(t *testing.T) {
	tests := []struct {
		name        string
		finding     Finding
		expectedLoc string
	}{
		{
			name: "file only (line and column both 0)",
			finding: Finding{
				Level:   SeverityWarn,
				RuleID:  "test",
				File:    "icon.png",
				Line:    0,
				Column:  0,
				Message: "size warning",
			},
			expectedLoc: "icon.png",
		},
		{
			name: "file:line (column 0)",
			finding: Finding{
				Level:   SeverityError,
				RuleID:  "test",
				File:    "module.yaml",
				Line:    5,
				Column:  0,
				Message: "invalid field",
			},
			expectedLoc: "module.yaml:5",
		},
		{
			name: "file:line:col (all present)",
			finding: Finding{
				Level:   SeverityError,
				RuleID:  "test",
				File:    "template.yaml",
				Line:    10,
				Column:  7,
				Message: "invalid protocol",
			},
			expectedLoc: "template.yaml:10:7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := ValidationReport{
				Clean:   false,
				Modules: []ModuleReport{{Name: "test", Findings: []Finding{tt.finding}}},
			}
			humanOutput := report.FormatHuman()
			if !strings.Contains(humanOutput, tt.expectedLoc) {
				t.Errorf("expected location format %q in output, got:\n%s", tt.expectedLoc, humanOutput)
			}
		})
	}
}

func TestValidate_InvalidPortProtocolWithColumn(t *testing.T) {
	// Test that invalid port protocol finding has a non-zero Column value from the yaml node
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "example.com/img@sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  ports:
    - name: test
      containerPort: 8080
      protocol: ICMP
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleInvalidPortProtocol {
			found = true
			if f.Column == 0 {
				t.Errorf("expected non-zero Column for protocol error, got 0 (line: %d)", f.Line)
			}
			// The "protocol: ICMP" line should have column pointing to the value node
			// With proper indentation (6 spaces), the protocol key should be at column 7
			if f.Line == 0 {
				t.Errorf("expected non-zero Line for protocol error")
			}
			break
		}
	}

	if !found {
		t.Errorf("expected invalid-port-protocol error in findings")
	}
}

func TestValidate_ImageUnpinnedWithColumn(t *testing.T) {
	// Test that unpinned image finding has column information from the yaml node
	files := map[string][]byte{
		"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
`),
		"template.yaml": []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  image: "docker.io/mygame:latest"
`),
		"README.md": []byte("# Test\n"),
	}

	report, err := ValidateFiles("test-mod", files, ValidateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range report.Findings {
		if f.RuleID == RuleImageUnpinned {
			found = true
			// The image field should have column information from the yaml node
			if f.Line == 0 {
				t.Errorf("expected non-zero Line for image-unpinned error")
			}
			// Column should be set from imgNode.Column even if it's at spec level
			break
		}
	}

	if !found {
		t.Errorf("expected image-unpinned error in findings")
	}
}

func TestValidate_GameplaneMinVersionExceedsTool(t *testing.T) {
	pinnedTemplate := []byte(`apiVersion: gameplane.io/v1alpha1
kind: GameTemplate
metadata:
  name: test-mod
spec:
  game: test
  displayName: Test Mod
  version: "1.0.0"
  image: "docker.io/mygame/server:latest@sha256:4b9a8e23f0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7"
`)

	t.Run("above tool version warns", func(t *testing.T) {
		files := map[string][]byte{
			"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
gameplaneMinVersion: 9.0.0
`),
			"template.yaml": pinnedTemplate,
			"README.md":     []byte("# Test\n"),
		}

		report, err := ValidateFiles("test-mod", files, ValidateOptions{})
		if err != nil {
			t.Fatal(err)
		}

		var found *Finding
		for i, f := range report.Findings {
			if f.RuleID == RuleMinVersionExceedsTool {
				found = &report.Findings[i]
			}
		}
		if found == nil {
			t.Fatalf("expected a %s finding, got %+v", RuleMinVersionExceedsTool, report.Findings)
		}
		if found.Level != SeverityWarn {
			t.Errorf("expected %s finding to be a warning, got %s", RuleMinVersionExceedsTool, found.Level)
		}
		if !report.Clean {
			t.Errorf("expected report to stay clean on a warning without --strict, got findings: %+v", report.Findings)
		}
	})

	t.Run("at or below tool version is silent", func(t *testing.T) {
		files := map[string][]byte{
			"module.yaml": []byte(`apiVersion: gameplane.local/module/v1
name: test-mod
displayName: Test Mod
version: 1.0.0
game: test
summary: Test
gameplaneMinVersion: 1.0.0
`),
			"template.yaml": pinnedTemplate,
			"README.md":     []byte("# Test\n"),
		}

		report, err := ValidateFiles("test-mod", files, ValidateOptions{})
		if err != nil {
			t.Fatal(err)
		}

		for _, f := range report.Findings {
			if f.RuleID == RuleMinVersionExceedsTool {
				t.Errorf("did not expect a %s finding for gameplaneMinVersion == tool version, got %+v", RuleMinVersionExceedsTool, f)
			}
		}
	})
}
