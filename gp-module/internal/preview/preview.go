// Package preview evaluates runtime manifests and dynamic memory calculations.
package preview

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options defines inputs for generating a dry-run preview.
type Options struct {
	ModuleDir    string            `json:"moduleDir,omitempty"`
	TemplateYAML []byte            `json:"templateYaml,omitempty"`
	VersionID    string            `json:"versionId,omitempty"`
	MemoryLimit  string            `json:"memoryLimit,omitempty"`
	UserConfig   map[string]string `json:"userConfig,omitempty"`
}

// PortPreview represents an exposed network port.
type PortPreview struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	Advertise     bool   `json:"advertise"`
}

// StoragePreview represents module storage settings.
type StoragePreview struct {
	Size      string `json:"size"`
	MountPath string `json:"mountPath"`
}

// ConfigFieldPreview describes a resolved configuration field with its provenance.
type ConfigFieldPreview struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Type   string `json:"type"`
	Source string `json:"source"`
}

// Result holds the synthesized runtime configuration.
type Result struct {
	Game             string               `json:"game"`
	DisplayName      string               `json:"displayName"`
	VersionID        string               `json:"versionId,omitempty"`
	EffectiveImage   string               `json:"effectiveImage"`
	EffectiveMemory  string               `json:"effectiveMemory"`
	ConfigFields     []ConfigFieldPreview `json:"configFields"`
	ComputedConfig   map[string]string    `json:"computedConfig"`
	EffectiveEnv     map[string]string    `json:"effectiveEnv"`
	Ports            []PortPreview        `json:"ports"`
	Storage          StoragePreview       `json:"storage"`
	RenderedManifest string               `json:"renderedManifest,omitempty"`
}

// GeneratePreview evaluates template.yaml against inputs and resolves effective runtime config.
func GeneratePreview(opts Options) (*Result, error) {
	yamlBytes := opts.TemplateYAML
	if len(yamlBytes) == 0 && opts.ModuleDir != "" {
		path := filepath.Join(opts.ModuleDir, "template.yaml")
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", path, err)
		}
		yamlBytes = data
	}

	if len(yamlBytes) == 0 {
		return nil, fmt.Errorf("no template.yaml content or module directory provided")
	}

	var root map[string]any
	if err := yaml.Unmarshal(yamlBytes, &root); err != nil {
		return nil, fmt.Errorf("failed to parse template.yaml: %w", err)
	}

	spec, _ := root["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("missing spec mapping in template.yaml")
	}

	displayName, _ := spec["displayName"].(string)
	game, _ := spec["game"].(string)
	defaultImage, _ := spec["image"].(string)

	memLimit := opts.MemoryLimit
	if memLimit == "" {
		memLimit = "4Gi"
	}

	effectiveImage := defaultImage
	selectedVer := ""
	var versionEnv []any

	// Version overlay. Mirrors the operator's resolveVersion
	// (operator/internal/controller/gameserver_version.go): an explicit
	// VersionID must match one of spec.versions; with no explicit choice,
	// the entry marked default wins, else the first declared version.
	// Only templates with no spec.versions at all skip this layer.
	if vers, ok := spec["versions"].([]any); ok && len(vers) > 0 {
		var chosen map[string]any
		if opts.VersionID != "" {
			for _, v := range vers {
				vMap, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if vid, _ := vMap["id"].(string); vid == opts.VersionID {
					chosen = vMap
					break
				}
			}
			if chosen == nil {
				return nil, fmt.Errorf("requested version %q not found in template.yaml", opts.VersionID)
			}
		} else {
			for _, v := range vers {
				vMap, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if def, _ := vMap["default"].(bool); def {
					chosen = vMap
					break
				}
			}
			if chosen == nil {
				if first, ok := vers[0].(map[string]any); ok {
					chosen = first
				}
			}
		}
		if chosen != nil {
			selectedVer, _ = chosen["id"].(string)
			if img, ok := chosen["image"].(string); ok && img != "" {
				effectiveImage = img
			}
			if envList, ok := chosen["env"].([]any); ok {
				versionEnv = envList
			}
		}
	}

	effectiveEnv := make(map[string]string)

	// 1. Base template env
	if baseEnv, ok := spec["env"].([]any); ok {
		for _, e := range baseEnv {
			if eMap, ok := e.(map[string]any); ok {
				k, _ := eMap["name"].(string)
				v := fmt.Sprintf("%v", eMap["value"])
				if k != "" {
					effectiveEnv[k] = v
				}
			}
		}
	}

	// 2. Version overlay env
	for _, e := range versionEnv {
		if eMap, ok := e.(map[string]any); ok {
			k, _ := eMap["name"].(string)
			v := fmt.Sprintf("%v", eMap["value"])
			if k != "" {
				effectiveEnv[k] = v
			}
		}
	}

	computedConfig := make(map[string]string)
	var configFields []ConfigFieldPreview

	// 3. ConfigSchema evaluation
	if schemaList, ok := spec["configSchema"].([]any); ok {
		for _, s := range schemaList {
			sMap, ok := s.(map[string]any)
			if !ok {
				continue
			}
			fieldName, _ := sMap["name"].(string)
			fieldType, _ := sMap["type"].(string)
			if fieldType == "" {
				fieldType = "string"
			}
			var defaultVal string
			if def, exists := sMap["default"]; exists && def != nil {
				defaultVal = fmt.Sprintf("%v", def)
			}
			target, _ := sMap["target"].(string)
			if target == "" {
				target = "env"
			}
			envName, _ := sMap["envName"].(string)
			if envName == "" {
				envName = fieldName
			}

			val := ""
			source := ""

			if userVal, exists := opts.UserConfig[fieldName]; exists && userVal != "" {
				val = userVal
				source = "user override"
			} else if defaultVal != "" {
				val = defaultVal
				source = "default"
			} else if autoMem, ok := sMap["autoFromMemoryLimit"].(map[string]any); ok {
				pct := 0
				switch p := autoMem["percent"].(type) {
				case int:
					pct = p
				case float64:
					if p != math.Trunc(p) {
						return nil, fmt.Errorf("field %q has non-integral autoFromMemoryLimit percent value %v", fieldName, p)
					}
					pct = int(p)
				}
				if pct <= 0 || pct > 100 {
					return nil, fmt.Errorf("field %q has invalid autoFromMemoryLimit percent %d (must be 1-100)", fieldName, pct)
				}
				calcVal, err := CalculateAutoMemory(memLimit, pct)
				if err != nil {
					return nil, fmt.Errorf("field %q: %w", fieldName, err)
				}
				val = calcVal
				source = fmt.Sprintf("autoFromMemoryLimit: %d%% of %s", pct, memLimit)
			}

			if val != "" {
				displayVal := val
				if fieldType == "password" {
					displayVal = "******"
				}
				computedConfig[fieldName] = displayVal
				configFields = append(configFields, ConfigFieldPreview{
					Name:   fieldName,
					Value:  displayVal,
					Type:   fieldType,
					Source: source,
				})

				if target == "env" {
					if fieldType == "password" {
						effectiveEnv[envName] = fmt.Sprintf("[secret: configSecret/%s]", fieldName)
					} else {
						effectiveEnv[envName] = val
					}
				}
			}
		}
	}

	// Ports
	var ports []PortPreview
	if portsList, ok := spec["ports"].([]any); ok {
		for _, p := range portsList {
			if pMap, ok := p.(map[string]any); ok {
				pName, _ := pMap["name"].(string)
				pPort := 0
				switch num := pMap["containerPort"].(type) {
				case int:
					pPort = num
				case float64:
					pPort = int(num)
				}
				pProto, _ := pMap["protocol"].(string)
				if pProto == "" {
					pProto = "TCP"
				}
				pAdv := true
				if advVal, ok := pMap["advertise"].(bool); ok {
					pAdv = advVal
				}
				ports = append(ports, PortPreview{
					Name:          pName,
					ContainerPort: pPort,
					Protocol:      pProto,
					Advertise:     pAdv,
				})
			}
		}
	}

	// Storage
	storage := StoragePreview{}
	if storMap, ok := spec["storage"].(map[string]any); ok {
		storage.Size, _ = storMap["size"].(string)
		storage.MountPath, _ = storMap["mountPath"].(string)
	}

	return &Result{
		Game:            game,
		DisplayName:     displayName,
		VersionID:       selectedVer,
		EffectiveImage:  effectiveImage,
		EffectiveMemory: memLimit,
		ConfigFields:    configFields,
		ComputedConfig:  computedConfig,
		EffectiveEnv:    effectiveEnv,
		Ports:           ports,
		Storage:         storage,
	}, nil
}

// ToJSON returns indented JSON representation.
func (p *Result) ToJSON() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// FormatHuman returns a formatted terminal representation.
func (p *Result) FormatHuman() string {
	var sb strings.Builder

	title := p.DisplayName
	if title == "" {
		title = p.Game
	}
	fmt.Fprintf(&sb, "== %s (%s) Preview ==\n", title, p.Game)
	fmt.Fprintf(&sb, "Selected Version: %s\n", p.VersionID)
	fmt.Fprintf(&sb, "Container Image:  %s\n", p.EffectiveImage)
	fmt.Fprintf(&sb, "Memory Limit:     %s\n\n", p.EffectiveMemory)

	sb.WriteString("Computed Configuration:\n")
	if len(p.ConfigFields) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for _, cf := range p.ConfigFields {
			fmt.Fprintf(&sb, "  %-16s %s (%s)\n", cf.Name+":", cf.Value, cf.Source)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("Effective Environment:\n")
	if len(p.EffectiveEnv) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		// Sort keys for deterministic output
		var keys []string
		for k := range p.EffectiveEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "  %-16s %s\n", k+":", p.EffectiveEnv[k])
		}
	}
	sb.WriteString("\n")

	sb.WriteString("Exposed Ports:\n")
	if len(p.Ports) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for _, port := range p.Ports {
			fmt.Fprintf(&sb, "  - %d/%s (%s)\n", port.ContainerPort, port.Protocol, port.Name)
		}
	}

	return sb.String()
}
