// Package validator provides offline static validation and linting of module manifests.
package validator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Severity indicates finding severity level.
type Severity string

// Finding severity level constants.
const (
	SeverityError Severity = "ERROR"
	SeverityWarn  Severity = "WARN"
)

// Rule ID constants matching contracts/diagnostics-contract.md.
const (
	RuleMissingRequiredFile     = "missing-required-file"
	RuleInvalidModuleName       = "invalid-module-name"
	RuleMetadataSchemaViolation = "metadata-schema-violation"
	RuleTemplateSchemaViolation = "template-schema-violation"
	RuleImageUnpinned           = "image-unpinned"
	RuleUnrecognizedCategory    = "unrecognized-category"
	RuleInvalidPortNumber       = "invalid-port-number"
	RuleInvalidPortProtocol     = "invalid-port-protocol"
	RuleDuplicatePortCollision  = "duplicate-port-collision"
	RuleInvalidConfigType       = "invalid-config-type"
	RuleInvalidMemoryPercent    = "invalid-memory-percent"
	RuleInsecureAuthField       = "credential-field-not-password"
	RuleExcessiveAssetSize      = "excessive-asset-size"
	RuleMinVersionExceedsTool   = "gameplane-min-version-exceeds-tool"
	RuleLocaleInvalid           = "locale-invalid"
	RuleLocaleIDInvalid         = "locale-id-invalid"
	RuleLocaleNeedsReview       = "locale-needs-review"
)

// ToolVersion is the gp-module release. cmd/gp-module surfaces it via
// --version, and validateModuleSchema compares it against a module's
// declared gameplaneMinVersion to warn authors who require a newer tool
// than the one running.
const ToolVersion = "1.0.0"

// Finding represents a single diagnostic finding.
type Finding struct {
	Level       Severity `json:"level"`
	RuleID      string   `json:"ruleId"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Column      int      `json:"column,omitempty"`
	Field       string   `json:"field,omitempty"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation"`
}

// ModuleReport contains the validation findings for a single module.
type ModuleReport struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Clean    bool      `json:"clean"`
	Findings []Finding `json:"findings"`
}

// ValidationReport contains aggregated findings across all validated modules.
type ValidationReport struct {
	Timestamp    string         `json:"timestamp"`
	Clean        bool           `json:"clean"`
	ErrorCount   int            `json:"errorCount"`
	WarningCount int            `json:"warningCount"`
	Modules      []ModuleReport `json:"modules"`
}

// NewValidationReport creates a new ValidationReport with current timestamp.
func NewValidationReport(modules []ModuleReport) ValidationReport {
	errCount := 0
	warnCount := 0
	clean := true

	if modules == nil {
		modules = []ModuleReport{}
	}

	for i := range modules {
		if modules[i].Findings == nil {
			modules[i].Findings = []Finding{}
		}
		if !modules[i].Clean {
			clean = false
		}
		for _, f := range modules[i].Findings {
			switch f.Level {
			case SeverityError:
				errCount++
			case SeverityWarn:
				warnCount++
			}
		}
	}

	return ValidationReport{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Clean:        clean,
		ErrorCount:   errCount,
		WarningCount: warnCount,
		Modules:      modules,
	}
}

// ToJSON returns indented JSON representation matching diagnostics-contract.md.
func (r *ValidationReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// FormatHuman returns a human-readable terminal output.
func (r *ValidationReport) FormatHuman() string {
	var sb strings.Builder

	for _, m := range r.Modules {
		fmt.Fprintf(&sb, "== %s ==\n", m.Name)
		if len(m.Findings) == 0 {
			sb.WriteString("  OK (no findings)\n")
		} else {
			for _, f := range m.Findings {
				loc := f.File
				if f.Line > 0 {
					if f.Column > 0 {
						loc = fmt.Sprintf("%s:%d:%d", f.File, f.Line, f.Column)
					} else {
						loc = fmt.Sprintf("%s:%d", f.File, f.Line)
					}
				}
				fmt.Fprintf(&sb, "  %-5s [%s] %s: %s\n", f.Level, f.RuleID, loc, f.Message)
				if f.Remediation != "" {
					fmt.Fprintf(&sb, "        Remediation: %s\n", f.Remediation)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "SUMMARY: %d module(s) checked, %d error(s), %d warning(s).\n",
		len(r.Modules), r.ErrorCount, r.WarningCount)

	return sb.String()
}
