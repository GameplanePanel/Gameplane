// Package validator provides offline static validation and linting of module manifests.
package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
)

// ValidateOptions controls validation flags and strictness.
type ValidateOptions struct {
	Strict  bool
	Offline bool
}

// ValidateDirectory reads files from a directory and executes all offline validation rules.
func ValidateDirectory(dirPath string, opts ValidateOptions) (*ModuleReport, error) {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve directory path %q: %w", dirPath, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("module directory %q does not exist: %w", absPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %q is not a directory", absPath)
	}

	dirName := filepath.Base(absPath)
	files := make(map[string][]byte)
	var totalSize int64

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %q: %w", absPath, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			if entry.Name() == "locales" {
				if err := readLocaleFiles(absPath, files); err != nil {
					return nil, err
				}
			}
			continue
		}
		path := filepath.Join(absPath, entry.Name())
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", path, err)
		}
		files[entry.Name()] = data
		totalSize += int64(len(data))
	}

	report, err := ValidateFiles(dirName, files, opts)
	if err != nil {
		return nil, err
	}
	report.Path = absPath

	// Asset size checks
	var iconSize int64
	if iconBytes, ok := files["icon.png"]; ok {
		iconSize = int64(len(iconBytes))
	}
	sizeFindings := validateAssetSizes(iconSize, totalSize)
	report.Findings = append(report.Findings, sizeFindings...)

	// Re-evaluate clean
	report.Clean = isClean(report.Findings, opts.Strict)
	return report, nil
}

// readLocaleFiles adds every regular locales/*.json file of the module to
// files under the key "locales/<name>". Symlinks and other entries are skipped.
// They do not count towards the asset size checks.
func readLocaleFiles(absPath string, files map[string][]byte) error {
	dir := filepath.Join(absPath, "locales")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %q: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return fmt.Errorf("failed to read file %q: %w", path, err)
		}
		files["locales/"+entry.Name()] = data
	}
	return nil
}

// ValidateFiles executes all validation rules against an in-memory map of file contents.
func ValidateFiles(dirName string, files map[string][]byte, opts ValidateOptions) (*ModuleReport, error) {
	var findings []Finding

	// Rule: missing-required-file
	requiredFiles := []string{"module.yaml", "template.yaml", "README.md"}
	for _, req := range requiredFiles {
		if _, ok := files[req]; !ok {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMissingRequiredFile,
				File:        req,
				Line:        0,
				Column:      0,
				Message:     fmt.Sprintf("required file %q is missing", req),
				Remediation: "Create the missing required file or re-scaffold using 'gp-module init'.",
			})
		}
	}

	// Validate module.yaml
	var moduleName string
	if modBytes, ok := files["module.yaml"]; ok {
		node, err := common.ParseYAMLNode(modBytes)
		if err != nil {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        1,
				Column:      0,
				Message:     fmt.Sprintf("failed to parse YAML: %v", err),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		} else {
			// Schema checks
			findings = append(findings, validateModuleSchema(node)...)

			// Module name rule
			nameNode := common.FindNode(node, "name")
			if nameNode != nil {
				moduleName = nameNode.Value
				findings = append(findings, validateModuleName(nameNode.Value, dirName, node)...)
			}

			// Categories rule
			findings = append(findings, validateCategories(node)...)
		}
	}

	// Validate template.yaml
	if tmplBytes, ok := files["template.yaml"]; ok {
		node, err := common.ParseYAMLNode(tmplBytes)
		if err != nil {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        1,
				Column:      0,
				Message:     fmt.Sprintf("failed to parse YAML: %v", err),
				Remediation: "Correct the malformed YAML field matching the schema definition.",
			})
		} else {
			findings = append(findings, validateTemplateSchema(node)...)
			findings = append(findings, validateImageDigest(node, string(tmplBytes))...)
			findings = append(findings, validatePorts(node)...)
			findings = append(findings, validateConfigSchemaRules(node)...)
		}
	}

	findings = append(findings, validateLocales(files)...)
	findings = append(findings, validateLocaleIDs(files)...)

	repName := dirName
	if repName == "" || repName == "." {
		repName = moduleName
	}

	clean := isClean(findings, opts.Strict)
	if findings == nil {
		findings = []Finding{}
	}

	return &ModuleReport{
		Name:     repName,
		Path:     dirName,
		Clean:    clean,
		Findings: findings,
	}, nil
}

func isClean(findings []Finding, strict bool) bool {
	if strict {
		return len(findings) == 0
	}
	for _, f := range findings {
		if f.Level == SeverityError {
			return false
		}
	}
	return true
}
