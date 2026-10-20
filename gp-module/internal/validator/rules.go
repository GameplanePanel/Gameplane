// Package validator provides offline static validation and linting of module manifests.
package validator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
)

var sha256DigestRegex = regexp.MustCompile(`@sha256:[0-9a-fA-F]{64}$`)

var allowedConfigTypes = map[string]bool{
	"string":   true,
	"int":      true,
	"bool":     true,
	"enum":     true,
	"password": true,
}

var credentialKeywords = []string{
	"PASSWORD",
	"TOKEN",
	"SECRET",
	"KEY",
	"AUTH",
}

func validateModuleName(name string, dirName string, node *yaml.Node) []Finding {
	var findings []Finding

	nameNode := common.FindNode(node, "name")
	line := 1
	if nameNode != nil {
		line = nameNode.Line
	}

	if err := common.ValidateModuleName(name); err != nil {
		col := 0
		if nameNode != nil {
			col = nameNode.Column
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleInvalidModuleName,
			File:        "module.yaml",
			Line:        line,
			Column:      col,
			Field:       "name",
			Message:     fmt.Sprintf("module name %q is invalid: %v", name, err),
			Remediation: "Rename the directory or update 'name' in module.yaml to match lower-case alphanumeric DNS-1123 format.",
		})
	} else if dirName != "" && dirName != "." && dirName != name {
		col := 0
		if nameNode != nil {
			col = nameNode.Column
		}
		findings = append(findings, Finding{
			Level:       SeverityError,
			RuleID:      RuleInvalidModuleName,
			File:        "module.yaml",
			Line:        line,
			Column:      col,
			Field:       "name",
			Message:     fmt.Sprintf("module name %q does not match directory name %q", name, dirName),
			Remediation: "Rename the directory or update 'name' in module.yaml to match lower-case alphanumeric DNS-1123 format.",
		})
	}

	return findings
}

func validateCategories(node *yaml.Node) []Finding {
	var findings []Finding
	catNode := common.FindNode(node, "categories")
	if catNode == nil || catNode.Kind != yaml.SequenceNode {
		return nil
	}

	for _, item := range catNode.Content {
		if !common.IsCanonicalCategory(item.Value) {
			findings = append(findings, Finding{
				Level:       SeverityWarn,
				RuleID:      RuleUnrecognizedCategory,
				File:        "module.yaml",
				Line:        item.Line,
				Column:      item.Column,
				Field:       "categories",
				Message:     fmt.Sprintf("category %q is not in the canonical Gameplane taxonomy", item.Value),
				Remediation: "Use a canonical category where possible to align with catalog filter chips.",
			})
		}
	}

	return findings
}

func validateImageDigest(node *yaml.Node, rawContent string) []Finding {
	var findings []Finding
	imgNode := common.FindNode(node, "spec.image")
	if imgNode == nil {
		return nil
	}

	val := imgNode.Value
	if sha256DigestRegex.MatchString(val) {
		// Image is pinned
		return nil
	}

	// Check for floating opt-out comment
	if strings.Contains(imgNode.LineComment, "gameplane:floating") ||
		strings.Contains(imgNode.HeadComment, "gameplane:floating") {
		return nil
	}

	// Also check raw line in case comment wasn't attached to the node
	lines := strings.Split(rawContent, "\n")
	if imgNode.Line > 0 && imgNode.Line <= len(lines) {
		lineStr := lines[imgNode.Line-1]
		if strings.Contains(lineStr, "gameplane:floating") {
			return nil
		}
	}

	findings = append(findings, Finding{
		Level:       SeverityError,
		RuleID:      RuleImageUnpinned,
		File:        "template.yaml",
		Line:        imgNode.Line,
		Column:      imgNode.Column,
		Field:       "spec.image",
		Message:     fmt.Sprintf("default image %q is not pinned with @sha256 digest", val),
		Remediation: "Resolve the tag to a digest (e.g. 'docker buildx imagetools inspect <image>' or 'crane digest <image>') and append '@sha256:<digest>' to spec.image, or append '# gameplane:floating' if intentionally dynamic. 'make module-pin' re-resolves every module in the catalog and is meant for the periodic refresh job, not a single module.",
	})

	return findings
}

func validatePorts(node *yaml.Node) []Finding {
	var findings []Finding
	portsNode := common.FindNode(node, "spec.ports")
	if portsNode == nil || portsNode.Kind != yaml.SequenceNode {
		return nil
	}

	type portKey struct {
		port  int
		proto string
	}
	seen := make(map[portKey]int)

	for i, pNode := range portsNode.Content {
		if pNode.Kind != yaml.MappingNode {
			continue
		}

		portField := common.FindNode(pNode, "containerPort")
		protoField := common.FindNode(pNode, "protocol")
		nameField := common.FindNode(pNode, "name")

		if nameField == nil || nameField.Value == "" {
			col := 0
			line := pNode.Line
			if nameField != nil {
				col = nameField.Column
				line = nameField.Line
			}
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleTemplateSchemaViolation,
				File:        "template.yaml",
				Line:        line,
				Column:      col,
				Field:       fmt.Sprintf("spec.ports[%d].name", i),
				Message:     "name is required for each port entry",
				Remediation: "Add a DNS-label 'name' (e.g. \"game\") to this port entry.",
			})
		}

		proto := "TCP"
		protoLine := pNode.Line
		if protoField != nil {
			proto = protoField.Value
			protoLine = protoField.Line
		}

		if proto != "TCP" && proto != "UDP" {
			col := 0
			if protoField != nil {
				col = protoField.Column
			}
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleInvalidPortProtocol,
				File:        "template.yaml",
				Line:        protoLine,
				Column:      col,
				Field:       fmt.Sprintf("spec.ports[%d].protocol", i),
				Message:     fmt.Sprintf("protocol %q is invalid (must be TCP or UDP)", proto),
				Remediation: "Change protocol to either TCP or UDP.",
			})
		}

		if portField == nil {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleInvalidPortNumber,
				File:        "template.yaml",
				Line:        pNode.Line,
				Column:      pNode.Column,
				Field:       fmt.Sprintf("spec.ports[%d].containerPort", i),
				Message:     "containerPort is required",
				Remediation: "Change containerPort to an integer between 1 and 65535.",
			})
			continue
		}

		portNum, err := strconv.Atoi(portField.Value)
		if err != nil || portNum < 1 || portNum > 65535 {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleInvalidPortNumber,
				File:        "template.yaml",
				Line:        portField.Line,
				Column:      portField.Column,
				Field:       fmt.Sprintf("spec.ports[%d].containerPort", i),
				Message:     fmt.Sprintf("containerPort %q is outside allowable range 1-65535", portField.Value),
				Remediation: "Change containerPort to an integer between 1 and 65535.",
			})
			continue
		}

		key := portKey{port: portNum, proto: proto}
		if prevLine, ok := seen[key]; ok {
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleDuplicatePortCollision,
				File:        "template.yaml",
				Line:        portField.Line,
				Column:      portField.Column,
				Field:       fmt.Sprintf("spec.ports[%d]", i),
				Message:     fmt.Sprintf("duplicate port declaration %d/%s (already declared on line %d)", portNum, proto, prevLine),
				Remediation: "Assign unique port numbers or change protocol.",
			})
		} else {
			seen[key] = portField.Line
		}
	}

	return findings
}

func validateConfigSchemaRules(node *yaml.Node) []Finding {
	var findings []Finding
	cfgNode := common.FindNode(node, "spec.configSchema")
	if cfgNode == nil || cfgNode.Kind != yaml.SequenceNode {
		return nil
	}

	for i, item := range cfgNode.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}

		nameNode := common.FindNode(item, "name")
		typeNode := common.FindNode(item, "type")

		fieldName := ""
		nameLine := item.Line
		if nameNode != nil {
			fieldName = nameNode.Value
			nameLine = nameNode.Line
		}

		fieldType := ""
		typeLine := item.Line
		if typeNode != nil {
			fieldType = typeNode.Value
			typeLine = typeNode.Line
		}

		if fieldType != "" && !allowedConfigTypes[fieldType] {
			col := 0
			if typeNode != nil {
				col = typeNode.Column
			}
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleInvalidConfigType,
				File:        "template.yaml",
				Line:        typeLine,
				Column:      col,
				Field:       fmt.Sprintf("spec.configSchema[%d].type", i),
				Message:     fmt.Sprintf("configSchema type %q is invalid (allowed: string, int, bool, enum, password)", fieldType),
				Remediation: "Change field type to one of the supported Gameplane types.",
			})
		}

		defaultNode := common.FindNode(item, "default")
		defaultVal := ""
		if defaultNode != nil {
			defaultVal = defaultNode.Value
		}

		// Type-specific default and enum validation
		switch fieldType {
		case "int":
			if defaultNode != nil && defaultVal != "" {
				if _, err := strconv.Atoi(defaultVal); err != nil {
					findings = append(findings, Finding{
						Level:       SeverityError,
						RuleID:      RuleInvalidConfigType,
						File:        "template.yaml",
						Line:        defaultNode.Line,
						Column:      defaultNode.Column,
						Field:       fmt.Sprintf("spec.configSchema[%d].default", i),
						Message:     fmt.Sprintf("configSchema field %q default value %q is not a valid integer", fieldName, defaultVal),
						Remediation: "Provide a valid integer default value.",
					})
				}
			}
		case "bool":
			if defaultNode != nil && defaultVal != "" {
				lower := strings.ToLower(defaultVal)
				if lower != "true" && lower != "false" {
					findings = append(findings, Finding{
						Level:       SeverityError,
						RuleID:      RuleInvalidConfigType,
						File:        "template.yaml",
						Line:        defaultNode.Line,
						Column:      defaultNode.Column,
						Field:       fmt.Sprintf("spec.configSchema[%d].default", i),
						Message:     fmt.Sprintf("configSchema field %q default value %q is not a valid boolean (true/false)", fieldName, defaultVal),
						Remediation: "Provide 'true' or 'false' as the boolean default value.",
					})
				}
			}
		case "enum":
			optsNode := common.FindNode(item, "enum")
			if optsNode == nil || optsNode.Kind != yaml.SequenceNode || len(optsNode.Content) == 0 {
				col := item.Column
				if optsNode != nil {
					col = optsNode.Column
				}
				findings = append(findings, Finding{
					Level:       SeverityError,
					RuleID:      RuleInvalidConfigType,
					File:        "template.yaml",
					Line:        item.Line,
					Column:      col,
					Field:       fmt.Sprintf("spec.configSchema[%d].enum", i),
					Message:     fmt.Sprintf("configSchema enum field %q must specify a non-empty enum list", fieldName),
					Remediation: "Provide an 'enum' array with at least one allowed value.",
				})
			} else if defaultNode != nil && defaultVal != "" {
				found := false
				for _, opt := range optsNode.Content {
					if opt.Value == defaultVal {
						found = true
						break
					}
				}
				if !found {
					findings = append(findings, Finding{
						Level:       SeverityError,
						RuleID:      RuleInvalidConfigType,
						File:        "template.yaml",
						Line:        defaultNode.Line,
						Column:      defaultNode.Column,
						Field:       fmt.Sprintf("spec.configSchema[%d].default", i),
						Message:     fmt.Sprintf("configSchema enum field %q default %q is not in enum list", fieldName, defaultVal),
						Remediation: "Set default to one of the declared enum values.",
					})
				}
			}
		}

		// Credential name check
		upper := strings.ToUpper(fieldName)
		isCred := false
		// Split field name on '_' and '-' to match whole tokens only
		tokens := strings.FieldsFunc(upper, func(r rune) bool {
			return r == '_' || r == '-'
		})
		for _, token := range tokens {
			for _, kw := range credentialKeywords {
				if token == kw {
					isCred = true
					break
				}
			}
			if isCred {
				break
			}
		}

		if isCred && fieldType != "password" && fieldType != "" {
			col := 0
			if nameNode != nil {
				col = nameNode.Column
			}
			findings = append(findings, Finding{
				Level:       SeverityError,
				RuleID:      RuleInsecureAuthField,
				File:        "template.yaml",
				Line:        nameLine,
				Column:      col,
				Field:       fmt.Sprintf("spec.configSchema[%d].name", i),
				Message:     fmt.Sprintf("field %q appears to be a credential but has type %q instead of \"password\"", fieldName, fieldType),
				Remediation: "Set 'type: password' so the operator stores value securely in a Secret instead of plaintext CR.",
			})
		}

		// autoFromMemoryLimit percent check
		memNode := common.FindNode(item, "autoFromMemoryLimit")
		if memNode != nil {
			pctNode := common.FindNode(memNode, "percent")
			if pctNode == nil {
				findings = append(findings, Finding{
					Level:       SeverityError,
					RuleID:      RuleInvalidMemoryPercent,
					File:        "template.yaml",
					Line:        memNode.Line,
					Column:      memNode.Column,
					Field:       fmt.Sprintf("spec.configSchema[%d].autoFromMemoryLimit.percent", i),
					Message:     fmt.Sprintf("autoFromMemoryLimit for field %q requires 'percent' property", fieldName),
					Remediation: "Specify 'percent: <1-100>' inside autoFromMemoryLimit.",
				})
			} else {
				pctVal, err := strconv.Atoi(pctNode.Value)
				if err != nil || pctVal < 1 || pctVal > 100 {
					findings = append(findings, Finding{
						Level:       SeverityError,
						RuleID:      RuleInvalidMemoryPercent,
						File:        "template.yaml",
						Line:        pctNode.Line,
						Column:      pctNode.Column,
						Field:       fmt.Sprintf("spec.configSchema[%d].autoFromMemoryLimit.percent", i),
						Message:     fmt.Sprintf("autoFromMemoryLimit percent %q must be an integer between 1 and 100", pctNode.Value),
						Remediation: "Set percent between 1 and 100 (e.g. 75 for 75%).",
					})
				}
			}
		}
	}

	return findings
}

func validateAssetSizes(iconSize int64, totalSize int64) []Finding {
	var findings []Finding

	const maxIconSize = 512 * 1024    // 512 KiB
	const maxBundleSize = 1024 * 1024 // 1 MiB

	if iconSize > maxIconSize {
		findings = append(findings, Finding{
			Level:       SeverityWarn,
			RuleID:      RuleExcessiveAssetSize,
			File:        "icon.png",
			Line:        0,
			Column:      0,
			Message:     fmt.Sprintf("icon asset size (%d bytes) exceeds recommended 512 KiB limit", iconSize),
			Remediation: "Compress asset or reduce resolution to optimize OCI bundle transfer.",
		})
	}

	if totalSize > maxBundleSize {
		findings = append(findings, Finding{
			Level:       SeverityWarn,
			RuleID:      RuleExcessiveAssetSize,
			File:        "module directory",
			Line:        0,
			Column:      0,
			Message:     fmt.Sprintf("total module size (%d bytes) exceeds recommended 1 MiB limit", totalSize),
			Remediation: "Compress asset or reduce resolution to optimize OCI bundle transfer.",
		})
	}

	return findings
}
