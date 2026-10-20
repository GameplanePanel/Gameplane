// Package validator provides offline static validation and linting of module manifests.
package validator

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
)

var (
	semverRegex = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
	minVerRegex = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$`)
)

var allowedModuleKeys = map[string]bool{
	"apiVersion":          true,
	"name":                true,
	"displayName":         true,
	"version":             true,
	"game":                true,
	"categories":          true,
	"summary":             true,
	"homepage":            true,
	"license":             true,
	"gameplaneMinVersion": true,
	"icon":                true,
}

// validateModuleSchema checks module.yaml against module.schema.json rules.
func validateModuleSchema(node *yaml.Node) []Finding {
	var findings []Finding

	if node == nil || node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return []Finding{
			{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        1,
				Column:      0,
				Message:     "module.yaml is empty or malformed YAML",
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			},
		}
	}

	rootMap := node.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return []Finding{
			{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        rootMap.Line,
				Column:      rootMap.Column,
				Message:     "root of module.yaml must be a mapping",
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			},
		}
	}

	// Check unexpected fields
	for i := 0; i < len(rootMap.Content); i += 2 {
		keyNode := rootMap.Content[i]
		if !allowedModuleKeys[keyNode.Value] {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        keyNode.Line,
				Column:      keyNode.Column,
				Field:       keyNode.Value,
				Message:     fmt.Sprintf("unrecognized field %q in module.yaml", keyNode.Value),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		}
	}

	// Required fields: apiVersion, name, displayName, version, game, summary
	required := []string{"apiVersion", "name", "displayName", "version", "game", "summary"}
	for _, req := range required {
		kn := common.FindKeyNode(rootMap, req)
		vn := common.FindNode(node, req)
		if kn == nil || vn == nil {
			col := 0
			if kn != nil {
				col = kn.Column
			}
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        1,
				Column:      col,
				Field:       req,
				Message:     fmt.Sprintf("missing required field %q", req),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		}
	}

	// Check apiVersion const
	apiVerNode := common.FindNode(node, "apiVersion")
	if apiVerNode != nil {
		if apiVerNode.Value != "gameplane.local/module/v1" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        apiVerNode.Line,
				Column:      apiVerNode.Column,
				Field:       "apiVersion",
				Message:     fmt.Sprintf("apiVersion must be %q, got %q", "gameplane.local/module/v1", apiVerNode.Value),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		}
	}

	// Check displayName minLength
	dispNode := common.FindNode(node, "displayName")
	if dispNode != nil && len(dispNode.Value) < 1 {
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleMetadataSchemaViolation,
			File:        "module.yaml",
			Line:        dispNode.Line,
			Column:      dispNode.Column,
			Field:       "displayName",
			Message:     "displayName must not be empty",
			Remediation: "Review error line and correct invalid field format according to module.schema.json.",
		})
	}

	// Check version semver
	verNode := common.FindNode(node, "version")
	if verNode != nil && !semverRegex.MatchString(verNode.Value) {
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleMetadataSchemaViolation,
			File:        "module.yaml",
			Line:        verNode.Line,
			Column:      verNode.Column,
			Field:       "version",
			Message:     fmt.Sprintf("version %q does not conform to semantic versioning regex", verNode.Value),
			Remediation: "Review error line and correct invalid field format according to module.schema.json.",
		})
	}

	// Check summary minLength
	sumNode := common.FindNode(node, "summary")
	if sumNode != nil && len(sumNode.Value) < 1 {
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleMetadataSchemaViolation,
			File:        "module.yaml",
			Line:        sumNode.Line,
			Column:      sumNode.Column,
			Field:       "summary",
			Message:     "summary must not be empty",
			Remediation: "Review error line and correct invalid field format according to module.schema.json.",
		})
	}

	// Check game minLength
	gameNode := common.FindNode(node, "game")
	if gameNode != nil && len(gameNode.Value) < 1 {
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleMetadataSchemaViolation,
			File:        "module.yaml",
			Line:        gameNode.Line,
			Column:      gameNode.Column,
			Field:       "game",
			Message:     "game must not be empty",
			Remediation: "Review error line and correct invalid field format according to module.schema.json.",
		})
	}

	// Check homepage URI if present
	homeNode := common.FindNode(node, "homepage")
	if homeNode != nil && homeNode.Value != "" {
		u, err := url.ParseRequestURI(homeNode.Value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        homeNode.Line,
				Column:      homeNode.Column,
				Field:       "homepage",
				Message:     fmt.Sprintf("homepage %q is not a valid absolute URI", homeNode.Value),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		}
	}

	// Check gameplaneMinVersion semver if present
	gpMinNode := common.FindNode(node, "gameplaneMinVersion")
	if gpMinNode != nil && gpMinNode.Value != "" {
		if !minVerRegex.MatchString(gpMinNode.Value) {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleMetadataSchemaViolation,
				File:        "module.yaml",
				Line:        gpMinNode.Line,
				Column:      gpMinNode.Column,
				Field:       "gameplaneMinVersion",
				Message:     fmt.Sprintf("gameplaneMinVersion %q is not a valid semver", gpMinNode.Value),
				Remediation: "Review error line and correct invalid field format according to module.schema.json.",
			})
		} else if exceeds, err := semverExceeds(gpMinNode.Value, ToolVersion); err == nil && exceeds {
			findings = append(findings, Finding{
				Level:  SeverityWarn,
				RuleID: RuleMinVersionExceedsTool,
				File:   "module.yaml",
				Line:   gpMinNode.Line,
				Column: gpMinNode.Column,
				Field:  "gameplaneMinVersion",
				Message: fmt.Sprintf("gameplaneMinVersion %q is newer than this gp-module (%s); an operator running the same or an older release will refuse this module",
					gpMinNode.Value, ToolVersion),
				Remediation: "Lower gameplaneMinVersion to match a released Gameplane version, or upgrade gp-module/the operator before publishing.",
			})
		}
	}

	return findings
}

// semverExceeds reports whether a is a strictly higher release than b, using
// the same major.minor.patch (ignoring any pre-release/build metadata
// suffix) that gameplaneMinVersion and gp-module's own version follow. It
// errors if either string doesn't parse as at least major.minor.patch,
// so callers should validate with minVerRegex/semverRegex first.
func semverExceeds(a, b string) (bool, error) {
	aParts, err := parseSemverCore(a)
	if err != nil {
		return false, err
	}
	bParts, err := parseSemverCore(b)
	if err != nil {
		return false, err
	}
	for i := 0; i < 3; i++ {
		if aParts[i] != bParts[i] {
			return aParts[i] > bParts[i], nil
		}
	}
	return false, nil
}

// parseSemverCore extracts the [major, minor, patch] integers from the front
// of a semver string, ignoring any "-prerelease" or "+build" suffix.
func parseSemverCore(v string) ([3]int, error) {
	var out [3]int
	core := v
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.SplitN(core, ".", 3)
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not in major.minor.patch form", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, fmt.Errorf("version %q has a non-numeric component %q: %w", v, p, err)
		}
		out[i] = n
	}
	return out, nil
}

// validateTemplateSchema checks template.yaml against GameTemplate CRD basics.
func validateTemplateSchema(node *yaml.Node) []Finding {
	var findings []Finding

	if node == nil || node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return []Finding{
			{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        1,
				Column:      0,
				Message:     "template.yaml is empty or malformed YAML",
				Remediation: "Correct the malformed YAML field matching the schema definition.",
			},
		}
	}

	rootMap := node.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return []Finding{
			{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        rootMap.Line,
				Column:      rootMap.Column,
				Message:     "root of template.yaml must be a mapping",
				Remediation: "Correct the malformed YAML field matching the schema definition.",
			},
		}
	}

	apiVerNode := common.FindNode(node, "apiVersion")
	if apiVerNode == nil || (apiVerNode.Value != "gameplane.local/v1alpha1" && apiVerNode.Value != "gameplane.io/v1alpha1") {
		line := 1
		col := 0
		val := ""
		if apiVerNode != nil {
			line = apiVerNode.Line
			col = apiVerNode.Column
			val = apiVerNode.Value
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleTemplateSchemaViolation,
			File:        "template.yaml",
			Line:        line,
			Column:      col,
			Field:       "apiVersion",
			Message:     fmt.Sprintf("apiVersion must be %q, got %q", "gameplane.local/v1alpha1", val),
			Remediation: "Set 'apiVersion: gameplane.local/v1alpha1' at the root of template.yaml.",
		})
	}

	kindNode := common.FindNode(node, "kind")
	if kindNode == nil || kindNode.Value != "GameTemplate" {
		line := 1
		col := 0
		if kindNode != nil {
			line = kindNode.Line
			col = kindNode.Column
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleTemplateSchemaViolation,
			File:        "template.yaml",
			Line:        line,
			Column:      col,
			Field:       "kind",
			Message:     "kind must be \"GameTemplate\"",
			Remediation: "Correct the malformed YAML field matching the schema definition.",
		})
	}

	metaNameNode := common.FindNode(node, "metadata.name")
	if metaNameNode == nil || metaNameNode.Value == "" {
		line := 1
		if metaNode := common.FindNode(node, "metadata"); metaNode != nil {
			line = metaNode.Line
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleTemplateSchemaViolation,
			File:        "template.yaml",
			Line:        line,
			Field:       "metadata.name",
			Message:     "metadata.name is required in template.yaml",
			Remediation: "Correct the malformed YAML field matching the schema definition.",
		})
	}

	specNode := common.FindNode(node, "spec")
	if specNode == nil || specNode.Kind != yaml.MappingNode {
		line := 1
		if specNode != nil {
			line = specNode.Line
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleTemplateSchemaViolation,
			File:        "template.yaml",
			Line:        line,
			Field:       "spec",
			Message:     "spec mapping is required in template.yaml",
			Remediation: "Correct the malformed YAML field matching the schema definition.",
		})
	} else {
		gameNode := common.FindNode(node, "spec.game")
		if gameNode == nil || gameNode.Value == "" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        specNode.Line,
				Field:       "spec.game",
				Message:     "spec.game is required in template.yaml",
				Remediation: "Specify the game identifier under spec.game.",
			})
		}
		imgNode := common.FindNode(node, "spec.image")
		if imgNode == nil || imgNode.Value == "" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        specNode.Line,
				Field:       "spec.image",
				Message:     "spec.image is required in template.yaml",
				Remediation: "Specify the base container image under spec.image.",
			})
		}
		dispNode := common.FindNode(node, "spec.displayName")
		if dispNode == nil || dispNode.Value == "" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        specNode.Line,
				Field:       "spec.displayName",
				Message:     "spec.displayName is required in template.yaml",
				Remediation: "Specify a human-friendly label under spec.displayName.",
			})
		}
		specVerNode := common.FindNode(node, "spec.version")
		if specVerNode == nil || specVerNode.Value == "" {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        specNode.Line,
				Field:       "spec.version",
				Message:     "spec.version is required in template.yaml",
				Remediation: "Specify the template revision under spec.version.",
			})
		}
	}

	return findings
}
